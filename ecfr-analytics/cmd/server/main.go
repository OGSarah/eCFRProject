package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"ecfr-analytics/internal/ecfr"
	"ecfr-analytics/internal/metrics"
	"ecfr-analytics/internal/store"
)

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

	cli := ecfr.NewClient(baseURL, 25*time.Second)

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

func refreshCurrent(ctx context.Context, cli *ecfr.Client, st *store.Store) (map[string]any, error) {
	// 1) Pull agencies metadata, store it
	agencies, err := cli.GetAgencies(ctx)
	if err != nil {
		return nil, err
	}
	if err := st.UpsertAgencies(ctx, agencies); err != nil {
		return nil, err
	}

	// 2) Pull titles list, choose each title's up_to_date_as_of (current “snapshot date”)
	titles, err := cli.GetTitles(ctx)
	if err != nil {
		return nil, err
	}
	if err := st.UpsertTitles(ctx, titles); err != nil {
		return nil, err
	}

	// 3) For each title, download full XML and store as gzip file (if not already)
	downloaded := 0
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
		xmlBytes, err := cli.GetFullTitleXML(ctx, date, t.Number)
		if err != nil {
			return nil, err
		}
		if err := st.SaveSnapshot(ctx, t.Number, date, xmlBytes); err != nil {
			return nil, err
		}
		downloaded++
	}

	// 4) Compute metrics for the newest snapshot date per title, rolled up to agencies
	if err := metrics.ComputeLatest(ctx, st); err != nil {
		return nil, err
	}

	return map[string]any{
		"agencies":    len(agencies),
		"titles":      len(titles),
		"downloaded":  downloaded,
		"computed_at": time.Now().Format(time.RFC3339),
	}, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

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

func indexOf(s, sub string) int {
	// tiny helper to avoid importing strings everywhere
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
