package walletcsv_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/walletcsv"
	walletload "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

func TestReadCategoryAlias_ParsesSample(t *testing.T) {
	t.Parallel()

	got, err := walletcsv.ReadCategoryAlias("testdata/categories-alias-sample.csv")
	require.NoError(t, err)
	require.Equal(t, []walletload.CategoryAlias{
		{ExportCategory: "Charity", Target: "Charity, gifts"},
		{ExportCategory: "Banya", Target: "create:Health & beauty"},
		{ExportCategory: "Lottery, gambling", Target: "Lottery, gambling (Income)"},
	}, got)
}

func TestReadCategoryAlias_MissingFileIsEmpty(t *testing.T) {
	t.Parallel()

	got, err := walletcsv.ReadCategoryAlias(filepath.Join(t.TempDir(), "nope.csv"))
	require.NoError(t, err)
	require.Nil(t, got)

	got, err = walletcsv.ReadCategoryAlias("")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestReadCategoryAlias_MalformedRowErrors(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "bad.csv")
	require.NoError(t, os.WriteFile(p, []byte("export_category,target\nOnlyOneColumn\n"), 0o600))

	_, err := walletcsv.ReadCategoryAlias(p)
	require.ErrorIs(t, err, walletload.ErrBadAlias)
}

func TestReadCategoryAlias_HeaderlessFile(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "nohdr.csv")
	require.NoError(t, os.WriteFile(p, []byte("Charity,\"Charity, gifts\"\n"), 0o600))

	got, err := walletcsv.ReadCategoryAlias(p)
	require.NoError(t, err)
	require.Equal(t, []walletload.CategoryAlias{{ExportCategory: "Charity", Target: "Charity, gifts"}}, got)
}
