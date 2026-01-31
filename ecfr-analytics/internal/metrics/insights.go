package metrics

import (
	"context"
	"encoding/json"
	"sort"

	"ecfr-analytics/internal/ecfr"
	"ecfr-analytics/internal/store"
)

// OutlierChapter highlights a dense chapter for an agency.
type OutlierChapter struct {
	Agency string `json:"agency"`
	Title  int    `json:"title"`
	Chapter string `json:"chapter"`
	Words  int    `json:"words"`
}

// GrowthHotspot captures a large word-count increase over a window.
type GrowthHotspot struct {
	Agency  string  `json:"agency"`
	Delta   float64 `json:"delta"`
	From    float64 `json:"from"`
	To      float64 `json:"to"`
	Window  int     `json:"window_days"`
}

// OutlierChaptersByAgency returns the top N densest chapters for a single agency.
func OutlierChaptersByAgency(ctx context.Context, st *store.Store, slug string, limit int) ([]OutlierChapter, error) {
	agencies, err := loadAgenciesForInsights(ctx, st)
	if err != nil {
		return nil, err
	}
	titles, err := loadTitlesForInsights(ctx, st)
	if err != nil {
		return nil, err
	}

	var agency *agencyRecord
	for i := range agencies {
		if agencies[i].Slug == slug {
			agency = &agencies[i]
			break
		}
	}
	if agency == nil {
		return []OutlierChapter{}, nil
	}

	// Build chapter text maps only for titles referenced by this agency.
	titleText := map[int]map[string]string{}
	for _, ref := range agency.Raw.CFRReferences {
		if ref.Chapter == "" {
			continue
		}
		date, ok := findTitleDateForInsights(titles, ref.Title)
		if !ok {
			continue
		}
		if _, ok := titleText[ref.Title]; ok {
			continue
		}
		xmlBytes, err := st.ReadSnapshotXML(ctx, ref.Title, date)
		if err != nil {
			continue
		}
		chText, err := ecfr.ParseTitleChapters(xmlBytes)
		if err != nil {
			continue
		}
		titleText[ref.Title] = chText
	}

	var out []OutlierChapter
	for _, ref := range agency.Raw.CFRReferences {
		if ref.Chapter == "" {
			continue
		}
		chMap := titleText[ref.Title]
		if chMap == nil {
			continue
		}
		txt := chMap[ref.Chapter]
		if txt == "" {
			continue
		}
		out = append(out, OutlierChapter{
			Agency:  agency.Name,
			Title:   ref.Title,
			Chapter: ref.Chapter,
			Words:   ecfr.WordCount(txt),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Words > out[j].Words })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
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

func loadTitlesForInsights(ctx context.Context, st *store.Store) ([]ecfr.Title, error) {
	rows, err := st.DB().QueryContext(ctx, `SELECT number, name, up_to_date_as_of, reserved FROM titles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ecfr.Title
	for rows.Next() {
		var t ecfr.Title
		var reserved int
		if err := rows.Scan(&t.Number, &t.Name, &t.UpToDateAsOf, &reserved); err != nil {
			return nil, err
		}
		t.Reserved = reserved == 1
		out = append(out, t)
	}
	return out, nil
}

func findTitleDateForInsights(titles []ecfr.Title, number int) (string, bool) {
	for _, t := range titles {
		if t.Number == number {
			return t.UpToDateAsOf, true
		}
	}
	return "", false
}
