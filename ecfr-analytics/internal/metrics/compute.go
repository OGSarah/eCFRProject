package metrics

import (
	"context"
	"encoding/json"
	"fmt"
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
	titles, err := loadTitles(ctx, st)
	if err != nil {
		return err
	}
	titleDates := currentTitleDates(titles)
	return computeWithTitleDates(ctx, st, titles, titleDates)
}

func ComputeForTitleDates(ctx context.Context, st *store.Store, titleDates map[int]string) error {
	titles, err := loadTitles(ctx, st)
	if err != nil {
		return err
	}
	return computeWithTitleDates(ctx, st, titles, titleDates)
}

func computeWithTitleDates(ctx context.Context, st *store.Store, titles []ecfr.Title, titleDates map[int]string) error {
	agencies, err := loadAgencies(ctx, st)
	if err != nil {
		return err
	}

	titleChapterText := map[titleKey]map[string]string{}

	for _, t := range titles {
		if t.Reserved {
			continue
		}
		date := titleDates[t.Number]
		if date == "" {
			continue
		}
		k := titleKey{Title: t.Number, Date: date}
		xmlBytes, err := st.ReadSnapshotXML(ctx, t.Number, date)
		if err != nil {
			continue
		}
		chText, err := ecfr.ParseTitleChapters(xmlBytes)
		if err != nil {
			continue
		}
		titleChapterText[k] = chText
	}

	for _, a := range agencies {
		var allText string
		chapterChecksums := []string{}
		chapterSet := map[string]bool{}

		for _, ref := range a.Raw.CFRReferences {
			if ref.Chapter == "" {
				continue
			}
			td := titleDates[ref.Title]
			if td == "" {
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
			chapterSet[refKey(ref.Title, ref.Chapter)] = true
		}

		if allText == "" {
			continue
		}

		wc := float64(ecfr.WordCount(allText))

		denom := len(chapterSet)
		if denom == 0 {
			denom = 1
		}
		wordsPerChapter := wc / float64(denom)

		sum := ecfr.ChecksumHex(allText)

		fre := ecfr.FleschReadingEase(allText)

		churn := computeChurnBestEffort(ctx, st, a, titleDates)

		date := newestReferencedDateFromMap(a, titleDates)

		_ = st.PutAgencyMetric(ctx, a.Slug, date, "word_count", &wc, nil)
		_ = st.PutAgencyMetric(ctx, a.Slug, date, "words_per_chapter", &wordsPerChapter, nil)
		_ = st.PutAgencyMetric(ctx, a.Slug, date, "checksum", nil, &sum)
		_ = st.PutAgencyMetric(ctx, a.Slug, date, "readability", &fre, nil)
		_ = st.PutAgencyMetric(ctx, a.Slug, date, "churn", &churn, nil)

		_ = chapterChecksums
	}

	return nil
}

func computeChurnBestEffort(
	ctx context.Context,
	st *store.Store,
	a agencyRecord,
	titleDates map[int]string,
) float64 {
	type pair struct {
		title   int
		chapter string
	}
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

	m := map[int][]string{}
	for _, p := range refs {
		m[p.title] = append(m[p.title], p.chapter)
	}

	for title, chapters := range m {
		curDate := titleDates[title]
		if curDate == "" {
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

func currentTitleDates(titles []ecfr.Title) map[int]string {
	out := make(map[int]string, len(titles))
	for _, t := range titles {
		if t.Reserved {
			continue
		}
		out[t.Number] = t.UpToDateAsOf
	}
	return out
}

func newestReferencedDateFromMap(a agencyRecord, titleDates map[int]string) string {
	best := ""
	for _, r := range a.Raw.CFRReferences {
		d := titleDates[r.Title]
		if d == "" {
			continue
		}
		if d > best {
			best = d
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

func refKey(title int, chapter string) string {
	return fmt.Sprintf("%d:%s", title, chapter)
}
