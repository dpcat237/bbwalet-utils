// Package loadreport reads the report files wallet-migrate writes to its out/
// directory (task 5) and exposes them through the walletverify.LoadReport port:
// _review.csv (rows the loader deliberately did not send) and _category_map.csv
// (what the loader did with each export category).
package loadreport

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	core "github.com/dpcat237/bbwalet-utils/internal/core/walletverify"
)

const (
	reviewFileName      = "_review.csv"
	categoryMapFileName = "_category_map.csv"
)

// Store reads task 5's out/ report files from one directory.
type Store struct {
	dir string
}

// New returns a Store rooted at the given out/ directory.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Skipped returns the rows wallet-migrate diverted to _review.csv. A missing
// file yields no rows: a load may legitimately have skipped nothing.
func (s *Store) Skipped(_ context.Context) ([]core.SkippedRow, error) {
	records, err := readRows(filepath.Join(s.dir, reviewFileName), true)
	if err != nil {
		return nil, err
	}
	out := make([]core.SkippedRow, 0, len(records))
	for _, rec := range records {
		out = append(out, core.SkippedRow{
			RowKey:   col(rec, 0),
			Reason:   col(rec, 1),
			Account:  col(rec, 2),
			Currency: col(rec, 4),
			Amount:   col(rec, 5),
		})
	}
	return out, nil
}

// Categories returns the _category_map.csv rows. The file is required — a real
// load always writes it — so a missing file is ErrLoadReportUnreadable.
func (s *Store) Categories(_ context.Context) ([]core.CategoryAction, error) {
	records, err := readRows(filepath.Join(s.dir, categoryMapFileName), false)
	if err != nil {
		return nil, err
	}
	out := make([]core.CategoryAction, 0, len(records))
	for _, rec := range records {
		out = append(out, core.CategoryAction{
			ExportCategory: col(rec, 0),
			Action:         col(rec, 1),
			ResolvedID:     col(rec, 2),
		})
	}
	return out, nil
}

// readRows reads a CSV file and drops the header row. When optional is true a
// missing file returns no rows; otherwise a missing file is an error.
func readRows(path string, optional bool) ([][]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path derives from the operator-supplied --load-report-dir
	switch {
	case errors.Is(err, os.ErrNotExist) && optional:
		return nil, nil
	case errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("%w: %s not found", core.ErrLoadReportUnreadable, filepath.Base(path))
	case err != nil:
		return nil, fmt.Errorf("%w: reading %s: %w", core.ErrLoadReportUnreadable, filepath.Base(path), err)
	}

	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%w: parsing %s: %w", core.ErrLoadReportUnreadable, filepath.Base(path), err)
	}
	if len(rows) <= 1 {
		return nil, nil
	}
	return rows[1:], nil
}

func col(rec []string, i int) string {
	if i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}
