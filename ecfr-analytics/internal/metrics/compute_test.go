package metrics

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"ecfr-analytics/internal/ecfr"
	"ecfr-analytics/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.sqlite")
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000&_foreign_keys=1")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := store.New(db, dir)
	if err := os.MkdirAll(filepath.Join(dir, "xml"), 0o755); err != nil {
		t.Fatalf("mkdir xml: %v", err)
	}
	if err := st.InitSchema(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return st
}

func TestComputeLatest(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	agency := ecfr.Agency{
		Name: "Agency One",
		Slug: "agency-one",
		CFRReferences: []ecfr.CFRRef{
			{Title: 1, Chapter: "I"},
		},
	}
	if err := st.UpsertAgencies(ctx, []ecfr.Agency{agency}); err != nil {
		t.Fatalf("upsert agencies: %v", err)
	}
	title := ecfr.Title{Number: 1, Name: "Title 1", UpToDateAsOf: "2025-01-02", Reserved: false}
	if err := st.UpsertTitles(ctx, []ecfr.Title{title}); err != nil {
		t.Fatalf("upsert titles: %v", err)
	}

	xmlPrev := []byte(`<ROOT><DIV1 TYPE="CHAPTER" N="I"><P>Alpha beta.</P></DIV1></ROOT>`)
	xmlCur := []byte(`<ROOT><DIV1 TYPE="CHAPTER" N="I"><P>Alpha gamma.</P></DIV1></ROOT>`)
	if err := st.SaveSnapshotFromReader(ctx, 1, "2025-01-01", bytes.NewReader(xmlPrev)); err != nil {
		t.Fatalf("save prev snapshot: %v", err)
	}
	if err := st.SaveSnapshotFromReader(ctx, 1, "2025-01-02", bytes.NewReader(xmlCur)); err != nil {
		t.Fatalf("save cur snapshot: %v", err)
	}

	if err := ComputeLatest(ctx, st); err != nil {
		t.Fatalf("compute latest: %v", err)
	}

	rows, err := st.LatestAgencyMetric(ctx, "churn")
	if err != nil {
		t.Fatalf("latest churn: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 churn row, got %d", len(rows))
	}
	if rows[0]["value"].(float64) != 1.0 {
		t.Fatalf("expected churn=1.0, got %v", rows[0]["value"])
	}
}

func TestHelpers(t *testing.T) {
	titles := []ecfr.Title{
		{Number: 1, UpToDateAsOf: "2025-01-01", Reserved: false},
		{Number: 2, UpToDateAsOf: "2025-01-02", Reserved: true},
	}
	dates := currentTitleDates(titles)
	if dates[1] != "2025-01-01" {
		t.Fatalf("unexpected date map: %#v", dates)
	}

	u := uniqueStrings([]string{"a", "b", "a"})
	if len(u) != 2 {
		t.Fatalf("unexpected unique count: %d", len(u))
	}

	if refKey(1, "I") != "1:I" {
		t.Fatalf("unexpected refKey")
	}

	a := agencyRecord{
		Raw: ecfr.Agency{CFRReferences: []ecfr.CFRRef{{Title: 1}, {Title: 2}}},
	}
	if newestReferencedDateFromMap(a, map[int]string{1: "2025-01-01", 2: "2025-01-03"}) != "2025-01-03" {
		t.Fatalf("unexpected newest date")
	}
}
