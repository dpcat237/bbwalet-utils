package walletcsv_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/walletcsv"
	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func zeroKeys(rows []core.ExportRow) []core.ExportRow {
	out := make([]core.ExportRow, len(rows))
	for i, r := range rows {
		r.RowKey = ""
		out[i] = r
	}
	return out
}

func TestReader_Read_ParsesByHeaderName(t *testing.T) {
	t.Parallel()

	rows, err := walletcsv.New(filepath.Join("testdata", "sample.csv")).Read(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 3)

	require.Equal(t, "Denys EUR", rows[0].Account)
	require.Equal(t, "Food", rows[0].Category)
	require.Equal(t, "EUR", rows[0].Currency)
	require.Equal(t, "-12.00", rows[0].Amount)
	require.Equal(t, "Shop", rows[0].Payee)
	require.Equal(t, "lunch", rows[0].Note)
	require.Equal(t, time.Date(2024, 2, 3, 10, 27, 52, 0, time.UTC), rows[0].Date)
	require.False(t, rows[0].CustomCategory)
	require.True(t, rows[1].CustomCategory)
	require.NotEmpty(t, rows[0].RowKey)
}

func TestReader_Read_ColumnOrderTolerant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, err := walletcsv.New(filepath.Join("testdata", "sample.csv")).Read(ctx)
	require.NoError(t, err)
	b, err := walletcsv.New(filepath.Join("testdata", "alt-column-order.csv")).Read(ctx)
	require.NoError(t, err)

	// RowKey is derived from raw bytes and legitimately differs between two
	// files; every semantic field must match.
	require.Equal(t, zeroKeys(a), zeroKeys(b))
}

func TestReader_Read_StableRowKeyAcrossReads(t *testing.T) {
	t.Parallel()

	r := walletcsv.New(filepath.Join("testdata", "sample.csv"))
	first, err := r.Read(context.Background())
	require.NoError(t, err)
	second, err := r.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, first[0].RowKey, second[0].RowKey)
	require.NotEqual(t, first[0].RowKey, first[1].RowKey)
}

func TestReader_Read_MissingColumn_ReturnsUnknownHeader(t *testing.T) {
	t.Parallel()

	_, err := walletcsv.New(filepath.Join("testdata", "no-header.csv")).Read(context.Background())
	require.ErrorIs(t, err, core.ErrUnknownHeader)
	require.Contains(t, err.Error(), "category")
}

func TestReader_Read_MissingFile_ReturnsUnreadable(t *testing.T) {
	t.Parallel()

	_, err := walletcsv.New(filepath.Join("testdata", "does-not-exist.csv")).Read(context.Background())
	require.ErrorIs(t, err, core.ErrExportUnreadable)
}

func TestReader_Read_BadDate_ReturnsUnreadable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.csv")
	writeFile(t, path, "account;category;currency;amount;date\nCash;Food;EUR;-1.00;03/02/2024\n")

	_, err := walletcsv.New(path).Read(context.Background())
	require.ErrorIs(t, err, core.ErrExportUnreadable)
	require.Contains(t, err.Error(), "date")
}
