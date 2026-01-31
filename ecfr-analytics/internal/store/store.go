package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"ecfr-analytics/internal/ecfr"
)

type Store struct {
	db      *sql.DB
	dataDir string
}

func New(db *sql.DB, dataDir string) *Store {
	return &Store{db: db, dataDir: dataDir}
}

func (s *Store) InitSchema() error {
	ddl := `
CREATE TABLE IF NOT EXISTS agencies (
  slug TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS titles (
  number INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  up_to_date_as_of TEXT NOT NULL,
  reserved INTEGER NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title_number INTEGER NOT NULL,
  issue_date TEXT NOT NULL,
  file_path TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(title_number, issue_date),
  FOREIGN KEY(title_number) REFERENCES titles(number)
);

CREATE TABLE IF NOT EXISTS agency_metrics (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agency_slug TEXT NOT NULL,
  issue_date TEXT NOT NULL,
  metric TEXT NOT NULL,              -- word_count, words_per_chapter, checksum, churn, readability
  value_num REAL,                    -- numeric metrics
  value_text TEXT,                   -- e.g., checksum
  created_at TEXT NOT NULL,
  UNIQUE(agency_slug, issue_date, metric),
  FOREIGN KEY(agency_slug) REFERENCES agencies(slug)
);
`
	_, err := s.db.Exec(ddl)
	return err
}

func (s *Store) UpsertAgencies(ctx context.Context, agencies []ecfr.Agency) error {
	now := time.Now().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO agencies(slug, name, json, updated_at)
VALUES(?,?,?,?)
ON CONFLICT(slug) DO UPDATE SET name=excluded.name, json=excluded.json, updated_at=excluded.updated_at
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, a := range agencies {
		b, _ := json.Marshal(a)
		if _, err := stmt.ExecContext(ctx, a.Slug, a.Name, string(b), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertTitles(ctx context.Context, titles []ecfr.Title) error {
	now := time.Now().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO titles(number, name, up_to_date_as_of, reserved, updated_at)
VALUES(?,?,?,?,?)
ON CONFLICT(number) DO UPDATE SET name=excluded.name, up_to_date_as_of=excluded.up_to_date_as_of, reserved=excluded.reserved, updated_at=excluded.updated_at
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range titles {
		res := 0
		if t.Reserved {
			res = 1
		}
		if _, err := stmt.ExecContext(ctx, t.Number, t.Name, t.UpToDateAsOf, res, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SnapshotExists(ctx context.Context, title int, date string) (bool, error) {
	var x int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM snapshots WHERE title_number=? AND issue_date=? LIMIT 1`, title, date).Scan(&x)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) SaveSnapshot(ctx context.Context, title int, date string, xmlBytes []byte) error {
	return s.SaveSnapshotFromReader(ctx, title, date, bytes.NewReader(xmlBytes))
}

func (s *Store) SaveSnapshotFromReader(ctx context.Context, title int, date string, r io.Reader) error {
	fn := fmt.Sprintf("title-%d_%s.xml.gz", title, date)
	dir := filepath.Join(s.dataDir, "xml")
	path := filepath.Join(dir, fn)

	tmp, err := os.CreateTemp(dir, fn+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	gz := gzip.NewWriter(tmp)
	const maxXMLSize = 300 << 20 // 300MB safety limit on source XML
	n, err := io.Copy(gz, io.LimitReader(r, maxXMLSize+1))
	if err == nil && n > maxXMLSize {
		err = fmt.Errorf("snapshot too large")
	}
	if err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o644); err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO snapshots(title_number, issue_date, file_path, created_at)
VALUES(?,?,?,?)
`, title, date, path, time.Now().Format(time.RFC3339))
	return err
}

func (s *Store) ReadSnapshotXML(ctx context.Context, title int, date string) ([]byte, error) {
	var path string
	if err := s.db.QueryRowContext(ctx, `SELECT file_path FROM snapshots WHERE title_number=? AND issue_date=?`, title, date).Scan(&path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return ioReadAllLimit(r, 200<<20) // 200MB safety
}

func ioReadAllLimit(r interface{ Read([]byte) (int, error) }, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	var total int64
	p := make([]byte, 64*1024)
	for {
		n, err := r.Read(p)
		if n > 0 {
			total += int64(n)
			if total > limit {
				return nil, fmt.Errorf("snapshot too large")
			}
			buf.Write(p[:n])
		}
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			// handle io.EOF without importing io
			if err == os.ErrClosed {
				break
			}
			if err.Error() == "EOF" {
				break
			}
			// best effort
			if err.Error() == "unexpected EOF" {
				break
			}
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func (s *Store) ListAgencies(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT slug, name FROM agencies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var slug, name string
		if err := rows.Scan(&slug, &name); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"slug": slug, "name": name})
	}
	return out, nil
}

func (s *Store) PutAgencyMetric(ctx context.Context, slug, date, metric string, num *float64, text *string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agency_metrics(agency_slug, issue_date, metric, value_num, value_text, created_at)
VALUES(?,?,?,?,?,?)
ON CONFLICT(agency_slug, issue_date, metric) DO UPDATE SET value_num=excluded.value_num, value_text=excluded.value_text
`, slug, date, metric, num, text, time.Now().Format(time.RFC3339))
	return err
}

func (s *Store) LatestAgencyMetric(ctx context.Context, metric string) ([]map[string]any, error) {
	// latest by issue_date per agency for a given metric
	q := `
SELECT m.agency_slug, a.name, m.issue_date, m.value_num, m.value_text
FROM agency_metrics m
JOIN agencies a ON a.slug = m.agency_slug
WHERE m.metric = ?
  AND m.issue_date = (SELECT MAX(issue_date) FROM agency_metrics m2 WHERE m2.agency_slug=m.agency_slug AND m2.metric=m.metric)
ORDER BY a.name
`
	rows, err := s.db.QueryContext(ctx, q, metric)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var slug, name, date string
		var num sql.NullFloat64
		var txt sql.NullString
		if err := rows.Scan(&slug, &name, &date, &num, &txt); err != nil {
			return nil, err
		}
		o := map[string]any{"slug": slug, "name": name, "date": date}
		if num.Valid {
			o["value"] = num.Float64
		} else if txt.Valid {
			o["value"] = txt.String
		} else {
			o["value"] = nil
		}
		out = append(out, o)
	}
	return out, nil
}

func (s *Store) AgencyMetricSeries(ctx context.Context, slug, metric string, days int) ([]map[string]any, error) {
	q := `
SELECT issue_date, value_num, value_text
FROM agency_metrics
WHERE agency_slug=? AND metric=?
ORDER BY issue_date DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, slug, metric, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var date string
		var num sql.NullFloat64
		var txt sql.NullString
		if err := rows.Scan(&date, &num, &txt); err != nil {
			return nil, err
		}
		o := map[string]any{"date": date}
		if num.Valid {
			o["value"] = num.Float64
		} else if txt.Valid {
			o["value"] = txt.String
		} else {
			o["value"] = nil
		}
		out = append(out, o)
	}
	return out, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) PreviousSnapshotDate(ctx context.Context, title int, currentDate string) (string, bool) {
	q := `SELECT issue_date FROM snapshots WHERE title_number=? AND issue_date < ? ORDER BY issue_date DESC LIMIT 1`
	var d string
	if err := s.db.QueryRowContext(ctx, q, title, currentDate).Scan(&d); err != nil {
		return "", false
	}
	return d, true
}
