package loadreport_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/loadreport"
	core "github.com/dpcat237/bbwalet-utils/internal/core/walletverify"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func TestStore_Skipped_ParsesReviewRows(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, "_review.csv",
		"row_key,reason,account,category,currency,amount,date\n"+
			"k1,foreign-currency,Denys USD,Food,EUR,99.00,2020-01-01 00:00:00\n"+
			"k2,zero-amount,Denys EUR,Food,EUR,0,2020-02-02 00:00:00\n")

	rows, err := loadreport.New(dir).Skipped(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "k1", rows[0].RowKey)
	require.Equal(t, "foreign-currency", rows[0].Reason)
	require.Equal(t, "Denys USD", rows[0].Account)
	require.Equal(t, "EUR", rows[0].Currency)
	require.Equal(t, "99.00", rows[0].Amount)
}

func TestStore_Skipped_MissingFileIsEmpty(t *testing.T) {
	t.Parallel()

	rows, err := loadreport.New(t.TempDir()).Skipped(context.Background())
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestStore_Categories_ParsesMapRows(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, "_category_map.csv",
		"export_category,action,resolved_id\n"+
			"Internet,resolved,c-int\n"+
			"Pharmacy,fallback-parent,\n"+
			"Weird,none,\n")

	rows, err := loadreport.New(dir).Categories(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "Internet", rows[0].ExportCategory)
	require.Equal(t, "resolved", rows[0].Action)
	require.Equal(t, "c-int", rows[0].ResolvedID)
	require.Equal(t, "fallback-parent", rows[1].Action)
	require.Equal(t, "none", rows[2].Action)
}

func TestStore_Categories_MissingFileIsError(t *testing.T) {
	t.Parallel()

	_, err := loadreport.New(t.TempDir()).Categories(context.Background())
	require.ErrorIs(t, err, core.ErrLoadReportUnreadable)
}

func TestStore_Categories_MalformedIsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, "_category_map.csv", "export_category,action\n\"unterminated,quote\n")

	_, err := loadreport.New(dir).Categories(context.Background())
	require.ErrorIs(t, err, core.ErrLoadReportUnreadable)
}

func TestStore_Skipped_HeaderOnlyIsEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, "_review.csv", "row_key,reason,account,category,currency,amount,date\n")

	rows, err := loadreport.New(dir).Skipped(context.Background())
	require.NoError(t, err)
	require.Empty(t, rows)
}
