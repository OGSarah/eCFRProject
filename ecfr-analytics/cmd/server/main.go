package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"ecfr-analytics/internal/ecfr"
	"ecfr-analytics/internal/metrics"
	"ecfr-analytics/internal/store"
)

// main configures the HTTP server, routes, and shared dependencies.
func main() {
	baseURL := getenv("ECFR_BASE_URL", "https://www.ecfr.gov")
	dataDir := getenv("DATA_DIR", "./data")
	addr := getenv("ADDR", ":8080")

	if err := os.MkdirAll(filepath.Join(dataDir, "xml"), 0o755); err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("sqlite3", filepath.Join(dataDir, "ecfr.sqlite")+"?_busy_timeout=5000&_foreign_keys=1")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	st := store.New(db, dataDir)
	if err := st.InitSchema(); err != nil {
		log.Fatal(err)
	}

	cli := ecfr.NewClient(baseURL, 120*time.Second)

	// Static UI
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("./web")))

	// Health
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().Format(time.RFC3339)})
	})

	// Download/refresh current snapshot (manual trigger)
	mux.HandleFunc("/api/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()

		result, err := refreshCurrent(ctx, cli, st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	// List agencies (from stored admin feed)
	mux.HandleFunc("/api/agencies", func(w http.ResponseWriter, r *http.Request) {
		ag, err := st.ListAgencies(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, ag)
	})

	// Insights: outlier chapters for a selected agency.
	mux.HandleFunc("/api/insights/outliers", func(w http.ResponseWriter, r *http.Request) {
		slug := r.URL.Query().Get("slug")
		if slug == "" {
			http.Error(w, "slug required", http.StatusBadRequest)
			return
		}
		outliers, err := metrics.OutlierChaptersByAgency(r.Context(), st, slug, 5)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, outliers)
	})

	// Insights: growth hotspots for a window.
	mux.HandleFunc("/api/insights/growth", func(w http.ResponseWriter, r *http.Request) {
		days := 365
		if v := r.URL.Query().Get("days"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 3650 {
				days = n
			}
		}
		out, err := metrics.GrowthHotspots(r.Context(), st, days, 5)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})

	// Status / metadata
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		lastRefresh, err := st.GetState(r.Context(), "last_refresh")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"last_refresh": lastRefresh,
		})
	})

	// Latest metrics for all agencies
	// /api/metrics/latest?metric=word_count|words_per_chapter|checksum|churn|readability
	mux.HandleFunc("/api/metrics/latest", func(w http.ResponseWriter, r *http.Request) {
		metric := r.URL.Query().Get("metric")
		if metric == "" {
			metric = "word_count"
		}
		rows, err := st.LatestAgencyMetric(r.Context(), metric)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, rows)
	})

	// Timeseries per agency
	// /api/metrics/agency/{slug}/timeseries?metric=word_count|words_per_chapter|churn|readability&days=180
	mux.HandleFunc("/api/metrics/agency/", func(w http.ResponseWriter, r *http.Request) {
		// naive router
		path := r.URL.Path
		// /api/metrics/agency/{slug}/timeseries
		const prefix = "/api/metrics/agency/"
		if len(path) <= len(prefix) {
			http.NotFound(w, r)
			return
		}
		rest := path[len(prefix):]
		// rest = "{slug}/timeseries"
		parts := split2(rest, "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		slug := parts[0]
		action := parts[1]
		switch action {
		case "timeseries":
			metric := r.URL.Query().Get("metric")
			if metric == "" {
				metric = "word_count"
			}
			days := 180
			if v := r.URL.Query().Get("days"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 3650 {
					days = n
				}
			}
			out, err := st.AgencyMetricSeries(r.Context(), slug, metric, days)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, out)
		default:
			http.NotFound(w, r)
		}
	})

	log.Printf("Listening on %s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

// refreshCurrent downloads latest datasets, stores snapshots, and recomputes metrics.
func refreshCurrent(ctx context.Context, cli *ecfr.Client, st *store.Store) (map[string]any, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 1) Pull agencies + titles concurrently and store them
	var agencies []ecfr.Agency
	var titles []ecfr.Title
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		a, err := cli.GetAgencies(ctx)
		if err == nil {
			err = st.UpsertAgencies(ctx, a)
		}
		if err != nil {
			errCh <- err
			cancel()
			return
		}
		agencies = a
	}()
	go func() {
		defer wg.Done()
		t, err := cli.GetTitles(ctx)
		if err == nil {
			err = st.UpsertTitles(ctx, t)
		}
		if err != nil {
			errCh <- err
			cancel()
			return
		}
		titles = t
	}()
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
	}

	// 2) For each title, download full XML and store as gzip file (if not already)
	type job struct {
		title int
		date  string
	}
	jobs := make([]job, 0, len(titles))
	for _, t := range titles {
		if t.Reserved {
			continue
		}
		date := t.UpToDateAsOf // string "YYYY-MM-DD"
		exists, err := st.SnapshotExists(ctx, t.Number, date)
		if err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		jobs = append(jobs, job{title: t.Number, date: date})
	}

	workers := getenvInt("ECFR_DOWNLOAD_CONCURRENCY", 2)
	if workers < 1 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}

	jobCh := make(chan job)
	errCh = make(chan error, workers)
	var downloaded int64
	wg = sync.WaitGroup{}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				if ctx.Err() != nil {
					return
				}
				var lastErr error
				for attempt := 0; attempt < 3; attempt++ {
					rc, err := cli.GetFullTitleXMLStream(ctx, j.date, j.title)
					if err == nil {
						err = st.SaveSnapshotFromReader(ctx, j.title, j.date, rc)
						_ = rc.Close()
					}
					if err == nil {
						atomic.AddInt64(&downloaded, 1)
						lastErr = nil
						break
					}
					lastErr = err
					if !isRetryableDownloadErr(err) || attempt == 2 {
						break
					}
					delay := time.Duration(2<<attempt) * time.Second
					jitter := time.Duration(time.Now().UnixNano()%500) * time.Millisecond
					t := time.NewTimer(delay + jitter)
					select {
					case <-ctx.Done():
						t.Stop()
						return
					case <-t.C:
					}
				}
				if lastErr != nil {
					select {
					case errCh <- lastErr:
					default:
					}
					cancel()
					return
				}
			}
		}()
	}
sendLoop:
	for _, j := range jobs {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobCh <- j:
		}
	}
	close(jobCh)
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
	}

	// 4) Compute metrics for the newest snapshot date per title, rolled up to agencies
	if err := metrics.ComputeLatest(ctx, st); err != nil {
		return nil, err
	}

	computedAt := time.Now().Format(time.RFC3339)
	if err := st.SetState(ctx, "last_refresh", computedAt); err != nil {
		return nil, err
	}

	return map[string]any{
		"agencies":    len(agencies),
		"titles":      len(titles),
		"downloaded":  int(atomic.LoadInt64(&downloaded)),
		"computed_at": computedAt,
		"last_refresh": computedAt,
	}, nil
}

// getenv returns the environment variable or a default.
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// getenvInt returns an int environment variable or a default.
func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// writeJSON encodes the response as JSON with status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// withCORS adds permissive CORS headers for the API.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// split2 splits into at most two parts by the first separator.
func split2(s, sep string) []string {
	var out []string
	i := 0
	for {
		j := indexOf(s[i:], sep)
		if j < 0 {
			out = append(out, s[i:])
			break
		}
		j += i
		out = append(out, s[i:j])
		i = j + len(sep)
		if i >= len(s) {
			out = append(out, "")
			break
		}
	}
	return out
}

// isRetryableDownloadErr identifies transient download errors worth retrying.
func isRetryableDownloadErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	msg := err.Error()
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "Client.Timeout") {
		return true
	}
	return false
}

func indexOf(s, sub string) int {
	// tiny helper to avoid importing strings everywhere
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
