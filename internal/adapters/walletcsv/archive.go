package walletcsv

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	verify "github.com/dpcat237/bbwalet-utils/internal/core/walletverify"
)

// ArchiveReader reads the archived Wallet export for the verification tool
// (wallet-verify). It reuses the same semicolon parsing, header-name addressing
// and stable RowKey as Reader, but emits walletverify.ArchiveRow and reports
// failures as walletverify.ErrArchiveUnreadable so the walletverify core owns
// its own error identity.
type ArchiveReader struct {
	path string
}

// NewArchiveReader returns an ArchiveReader for the archived export at path.
func NewArchiveReader(path string) *ArchiveReader {
	return &ArchiveReader{path: path}
}

// Read parses the whole archived export into walletverify.ArchiveRow values.
//
//nolint:dupl // deliberately parallel to Reader.Read: same CSV shape, different target type and error identity.
func (r *ArchiveReader) Read(_ context.Context) ([]verify.ArchiveRow, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", verify.ErrArchiveUnreadable, err)
	}

	cr := csv.NewReader(bytes.NewReader(data))
	cr.Comma = ';'
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: reading header: %w", verify.ErrArchiveUnreadable, err)
	}
	idx, err := archiveColumnIndex(header)
	if err != nil {
		return nil, err
	}
	return readArchiveRows(cr, idx)
}

func archiveColumnIndex(header []string) (map[string]int, error) {
	idx := make(map[string]int, len(header))
	for i, name := range header {
		idx[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for _, want := range []string{"account", "category", "currency", "amount", "date"} {
		if _, ok := idx[want]; !ok {
			return nil, fmt.Errorf("%w: missing column %q", verify.ErrArchiveUnreadable, want)
		}
	}
	return idx, nil
}

func readArchiveRows(cr *csv.Reader, idx map[string]int) ([]verify.ArchiveRow, error) {
	var rows []verify.ArchiveRow
	line := 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", verify.ErrArchiveUnreadable, line, err)
		}
		row, err := parseArchiveRow(rec, idx, line)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
}

func parseArchiveRow(rec []string, idx map[string]int, line int) (verify.ArchiveRow, error) {
	date, err := time.Parse(dateLayout, strings.TrimSpace(field(rec, idx, "date")))
	if err != nil {
		return verify.ArchiveRow{}, fmt.Errorf("%w: line %d: date: %w", verify.ErrArchiveUnreadable, line, err)
	}
	return verify.ArchiveRow{
		RowKey:   rowKey(rec),
		Account:  strings.TrimSpace(field(rec, idx, "account")),
		Category: strings.TrimSpace(field(rec, idx, "category")),
		Currency: strings.TrimSpace(field(rec, idx, "currency")),
		Amount:   strings.TrimSpace(field(rec, idx, "amount")),
		Date:     date,
	}, nil
}
