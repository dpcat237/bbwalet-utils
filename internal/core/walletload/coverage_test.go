package walletload_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/walletcsv"
	wl "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

// repoPath resolves a path relative to the repository root from this test file's
// location (internal/core/walletload/).
func repoPath(t *testing.T, rel string) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(self), "..", "..", "..", rel)
}

func loadCatalogueFixture(t *testing.T) []wl.Category {
	t.Helper()
	data, err := os.ReadFile("testdata/live_categories_2026-08-29.json")
	require.NoError(t, err)

	var body struct {
		Categories []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			SystemID       string `json:"systemId"`
			CustomCategory bool   `json:"customCategory"`
			Group          struct {
				Name string `json:"name"`
			} `json:"group"`
		} `json:"categories"`
	}
	require.NoError(t, json.Unmarshal(data, &body))

	out := make([]wl.Category, len(body.Categories))
	for i, c := range body.Categories {
		out[i] = wl.Category{
			ID: c.ID, Name: c.Name, GroupName: c.Group.Name,
			SystemID: c.SystemID, Custom: c.CustomCategory,
		}
	}
	return out
}

// TestPlanCategories_FullExportCoverage is the regression guard for R9b: every
// distinct category in the committed working export must resolve (exact,
// normalized, alias, or create) against the checked-in catalogue snapshot and
// categories-alias.csv — nothing may land as `none`. A live load with
// --create-missing hard-stops on any `none`, so this keeps the real migration
// unblocked and catches drift in the export or the alias file.
func TestPlanCategories_FullExportCoverage(t *testing.T) {
	t.Parallel()

	rows, err := walletcsv.New(repoPath(t, "data/report_2026-08-28_144610.csv")).Read(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, rows)

	aliases, err := walletcsv.ReadCategoryAlias(repoPath(t, "categories-alias.csv"))
	require.NoError(t, err)
	require.NotEmpty(t, aliases, "categories-alias.csv should be checked in and non-empty")

	cat := &fakeCatalog{categories: loadCatalogueFixture(t)}
	svc := newService(fakeReader{rows: rows}, cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	p, err := svc.Plan(context.Background(), wl.LoadOptions{
		TZOffset:        "+00:00",
		FallbackParent:  "Others",
		AccountType:     "General",
		CreateMissing:   true,
		CategoryAliases: aliases,
	})
	require.NoError(t, err)

	var unresolved []string
	for _, m := range p.CategoryMap {
		if m.Action == wl.CategoryNone {
			unresolved = append(unresolved, m.ExportCategory)
		}
	}
	require.Emptyf(t, unresolved, "these export categories resolve to none — add them to categories-alias.csv:\n%v", unresolved)
	require.Empty(t, p.Problems, "alias file has bad targets / missing parents: %v", p.Problems)
}
