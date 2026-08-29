// Package walletcsv reads a BudgetBakers Wallet CSV export (semicolon-delimited,
// 19-column format). Reader turns it into core walletload.ExportRow values for
// the loader; ArchiveReader (archive.go) turns it into walletverify.ArchiveRow
// values for the verifier. It addresses every field by header name so the two
// known column orderings parse identically, and it performs no validation
// beyond parsing — classification is the core's job.
package walletcsv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

const dateLayout = "2006-01-02 15:04:05"

// Reader reads one Wallet export file.
type Reader struct {
	path string
}

// New returns a Reader for the export at path.
func New(path string) *Reader {
	return &Reader{path: path}
}

// Read parses the whole export. It returns core.ErrExportUnreadable on an
// open/parse failure and core.ErrUnknownHeader when a required column is absent.
//
//nolint:dupl // deliberately parallel to ArchiveReader.Read: same CSV shape, different target type and error identity.
func (r *Reader) Read(_ context.Context) ([]core.ExportRow, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", core.ErrExportUnreadable, err)
	}

	cr := csv.NewReader(bytes.NewReader(data))
	cr.Comma = ';'
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: reading header: %w", core.ErrExportUnreadable, err)
	}
	idx, err := columnIndex(header)
	if err != nil {
		return nil, err
	}
	return readRows(cr, idx)
}

func columnIndex(header []string) (map[string]int, error) {
	idx := make(map[string]int, len(header))
	for i, name := range header {
		idx[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for _, want := range []string{"account", "category", "currency", "amount", "date"} {
		if _, ok := idx[want]; !ok {
			return nil, fmt.Errorf("%w: %q", core.ErrUnknownHeader, want)
		}
	}
	return idx, nil
}

func readRows(cr *csv.Reader, idx map[string]int) ([]core.ExportRow, error) {
	var rows []core.ExportRow
	line := 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", core.ErrExportUnreadable, line, err)
		}
		row, err := parseRow(rec, idx, line)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
}

func parseRow(rec []string, idx map[string]int, line int) (core.ExportRow, error) {
	date, err := time.Parse(dateLayout, strings.TrimSpace(field(rec, idx, "date")))
	if err != nil {
		return core.ExportRow{}, fmt.Errorf("%w: line %d: date: %w", core.ErrExportUnreadable, line, err)
	}
	return core.ExportRow{
		RowKey:         rowKey(rec),
		Account:        strings.TrimSpace(field(rec, idx, "account")),
		Category:       strings.TrimSpace(field(rec, idx, "category")),
		Currency:       strings.TrimSpace(field(rec, idx, "currency")),
		Amount:         strings.TrimSpace(field(rec, idx, "amount")),
		Payee:          strings.TrimSpace(field(rec, idx, "payee")),
		Note:           strings.TrimSpace(field(rec, idx, "note")),
		Date:           date,
		CustomCategory: strings.EqualFold(strings.TrimSpace(field(rec, idx, "custom_category")), "true"),
	}, nil
}

// field returns the record value for a header name, or "" when the column is
// absent or the row is short.
func field(rec []string, idx map[string]int, name string) string {
	i, ok := idx[name]
	if !ok || i >= len(rec) {
		return ""
	}
	return rec[i]
}

// rowKey is a stable per-row identifier: the SHA-256 of the record's fields
// re-joined with the export delimiter. Deterministic across reads of one file.
func rowKey(rec []string) string {
	sum := sha256.Sum256([]byte(strings.Join(rec, ";")))
	return hex.EncodeToString(sum[:])
}
