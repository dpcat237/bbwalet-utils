package walletcsv_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/walletcsv"
	verify "github.com/dpcat237/bbwalet-utils/internal/core/walletverify"
)

func zeroArchiveKeys(rows []verify.ArchiveRow) []verify.ArchiveRow {
	out := make([]verify.ArchiveRow, len(rows))
	for i, r := range rows {
		r.RowKey = ""
		out[i] = r
	}
	return out
}

func TestArchiveReader_Read_ParsesByHeaderName(t *testing.T) {
	t.Parallel()

	rows, err := walletcsv.NewArchiveReader(filepath.Join("testdata", "sample.csv")).Read(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 3)

	require.Equal(t, "Denys EUR", rows[0].Account)
	require.Equal(t, "Food", rows[0].Category)
	require.Equal(t, "EUR", rows[0].Currency)
	require.Equal(t, "-12.00", rows[0].Amount)
	require.Equal(t, time.Date(2024, 2, 3, 10, 27, 52, 0, time.UTC), rows[0].Date)
	require.NotEmpty(t, rows[0].RowKey)
}

func TestArchiveReader_Read_ColumnOrderTolerant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, err := walletcsv.NewArchiveReader(filepath.Join("testdata", "sample.csv")).Read(ctx)
	require.NoError(t, err)
	b, err := walletcsv.NewArchiveReader(filepath.Join("testdata", "alt-column-order.csv")).Read(ctx)
	require.NoError(t, err)

	require.Equal(t, zeroArchiveKeys(a), zeroArchiveKeys(b))
}

func TestArchiveReader_Read_StableRowKeyAcrossReads(t *testing.T) {
	t.Parallel()

	r := walletcsv.NewArchiveReader(filepath.Join("testdata", "sample.csv"))
	first, err := r.Read(context.Background())
	require.NoError(t, err)
	second, err := r.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, first[0].RowKey, second[0].RowKey)
	require.NotEqual(t, first[0].RowKey, first[1].RowKey)
}

func TestArchiveReader_Read_MissingColumn_ReturnsArchiveUnreadable(t *testing.T) {
	t.Parallel()

	_, err := walletcsv.NewArchiveReader(filepath.Join("testdata", "no-header.csv")).Read(context.Background())
	require.ErrorIs(t, err, verify.ErrArchiveUnreadable)
	require.Contains(t, err.Error(), "category")
}

func TestArchiveReader_Read_MissingFile_ReturnsArchiveUnreadable(t *testing.T) {
	t.Parallel()

	_, err := walletcsv.NewArchiveReader(filepath.Join("testdata", "does-not-exist.csv")).Read(context.Background())
	require.ErrorIs(t, err, verify.ErrArchiveUnreadable)
}

func TestArchiveReader_Read_BadDate_ReturnsArchiveUnreadable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.csv")
	writeFile(t, path, "account;category;currency;amount;date\nCash;Food;EUR;-1.00;03/02/2024\n")

	_, err := walletcsv.NewArchiveReader(path).Read(context.Background())
	require.ErrorIs(t, err, verify.ErrArchiveUnreadable)
	require.Contains(t, err.Error(), "date")
}
