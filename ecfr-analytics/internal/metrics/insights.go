package metrics

import (
	"context"
	"encoding/json"
	"sort"

	"ecfr-analytics/internal/ecfr"
	"ecfr-analytics/internal/store"
)

// GrowthHotspot captures a large word-count increase over a window.
type GrowthHotspot struct {
	Agency  string  `json:"agency"`
	Delta   float64 `json:"delta"`
	From    float64 `json:"from"`
	To      float64 `json:"to"`
	Window  int     `json:"window_days"`
}

// GrowthHotspots returns the top N agencies with the largest word_count increases in a window.
func GrowthHotspots(ctx context.Context, st *store.Store, windowDays int, limit int) ([]GrowthHotspot, error) {
	agencies, err := loadAgenciesForInsights(ctx, st)
	if err != nil {
		return nil, err
	}
	results := make([]GrowthHotspot, 0, limit)
	seen := map[string]bool{}
	for _, a := range agencies {
		if seen[a.Slug] {
			continue
		}
		seen[a.Slug] = true
		series, err := st.AgencyMetricSeries(ctx, a.Slug, "word_count", windowDays)
		if err != nil {
			continue
		}
		if len(series) < 2 {
			continue
		}
		// series is newest -> oldest; reverse for oldest -> newest
		first := series[len(series)-1]
		last := series[0]
		firstVal, ok1 := first["value"].(float64)
		lastVal, ok2 := last["value"].(float64)
		if !ok1 || !ok2 {
			continue
		}
		delta := lastVal - firstVal
		if delta <= 0 {
			continue
		}
		results = append(results, GrowthHotspot{
			Agency: a.Name,
			Delta:  delta,
			From:   firstVal,
			To:     lastVal,
			Window: windowDays,
		})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Delta > results[j].Delta })
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// ---- local helpers (duplicated to keep compute.go unchanged) ----

func loadAgenciesForInsights(ctx context.Context, st *store.Store) ([]agencyRecord, error) {
	rows, err := st.DB().QueryContext(ctx, `SELECT slug, name, json FROM agencies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agencyRecord
	for rows.Next() {
		var slug, name, raw string
		if err := rows.Scan(&slug, &name, &raw); err != nil {
			return nil, err
		}
		var a ecfr.Agency
		_ = json.Unmarshal([]byte(raw), &a)
		out = append(out, agencyRecord{Slug: slug, Name: name, Raw: a})
	}
	return flattenAgencyTree(out), nil
}
