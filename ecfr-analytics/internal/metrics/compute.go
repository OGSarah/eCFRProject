package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"

	"ecfr-analytics/internal/ecfr"
	"ecfr-analytics/internal/store"
)

type agencyRecord struct {
	Slug string
	Name string
	Raw  ecfr.Agency
}

type titleKey struct {
	Title int
	Date  string
}

func ComputeLatest(ctx context.Context, st *store.Store) error {
	agencies, err := loadAgencies(ctx, st)
	if err != nil {
		return err
	}
	titles, err := loadTitles(ctx, st)
	if err != nil {
		return err
	}

	// For each title, we only compute for the "current" snapshot date in titles.up_to_date_as_of
	// Then roll-up to agency based on (title, chapter) references.
	// Also compute "churn" vs previous snapshot date if present.

	titleChapterText := map[titleKey]map[string]string{}

	for _, t := range titles {
		if t.Reserved {
			continue
		}
		k := titleKey{Title: t.Number, Date: t.UpToDateAsOf}
		xmlBytes, err := st.ReadSnapshotXML(ctx, t.Number, t.UpToDateAsOf)
		if err != nil {
			// snapshot might not exist if refresh didn't download for some reason
			continue
		}
		chText, err := ecfr.ParseTitleChapters(xmlBytes)
		if err != nil {
			continue
		}
		titleChapterText[k] = chText
	}

	for _, a := range agencies {
		// collect all chapter texts that map to this agency
		var allText string
		chapterChecksums := []string{}

		for _, ref := range a.Raw.CFRReferences {
			// If chapter missing, we cannot attribute precisely; skip (avoid misleading metrics).
			if ref.Chapter == "" {
				continue
			}
			// Find "current" date for that title
			td, ok := findTitleDate(titles, ref.Title)
			if !ok {
				continue
			}
			k := titleKey{Title: ref.Title, Date: td}
			chMap := titleChapterText[k]
			if chMap == nil {
				continue
			}
			txt := chMap[ref.Chapter]
			if txt == "" {
				continue
			}
			allText += txt + " "
			chapterChecksums = append(chapterChecksums, ecfr.ChecksumHex(txt))
		}

		if allText == "" {
			continue
		}

		// ---- Metrics that provide meaningful information ----
		// Word count: “how much regulation text is this agency responsible for?”
		wc := float64(ecfr.WordCount(allText))

		// Agency checksum: stable fingerprint to detect changes
		sum := ecfr.ChecksumHex(allText)

		// Readability: proxy for complexity / stakeholder burden
		fre := ecfr.FleschReadingEase(allText)

		// Custom metric: churn rate
		// = fraction of chapters whose checksum changed vs previous snapshot date (best-effort).
		churn := computeChurnBestEffort(ctx, st, a, titles, titleChapterText)

		// issue_date: we store metrics at the newest issue_date among referenced titles.
		date := newestReferencedDate(a, titles)

		_ = st.PutAgencyMetric(ctx, a.Slug, date, "word_count", &wc, nil)
		_ = st.PutAgencyMetric(ctx, a.Slug, date, "checksum", nil, &sum)
		_ = st.PutAgencyMetric(ctx, a.Slug, date, "readability", &fre, nil)
		_ = st.PutAgencyMetric(ctx, a.Slug, date, "churn", &churn, nil)

		_ = chapterChecksums // keep if you want per-chapter diagnostics later
	}

	return nil
}

func computeChurnBestEffort(
	ctx context.Context,
	st *store.Store,
	a agencyRecord,
	titles []ecfr.Title,
	current map[titleKey]map[string]string,
) float64 {
	// Best effort: look up prior issue_date in snapshots table for each referenced title,
	// compute checksum per referenced chapter and compare.
	// If we can’t find a prior snapshot, churn = 0 for that title.
	type pair struct{ title int; chapter string }
	var refs []pair
	for _, r := range a.Raw.CFRReferences {
		if r.Chapter != "" {
			refs = append(refs, pair{r.Title, r.Chapter})
		}
	}
	if len(refs) == 0 {
		return 0
	}

	changed := 0
	total := 0

	// Group by title
	m := map[int][]string{}
	for _, p := range refs {
		m[p.title] = append(m[p.title], p.chapter)
	}

	for title, chapters := range m {
		curDate, ok := findTitleDate(titles, title)
		if !ok {
			continue
		}
		prevDate, ok := st.PreviousSnapshotDate(ctx, title, curDate)
		if !ok {
			continue
		}
		curXML, err := st.ReadSnapshotXML(ctx, title, curDate)
		if err != nil {
			continue
		}
		prevXML, err := st.ReadSnapshotXML(ctx, title, prevDate)
		if err != nil {
			continue
		}
		curCh, err := ecfr.ParseTitleChapters(curXML)
		if err != nil {
			continue
		}
		prevCh, err := ecfr.ParseTitleChapters(prevXML)
		if err != nil {
			continue
		}

		for _, ch := range uniqueStrings(chapters) {
			ct := curCh[ch]
			pt := prevCh[ch]
			if ct == "" || pt == "" {
				continue
			}
			total++
			if ecfr.ChecksumHex(ct) != ecfr.ChecksumHex(pt) {
				changed++
			}
		}
	}

	if total == 0 {
		return 0
	}
	return float64(changed) / float64(total)
}

// ---- helpers to read back stored agencies & titles ----

func loadAgencies(ctx context.Context, st *store.Store) ([]agencyRecord, error) {
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

func flattenAgencyTree(in []agencyRecord) []agencyRecord {
	// agencies.json contains nested children; we stored top-level json.
	// We re-flatten by walking each Raw agency tree.
	var out []agencyRecord
	var walk func(a ecfr.Agency)
	walk = func(a ecfr.Agency) {
		out = append(out, agencyRecord{Slug: a.Slug, Name: a.Name, Raw: a})
		for _, c := range a.Children {
			walk(c)
		}
	}
	for _, r := range in {
		walk(r.Raw)
	}
	// de-dupe by slug
	seen := map[string]agencyRecord{}
	for _, r := range out {
		seen[r.Slug] = r
	}
	out = out[:0]
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func loadTitles(ctx context.Context, st *store.Store) ([]ecfr.Title, error) {
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

func findTitleDate(titles []ecfr.Title, number int) (string, bool) {
	for _, t := range titles {
		if t.Number == number {
			return t.UpToDateAsOf, true
		}
	}
	return "", false
}

func newestReferencedDate(a agencyRecord, titles []ecfr.Title) string {
	best := ""
	for _, r := range a.Raw.CFRReferences {
		if d, ok := findTitleDate(titles, r.Title); ok {
			if d > best {
				best = d
			}
		}
	}
	return best
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
