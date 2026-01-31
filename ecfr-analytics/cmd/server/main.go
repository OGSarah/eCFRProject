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

type serverDeps struct {
	refresh       func(ctx context.Context) (map[string]any, error)
	listAgencies  func(ctx context.Context) ([]map[string]any, error)
	latestMetrics func(ctx context.Context, metric string) ([]map[string]any, error)
	getState      func(ctx context.Context, key string) (string, error)
}

// main configures the HTTP server, routes, and shared dependencies.
func main() {
	baseURL := getenv("ECFR_BASE_URL", "https://www.ecfr.gov")
	dataDir := getenv("DATA_DIR", "./data")
	addr := getenv("ADDR", ":8080")
	dailyHour := getenvInt("ECFR_DAILY_REFRESH_HOUR", 2)

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
	var refreshMu sync.Mutex

	deps := serverDeps{
		refresh: func(ctx context.Context) (map[string]any, error) {
			refreshMu.Lock()
			result, err := refreshCurrent(ctx, cli, st)
			refreshMu.Unlock()
			return result, err
		},
		listAgencies: func(ctx context.Context) ([]map[string]any, error) {
			return st.ListAgencies(ctx)
		},
		latestMetrics: func(ctx context.Context, metric string) ([]map[string]any, error) {
			return st.LatestAgencyMetric(ctx, metric)
		},
		getState: func(ctx context.Context, key string) (string, error) {
			return st.GetState(ctx, key)
		},
	}

	// Run startup refresh in the background so server startup isn't blocked.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		_, err := deps.refresh(ctx)
		if err != nil {
			log.Printf("startup refresh failed: %v", err)
			return
		}
	}()

	// Daily refresh loop to pull new snapshots if available.
	go func() {
		for {
			next := nextDailyRun(time.Now(), dailyHour)
			timer := time.NewTimer(time.Until(next))
			<-timer.C
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			_, err := deps.refresh(ctx)
			cancel()
			if err != nil {
				log.Printf("daily refresh failed: %v", err)
			}
		}
	}()

	// Static UI
	mux := newMux("./web", deps)

	log.Printf("Server started")
	log.Printf("Listening on %s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func newMux(webDir string, deps serverDeps) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

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

		result, err := deps.refresh(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	// List agencies (from stored admin feed)
	mux.HandleFunc("/api/agencies", func(w http.ResponseWriter, r *http.Request) {
		ag, err := deps.listAgencies(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, ag)
	})

	// Latest metrics for all agencies
	// /api/metrics/latest?metric=word_count|words_per_chapter|checksum|churn|readability
	mux.HandleFunc("/api/metrics/latest", func(w http.ResponseWriter, r *http.Request) {
		metric := r.URL.Query().Get("metric")
		if metric == "" {
			metric = "word_count"
		}
		rows, err := deps.latestMetrics(r.Context(), metric)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, rows)
	})

	// App state lookup
	// /api/state?key=last_refresh
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "key required", http.StatusBadRequest)
			return
		}
		value, err := deps.getState(r.Context(), key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": value})
	})

	return mux
}

// refreshCurrent downloads latest datasets, stores snapshots, and recomputes metrics.
func refreshCurrent(ctx context.Context, cli *ecfr.Client, st *store.Store) (map[string]any, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	log.Printf("ECFR INGEST: starting download check")
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
		if !exists {
			jobs = append(jobs, job{title: t.Number, date: date})
		}
	}

	workers := getenvInt("ECFR_DOWNLOAD_CONCURRENCY", 2)
	if workers < 1 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}

	var downloaded int64
	if len(jobs) == 0 {
		log.Printf("ECFR INGEST: no new snapshots to download")
	} else {
		log.Printf("ECFR INGEST: downloading snapshots (%d jobs, %d workers)", len(jobs), workers)
		jobCh := make(chan job)
		errCh = make(chan error, workers)
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
						log.Printf("ECFR INGEST: download failed (title=%d date=%s): %v; continuing", j.title, j.date, lastErr)
						continue
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
		log.Printf("ECFR INGEST: downloads complete (successfully downloaded=%d)", atomic.LoadInt64(&downloaded))
		select {
		case err := <-errCh:
			log.Printf("ECFR INGEST: completed with download errors: %v", err)
		default:
		}
	}

	// 4) Compute metrics for the newest snapshot date per title, rolled up to agencies.
	if err := metrics.ComputeLatest(ctx, st); err != nil {
		return nil, err
	}

	computedAt := time.Now().Format(time.RFC3339)
	if err := st.SetState(ctx, "last_refresh", computedAt); err != nil {
		return nil, err
	}

	return map[string]any{
		"agencies":     len(agencies),
		"titles":       len(titles),
		"downloaded":   int(atomic.LoadInt64(&downloaded)),
		"computed_at":  computedAt,
		"last_refresh": computedAt,
	}, nil
}

// nextDailyRun returns the next time at the given local hour.
func nextDailyRun(now time.Time, hour int) time.Time {
	if hour < 0 || hour > 23 {
		hour = 2
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
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
