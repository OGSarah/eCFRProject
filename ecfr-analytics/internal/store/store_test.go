package store

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"ecfr-analytics/internal/ecfr"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.sqlite")
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000&_foreign_keys=1")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := New(db, dir)
	if err := os.MkdirAll(filepath.Join(dir, "xml"), 0o755); err != nil {
		t.Fatalf("mkdir xml: %v", err)
	}
	if err := st.InitSchema(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return st
}

func TestStateRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.SetState(ctx, "last_refresh", "2025-01-01T00:00:00Z"); err != nil {
		t.Fatalf("set state: %v", err)
	}
	val, err := st.GetState(ctx, "last_refresh")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if val != "2025-01-01T00:00:00Z" {
		t.Fatalf("unexpected state value: %q", val)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.UpsertTitles(ctx, []ecfr.Title{{Number: 1, Name: "Title 1", UpToDateAsOf: "2025-01-02", Reserved: false}}); err != nil {
		t.Fatalf("upsert titles: %v", err)
	}
	xml := []byte(`<ROOT><DIV1 TYPE="CHAPTER" N="I"><P>Hello world.</P></DIV1></ROOT>`)
	if err := st.SaveSnapshotFromReader(ctx, 1, "2025-01-02", bytes.NewReader(xml)); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	ok, err := st.SnapshotExists(ctx, 1, "2025-01-02")
	if err != nil {
		t.Fatalf("snapshot exists: %v", err)
	}
	if !ok {
		t.Fatalf("expected snapshot to exist")
	}
	out, err := st.ReadSnapshotXML(ctx, 1, "2025-01-02")
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !bytes.Contains(out, []byte("Hello world")) {
		t.Fatalf("unexpected snapshot content: %q", string(out))
	}
}

func TestLatestAgencyMetricDelta(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ag := []ecfr.Agency{{Slug: "dot", Name: "Department of Testing"}}
	if err := st.UpsertAgencies(ctx, ag); err != nil {
		t.Fatalf("upsert agencies: %v", err)
	}

	v1 := 10.0
	v2 := 12.5
	if err := st.PutAgencyMetric(ctx, "dot", "2025-01-01", "word_count", &v1, nil); err != nil {
		t.Fatalf("put metric v1: %v", err)
	}
	if err := st.PutAgencyMetric(ctx, "dot", "2025-01-02", "word_count", &v2, nil); err != nil {
		t.Fatalf("put metric v2: %v", err)
	}

	rows, err := st.LatestAgencyMetric(ctx, "word_count")
	if err != nil {
		t.Fatalf("latest metric: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["value"].(float64) != v2 {
		t.Fatalf("unexpected value: %v", rows[0]["value"])
	}
	if rows[0]["delta"].(float64) != v2-v1 {
		t.Fatalf("unexpected delta: %v", rows[0]["delta"])
	}
}

func TestLatestAgencyMetricTextChange(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ag := []ecfr.Agency{{Slug: "doj", Name: "Department of Checksums"}}
	if err := st.UpsertAgencies(ctx, ag); err != nil {
		t.Fatalf("upsert agencies: %v", err)
	}
	a := "abc"
	b := "def"
	if err := st.PutAgencyMetric(ctx, "doj", "2025-01-01", "checksum", nil, &a); err != nil {
		t.Fatalf("put metric a: %v", err)
	}
	if err := st.PutAgencyMetric(ctx, "doj", "2025-01-02", "checksum", nil, &b); err != nil {
		t.Fatalf("put metric b: %v", err)
	}

	rows, err := st.LatestAgencyMetric(ctx, "checksum")
	if err != nil {
		t.Fatalf("latest metric: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["changed"].(bool) != true {
		t.Fatalf("expected changed=true")
	}
}

func TestAgencyMetricSeries(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ag := []ecfr.Agency{{Slug: "nsa", Name: "Numbers"}}
	if err := st.UpsertAgencies(ctx, ag); err != nil {
		t.Fatalf("upsert agencies: %v", err)
	}
	v1 := 1.0
	v2 := 2.0
	if err := st.PutAgencyMetric(ctx, "nsa", "2025-01-01", "word_count", &v1, nil); err != nil {
		t.Fatalf("put metric v1: %v", err)
	}
	if err := st.PutAgencyMetric(ctx, "nsa", "2025-01-02", "word_count", &v2, nil); err != nil {
		t.Fatalf("put metric v2: %v", err)
	}

	rows, err := st.AgencyMetricSeries(ctx, "nsa", "word_count", 2)
	if err != nil {
		t.Fatalf("metric series: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestPreviousSnapshotDate(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.UpsertTitles(ctx, []ecfr.Title{{Number: 2, Name: "Title 2", UpToDateAsOf: "2025-01-03", Reserved: false}}); err != nil {
		t.Fatalf("upsert titles: %v", err)
	}
	xml := []byte(`<ROOT><DIV1 TYPE="CHAPTER" N="I"><P>Hi.</P></DIV1></ROOT>`)
	if err := st.SaveSnapshotFromReader(ctx, 2, "2025-01-01", bytes.NewReader(xml)); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	if err := st.SaveSnapshotFromReader(ctx, 2, "2025-01-03", bytes.NewReader(xml)); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	prev, ok := st.PreviousSnapshotDate(ctx, 2, "2025-01-03")
	if !ok {
		t.Fatalf("expected previous snapshot")
	}
	if prev != "2025-01-01" {
		t.Fatalf("unexpected previous date: %q", prev)
	}
}
