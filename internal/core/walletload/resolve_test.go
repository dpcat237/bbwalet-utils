package walletload_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	wl "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

// liveCats is a small stand-in catalogue whose names drift from the export the
// way the real Wallet catalogue drifts (separators, plural, word order, a
// " (Group)" disambiguator).
func liveCats() []wl.Category {
	return []wl.Category{
		{ID: "c-food", Name: "Food & Drinks"},
		{ID: "c-bar", Name: "Bar cafe"},
		{ID: "c-rest", Name: "Restaurants & fast food"},
		{ID: "c-health", Name: "Health care & doctor"},
		{ID: "c-others", Name: "Others"},
		{ID: "c-lottery-inc", Name: "Lottery, gambling (Income)"},
		{ID: "c-lottery-life", Name: "Lottery, gambling (Life & Entertainment)"},
		{ID: "c-charity", Name: "Charity, gifts"},
	}
}

func createOpts() wl.LoadOptions {
	return wl.LoadOptions{TZOffset: "+00:00", FallbackParent: "Others", AccountType: "General", CreateMissing: true}
}

func rowsFor(cat, cur, acct string, custom bool) []wl.ExportRow {
	return []wl.ExportRow{{
		RowKey: "r1", Account: acct, Category: cat, Currency: cur, Amount: "-10.00",
		Date: at("2022-02-02 12:00:00"), CustomCategory: custom,
	}}
}

func TestService_Plan_CategoryTiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		category   string
		custom     bool
		aliases    []wl.CategoryAlias
		wantAction wl.CategoryAction
	}{
		{"exact", "Food & Drinks", false, nil, wl.CategoryResolved},
		{"normalized separator", "Bar, cafe", false, nil, wl.CategoryResolvedNormalised},
		{"normalized plural + amp", "Restaurant, fast-food", false, nil, wl.CategoryResolvedNormalised},
		{"alias match", "Charity", false,
			[]wl.CategoryAlias{{ExportCategory: "Charity", Target: "Charity, gifts"}}, wl.CategoryResolvedAlias},
		{"alias create", "Banya", true,
			[]wl.CategoryAlias{{ExportCategory: "Banya", Target: "create:Others"}}, wl.CategoryFallbackParent},
		{"ambiguous normalized -> none without alias", "Lottery, gambling", false, nil, wl.CategoryNone},
		{"ambiguous resolved by alias", "Lottery, gambling", false,
			[]wl.CategoryAlias{{ExportCategory: "Lottery, gambling", Target: "Lottery, gambling (Income)"}},
			wl.CategoryResolvedAlias},
		{"custom -> fallback-parent create", "Pet hotel", true, nil, wl.CategoryFallbackParent},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cat := &fakeCatalog{accounts: sampleAccounts(), categories: liveCats()}
			svc := newService(
				fakeReader{rows: rowsFor(tc.category, "EUR", "Denys EUR", tc.custom)},
				cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{},
			)
			opts := createOpts()
			opts.CategoryAliases = tc.aliases

			p, err := svc.Plan(context.Background(), opts)
			require.NoError(t, err)
			require.Len(t, p.CategoryMap, 1)
			require.Equal(t, tc.wantAction, p.CategoryMap[0].Action)
		})
	}
}

func TestService_Load_CreatesAccountFromExport(t *testing.T) {
	t.Parallel()

	rows := []wl.ExportRow{
		{RowKey: "a1", Account: "Denys USD", Category: "Food & Drinks", Currency: "USD", Amount: "-4.00", Date: at("2021-01-01 10:00:00")},
		{RowKey: "a2", Account: "Denys USD", Category: "Food & Drinks", Currency: "EUR", Amount: "-1.00", Date: at("2021-02-01 10:00:00")},
	}
	cat := &fakeCatalog{accounts: nil, categories: liveCats()}
	rec := &fakeRecords{}
	svc := newService(fakeReader{rows: rows}, cat, rec, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	sum, err := svc.Load(context.Background(), createOpts())
	require.NoError(t, err)

	require.Len(t, cat.createdAccts, 1)
	require.Equal(t, "Denys USD", cat.createdAccts[0].Name)
	require.Equal(t, "USD", cat.createdAccts[0].CurrencyCode, "tie broken by the name suffix")
	require.Equal(t, "General", cat.createdAccts[0].AccountType)
	require.Equal(t, "0", cat.createdAccts[0].InitialBalance)
	require.Equal(t, 1, sum.AccountsCreated)
	require.Equal(t, 1, sum.Skipped[wl.SkipForeignCurrency], "the EUR row is diverted")
	require.Len(t, rec.createCalls, 1, "the USD row is sent to the new account")
}

func TestService_Load_AliasChainMergesToOneCategory(t *testing.T) {
	t.Parallel()

	rows := []wl.ExportRow{
		{RowKey: "v1", Account: "Denys EUR", Category: "Vet", Currency: "EUR", Amount: "-10.00", Date: at("2021-01-01 10:00:00"), CustomCategory: true},
		{RowKey: "v2", Account: "Denys EUR", Category: "Veterinary", Currency: "EUR", Amount: "-20.00", Date: at("2021-02-01 10:00:00"), CustomCategory: true},
	}
	cat := &fakeCatalog{accounts: sampleAccounts(), categories: append(liveCats(), wl.Category{ID: "c-pets", Name: "Pets & animals"})}
	rec := &fakeRecords{}
	svc := newService(fakeReader{rows: rows}, cat, rec, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	opts := createOpts()
	opts.CategoryAliases = []wl.CategoryAlias{
		{ExportCategory: "Vet", Target: "Veterinary"},
		{ExportCategory: "Veterinary", Target: "create:Pets & animals"},
	}

	sum, err := svc.Load(context.Background(), opts)
	require.NoError(t, err)
	require.Len(t, cat.created, 1, "only one category is created for the merged pair")
	require.Equal(t, "Veterinary", cat.created[0].Name)
	require.Equal(t, "c-pets", cat.created[0].ParentID)

	// both export categories map to the created id
	for _, m := range sum.CategoryMap {
		require.Equal(t, wl.CategoryCreated, m.Action, m.ExportCategory)
		require.Equal(t, "new-Veterinary", m.ResolvedID)
	}
	// both rows sent, both with the same category id
	require.Len(t, rec.createCalls, 1)
	require.Len(t, rec.createCalls[0], 2)
	require.Equal(t, "new-Veterinary", rec.createCalls[0][0].CategoryID)
	require.Equal(t, "new-Veterinary", rec.createCalls[0][1].CategoryID)
}

func TestService_Load_AliasCycleErrors(t *testing.T) {
	t.Parallel()

	cat := &fakeCatalog{accounts: sampleAccounts(), categories: liveCats()}
	svc := newService(fakeReader{rows: rowsFor("A", "EUR", "Denys EUR", true)}, cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	opts := createOpts()
	opts.CategoryAliases = []wl.CategoryAlias{
		{ExportCategory: "A", Target: "B"},
		{ExportCategory: "B", Target: "A"},
	}
	_, err := svc.Load(context.Background(), opts)
	require.ErrorIs(t, err, wl.ErrBadAlias)
}

func TestService_Load_AccountCurrencyOverride(t *testing.T) {
	t.Parallel()

	// Two currencies, equal counts, name has no currency suffix -> needs override.
	rows := []wl.ExportRow{
		{RowKey: "a1", Account: "Joint", Category: "Food & Drinks", Currency: "USD", Amount: "-4.00", Date: at("2021-01-01 10:00:00")},
		{RowKey: "a2", Account: "Joint", Category: "Food & Drinks", Currency: "EUR", Amount: "-1.00", Date: at("2021-02-01 10:00:00")},
	}
	cat := &fakeCatalog{categories: liveCats()}
	svc := newService(fakeReader{rows: rows}, cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	_, err := svc.Load(context.Background(), createOpts())
	require.ErrorIs(t, err, wl.ErrAmbiguousAccountCurrency)

	opts := createOpts()
	opts.AccountCurrency = map[string]string{"Joint": "EUR"}
	opts.AccountInitialBalance = map[string]string{"Joint": "12.34"}
	_, err = svc.Load(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, "EUR", cat.createdAccts[0].CurrencyCode)
	require.Equal(t, "12.34", cat.createdAccts[0].InitialBalance)
}

func TestService_Load_AccountResolveIsIdempotent(t *testing.T) {
	t.Parallel()

	rows := rowsFor("Food & Drinks", "EUR", "Denys EUR", false)
	// Catalogue already has the account -> exact match, no create.
	cat := &fakeCatalog{accounts: sampleAccounts(), categories: liveCats()}
	svc := newService(fakeReader{rows: rows}, cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	sum, err := svc.Load(context.Background(), createOpts())
	require.NoError(t, err)
	require.Empty(t, cat.createdAccts)
	require.Equal(t, 1, sum.AccountsResolved)
}

func TestService_Load_ResidualNoneHardStops(t *testing.T) {
	t.Parallel()

	rows := rowsFor("Totally Unknown", "EUR", "Denys EUR", false)
	cat := &fakeCatalog{accounts: sampleAccounts(), categories: liveCats()}
	svc := newService(fakeReader{rows: rows}, cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	_, err := svc.Load(context.Background(), createOpts())
	require.ErrorIs(t, err, wl.ErrUnresolvedCategories)

	// with a fallback category id the load proceeds and the row is relabelled.
	opts := createOpts()
	opts.FallbackCategoryID = "c-others"
	sum, err := svc.Load(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, sum.CategoriesNoneFallback)
}

func TestService_Load_BadAliasAndMissingParent(t *testing.T) {
	t.Parallel()

	cat := &fakeCatalog{accounts: sampleAccounts(), categories: liveCats()}

	badTarget := createOpts()
	badTarget.CategoryAliases = []wl.CategoryAlias{{ExportCategory: "X", Target: "No Such Category"}}
	svc := newService(fakeReader{rows: rowsFor("X", "EUR", "Denys EUR", false)}, cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})
	_, err := svc.Load(context.Background(), badTarget)
	require.ErrorIs(t, err, wl.ErrBadAlias)

	badParent := createOpts()
	badParent.CategoryAliases = []wl.CategoryAlias{{ExportCategory: "Y", Target: "create:No Such Parent"}}
	svc = newService(fakeReader{rows: rowsFor("Y", "EUR", "Denys EUR", false)}, cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})
	_, err = svc.Load(context.Background(), badParent)
	require.ErrorIs(t, err, wl.ErrMissingParent)
}

func TestService_DryRun_ReportsWouldCreateWithoutCalling(t *testing.T) {
	t.Parallel()

	rows := []wl.ExportRow{
		{RowKey: "a1", Account: "Denys USD", Category: "Banya", Currency: "USD", Amount: "-4.00", Date: at("2021-01-01 10:00:00"), CustomCategory: true},
	}
	cat := &fakeCatalog{accounts: nil, categories: liveCats()}
	rec := &fakeRecords{}
	svc := newService(fakeReader{rows: rows}, cat, rec, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	opts := createOpts()
	opts.DryRun = true
	opts.CategoryAliases = []wl.CategoryAlias{{ExportCategory: "Banya", Target: "create:Others"}}

	sum, err := svc.Load(context.Background(), opts)
	require.NoError(t, err)
	require.Empty(t, cat.createdAccts)
	require.Empty(t, cat.created)
	require.Empty(t, rec.createCalls)
	require.Len(t, sum.AccountsToCreate, 1)
	require.Equal(t, "Denys USD", sum.AccountsToCreate[0].Name)
	require.Equal(t, "USD", sum.AccountsToCreate[0].CurrencyCode)
	require.Equal(t, 0, sum.CategoriesNone)
}

func TestService_Load_CreateMissingOff_UnchangedBehaviour(t *testing.T) {
	t.Parallel()

	// account not in catalogue, --create-missing off -> ErrUnmappedAccount (today's behaviour)
	rows := rowsFor("Food & Drinks", "EUR", "Ghost Account", false)
	cat := &fakeCatalog{accounts: sampleAccounts(), categories: liveCats()}
	svc := newService(fakeReader{rows: rows}, cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	_, err := svc.Load(context.Background(), baseOpts())
	require.ErrorIs(t, err, wl.ErrUnmappedAccount)
	require.Empty(t, cat.createdAccts)
}

func TestService_Load_CreateAccountErrorSurfaces(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("boom")
	cat := &fakeCatalog{categories: liveCats(), createAcctErr: sentinel}
	svc := newService(
		fakeReader{rows: rowsFor("Food & Drinks", "EUR", "Denys EUR", false)},
		cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{},
	)

	_, err := svc.Load(context.Background(), createOpts())
	require.ErrorIs(t, err, sentinel)
}
