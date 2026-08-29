package walletverify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/core/walletverify"
)

// --- fakes ---------------------------------------------------------------

type fakeArchive struct {
	rows []walletverify.ArchiveRow
	err  error
}

func (f fakeArchive) Read(context.Context) ([]walletverify.ArchiveRow, error) {
	return f.rows, f.err
}

type fakeLoad struct {
	skipped []walletverify.SkippedRow
	cats    []walletverify.CategoryAction
	err     error
}

func (f fakeLoad) Skipped(context.Context) ([]walletverify.SkippedRow, error) {
	return f.skipped, f.err
}

func (f fakeLoad) Categories(context.Context) ([]walletverify.CategoryAction, error) {
	return f.cats, f.err
}

type fakeWallet struct {
	accounts []walletverify.Account
	byAcct   map[string][]walletverify.Record
	err      error
}

func (f fakeWallet) Accounts(context.Context) ([]walletverify.Account, error) {
	return f.accounts, f.err
}

func (f fakeWallet) Records(_ context.Context, q walletverify.RecordQuery) ([]walletverify.Record, error) {
	return f.byAcct[q.AccountID], f.err
}

// --- helpers -----------------------------------------------------------

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 10, 0, 0, 0, time.UTC)
}

func run(t *testing.T, a fakeArchive, l fakeLoad, w fakeWallet, opts walletverify.Options) (walletverify.Report, error) {
	t.Helper()
	svc := walletverify.New(walletverify.Deps{Archive: a, Load: l, Wallet: w})
	return svc.Verify(context.Background(), opts)
}

func statusFor(rep walletverify.Report, account string) walletverify.AccountStatus {
	for _, r := range rep.Accounts {
		if r.Account == account {
			return r.Status
		}
	}
	return ""
}

// --- R2: count + sum -------------------------------------------------

func TestService_Verify_HappyAllPass(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Currency: "EUR", Amount: "-12.00", Category: "Internet", Date: day(2020, 1, 3)},
		{RowKey: "e2", Account: "Denys EUR", Currency: "EUR", Amount: "-8.50", Category: "Food", Date: day(2021, 6, 9)},
		{RowKey: "u1", Account: "Denys USD", Currency: "USD", Amount: "100.00", Category: "Salary", Date: day(2022, 2, 1)},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{
			{ID: "a-eur", Name: "Denys EUR", CurrencyCode: "EUR"},
			{ID: "a-usd", Name: "Denys USD", CurrencyCode: "USD"},
		},
		byAcct: map[string][]walletverify.Record{
			"a-eur": {
				{ID: "r1", AccountID: "a-eur", Amount: "-12.00", CurrencyCode: "EUR", CategoryName: "Internet", Date: day(2020, 1, 3)},
				{ID: "r2", AccountID: "a-eur", Amount: "-8.50", CurrencyCode: "EUR", CategoryName: "Food", Date: day(2021, 6, 9)},
			},
			"a-usd": {
				{ID: "r3", AccountID: "a-usd", Amount: "100.00", CurrencyCode: "USD", CategoryName: "Salary", Date: day(2022, 2, 1)},
			},
		},
	}

	rep, err := run(t, a, fakeLoad{}, w, walletverify.Options{})
	require.NoError(t, err)
	require.True(t, rep.OK)
	require.Equal(t, walletverify.AccountPass, statusFor(rep, "Denys EUR"))
	require.Equal(t, walletverify.AccountPass, statusFor(rep, "Denys USD"))
}

func TestService_Verify_CountMismatch(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Amount: "-12.00", Date: day(2020, 1, 3)},
		{RowKey: "e2", Account: "Denys EUR", Amount: "-8.50", Date: day(2021, 6, 9)},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{{ID: "a-eur", Name: "Denys EUR", CurrencyCode: "EUR"}},
		byAcct: map[string][]walletverify.Record{
			"a-eur": {{ID: "r1", AccountID: "a-eur", Amount: "-12.00", Date: day(2020, 1, 3)}},
		},
	}

	rep, err := run(t, a, fakeLoad{}, w, walletverify.Options{})
	require.NoError(t, err)
	require.False(t, rep.OK)
	require.Equal(t, walletverify.AccountCountMismatch, statusFor(rep, "Denys EUR"))
}

func TestService_Verify_SumMismatch_CentDifference(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Amount: "-12.00", Date: day(2020, 1, 3)},
		{RowKey: "e2", Account: "Denys EUR", Amount: "-8.50", Date: day(2021, 6, 9)},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{{ID: "a-eur", Name: "Denys EUR", CurrencyCode: "EUR"}},
		byAcct: map[string][]walletverify.Record{
			"a-eur": {
				{ID: "r1", AccountID: "a-eur", Amount: "-12.00", Date: day(2020, 1, 3)},
				{ID: "r2", AccountID: "a-eur", Amount: "-8.51", Date: day(2021, 6, 9)},
			},
		},
	}

	rep, err := run(t, a, fakeLoad{}, w, walletverify.Options{})
	require.NoError(t, err)
	require.Equal(t, walletverify.AccountSumMismatch, statusFor(rep, "Denys EUR"))
	require.Equal(t, "-20.50", accountSum(rep, "Denys EUR", true))
	require.Equal(t, "-20.51", accountSum(rep, "Denys EUR", false))
}

func accountSum(rep walletverify.Report, account string, expected bool) string {
	for _, r := range rep.Accounts {
		if r.Account != account {
			continue
		}
		if expected {
			return r.ExpectedSum
		}
		return r.ActualSum
	}
	return ""
}

func TestService_Verify_AccountOnlyInArchive(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "g1", Account: "Ghost", Amount: "1.00", Date: day(2020, 1, 3)},
	}}
	w := fakeWallet{accounts: []walletverify.Account{{ID: "a-eur", Name: "Denys EUR", CurrencyCode: "EUR"}}}

	rep, err := run(t, a, fakeLoad{}, w, walletverify.Options{})
	require.NoError(t, err)
	require.False(t, rep.OK)
	require.Equal(t, walletverify.AccountMissingInWallet, statusFor(rep, "Ghost"))
}

func TestService_Verify_AccountOnlyInWallet(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Amount: "-12.00", Date: day(2020, 1, 3)},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{
			{ID: "a-eur", Name: "Denys EUR", CurrencyCode: "EUR"},
			{ID: "a-extra", Name: "Extra", CurrencyCode: "EUR"},
		},
		byAcct: map[string][]walletverify.Record{
			"a-eur":   {{ID: "r1", AccountID: "a-eur", Amount: "-12.00", Date: day(2020, 1, 3)}},
			"a-extra": {{ID: "rx", AccountID: "a-extra", Amount: "5.00", Date: day(2020, 3, 3)}},
		},
	}

	rep, err := run(t, a, fakeLoad{}, w, walletverify.Options{})
	require.NoError(t, err)
	require.False(t, rep.OK)
	require.Equal(t, walletverify.AccountMissingInArchive, statusFor(rep, "Extra"))
}

func TestService_Verify_RenamedAccountViaAccountMap(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Old Name", Amount: "-12.00", Date: day(2020, 1, 3)},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{{ID: "a-new", Name: "New Name", CurrencyCode: "EUR"}},
		byAcct: map[string][]walletverify.Record{
			"a-new": {{ID: "r1", AccountID: "a-new", Amount: "-12.00", Date: day(2020, 1, 3)}},
		},
	}

	rep, err := run(t, a, fakeLoad{}, w, walletverify.Options{
		AccountMap: map[string]string{"Old Name": "a-new"},
	})
	require.NoError(t, err)
	require.Equal(t, walletverify.AccountPass, statusFor(rep, "Old Name"))
}

func TestService_Verify_SkippedRowsExcludedFromExpected(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys USD", Amount: "10.00", Date: day(2020, 1, 3)},
		{RowKey: "e2", Account: "Denys USD", Amount: "99.00", Date: day(2020, 2, 3)}, // foreign currency, skipped
	}}
	l := fakeLoad{skipped: []walletverify.SkippedRow{
		{RowKey: "e2", Account: "Denys USD", Reason: "foreign-currency"},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{{ID: "a-usd", Name: "Denys USD", CurrencyCode: "USD"}},
		byAcct: map[string][]walletverify.Record{
			"a-usd": {{ID: "r1", AccountID: "a-usd", Amount: "10.00", Date: day(2020, 1, 3)}},
		},
	}

	rep, err := run(t, a, l, w, walletverify.Options{})
	require.NoError(t, err)
	require.Equal(t, walletverify.AccountPass, statusFor(rep, "Denys USD"))
	require.Equal(t, 1, rep.Accounts[0].ExpectedCount)
}

func TestService_Verify_MalformedArchiveAmount(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Amount: "not-a-number", Date: day(2020, 1, 3)},
	}}
	w := fakeWallet{accounts: []walletverify.Account{{ID: "a-eur", Name: "Denys EUR"}}}

	_, err := run(t, a, fakeLoad{}, w, walletverify.Options{})
	require.ErrorIs(t, err, walletverify.ErrArchiveUnreadable)
}

func TestService_Verify_ArchiveReadErrorPropagates(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("disk gone")
	_, err := run(t, fakeArchive{err: sentinel}, fakeLoad{}, fakeWallet{}, walletverify.Options{})
	require.ErrorIs(t, err, sentinel)
}

func TestService_Verify_MalformedLoadedAmount(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Amount: "-12.00", Date: day(2020, 1, 3)},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{{ID: "a-eur", Name: "Denys EUR", CurrencyCode: "EUR"}},
		byAcct: map[string][]walletverify.Record{
			"a-eur": {{ID: "r1", AccountID: "a-eur", Amount: "??", Date: day(2020, 1, 3)}},
		},
	}

	_, err := run(t, a, fakeLoad{}, w, walletverify.Options{})
	require.ErrorIs(t, err, walletverify.ErrWalletRead)
}

func TestService_Verify_AccountMapSyntheticIDFallback(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Old Name", Amount: "-12.00", Date: day(2020, 1, 3)},
	}}
	// account map points at an id that is NOT in the returned account list.
	w := fakeWallet{accounts: []walletverify.Account{{ID: "a-real", Name: "Real", CurrencyCode: "EUR"}}}

	rep, err := run(t, a, fakeLoad{}, w, walletverify.Options{
		AccountMap: map[string]string{"Old Name": "a-ghost"},
	})
	require.NoError(t, err)
	// resolves to the synthetic id, which has no records -> count-mismatch, not missing-in-wallet.
	require.Equal(t, walletverify.AccountCountMismatch, statusFor(rep, "Old Name"))
}

// --- R3: rate sample ------------------------------------------------

func TestService_Verify_RateSampleVerdicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ratios []string
		want   walletverify.RateVerdict
	}{
		{"varying ratios -> date-accurate", []string{"0.90", "0.85", "0.80", "0.95"}, walletverify.RateDateAccurate},
		{"constant ratio -> current-rate", []string{"0.92", "0.92", "0.92"}, walletverify.RateCurrent},
		{"blank ratios -> no-signal", []string{"", "", ""}, walletverify.RateNoSignal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recs := make([]walletverify.Record, 0, len(tc.ratios))
			for i, ratio := range tc.ratios {
				recs = append(recs, walletverify.Record{
					ID: "r", AccountID: "a-usd", Amount: "10.00", CurrencyCode: "USD",
					ConvertedValue: "9.00", ConvertedRatio: ratio,
					Date: day(2018+i, time.January, 1),
				})
			}
			w := fakeWallet{
				accounts: []walletverify.Account{{ID: "a-usd", Name: "Denys USD", CurrencyCode: "USD"}},
				byAcct:   map[string][]walletverify.Record{"a-usd": recs},
			}
			a := fakeArchive{rows: []walletverify.ArchiveRow{
				{RowKey: "u1", Account: "Denys USD", Amount: "10.00", Date: day(2018, 1, 1)},
			}}
			l := fakeLoad{skipped: []walletverify.SkippedRow{{RowKey: "u1"}}} // ignore R2 here

			rep, err := run(t, a, l, w, walletverify.Options{RateSampleSize: 10})
			require.NoError(t, err)
			require.Len(t, rep.Rates, 1)
			require.Equal(t, "USD", rep.Rates[0].Currency)
			require.Equal(t, tc.want, rep.Rates[0].Verdict)
		})
	}
}

func TestService_Verify_NoForeignRecords_NoRateFindings(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Amount: "-12.00", Date: day(2020, 1, 3)},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{{ID: "a-eur", Name: "Denys EUR", CurrencyCode: "EUR"}},
		byAcct: map[string][]walletverify.Record{
			"a-eur": {{ID: "r1", AccountID: "a-eur", Amount: "-12.00", CurrencyCode: "EUR", Date: day(2020, 1, 3)}},
		},
	}

	rep, err := run(t, a, fakeLoad{}, w, walletverify.Options{})
	require.NoError(t, err)
	require.Empty(t, rep.Rates)
	require.Empty(t, rep.RateSample)
}

// --- R4: category spot-check ---------------------------------------

func TestService_Verify_CategorySpotCheck(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Amount: "-12.00", Category: "Internet", Date: day(2020, 1, 3)},
		{RowKey: "e2", Account: "Denys EUR", Amount: "-8.50", Category: "Groceries", Date: day(2021, 6, 9)},
	}}
	l := fakeLoad{cats: []walletverify.CategoryAction{
		{ExportCategory: "Internet", Action: "resolved", ResolvedID: "c-int"},
		{ExportCategory: "Pharmacy", Action: "fallback-parent"},
		{ExportCategory: "Weird", Action: "none"},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{{ID: "a-eur", Name: "Denys EUR", CurrencyCode: "EUR"}},
		byAcct: map[string][]walletverify.Record{
			"a-eur": {
				{ID: "r1", AccountID: "a-eur", Amount: "-12.00", CategoryName: "Internet", Date: day(2020, 1, 3)},
				{ID: "r2", AccountID: "a-eur", Amount: "-8.50", CategoryName: "Bar, cafe", Date: day(2021, 6, 9)},
			},
		},
	}

	rep, err := run(t, a, l, w, walletverify.Options{CategorySampleSize: 10})
	require.NoError(t, err)
	require.Equal(t, 2, rep.Categories.Sampled)
	require.Equal(t, 1, rep.Categories.Matched)
	require.Equal(t, 1, rep.Categories.Changed)
	require.Len(t, rep.Categories.Changes, 1)
	require.Equal(t, "Groceries", rep.Categories.Changes[0].Archive)
	require.Equal(t, "Bar, cafe", rep.Categories.Changes[0].Loaded)
	require.Equal(t, []string{"Pharmacy", "Weird"}, rep.Categories.ManualPass)
}

func TestService_Verify_CategoryUnmatchedWhenNoLoadedRecord(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Amount: "-12.00", Category: "Internet", Date: day(2020, 1, 3)},
	}}
	w := fakeWallet{
		accounts: []walletverify.Account{{ID: "a-eur", Name: "Denys EUR", CurrencyCode: "EUR"}},
		byAcct:   map[string][]walletverify.Record{"a-eur": nil},
	}

	rep, err := run(t, a, fakeLoad{}, w, walletverify.Options{})
	require.NoError(t, err)
	require.Equal(t, 1, rep.Categories.Unmatched)
	require.Equal(t, 0, rep.Categories.Sampled)
}

func TestService_Verify_LoadReportErrorPropagates(t *testing.T) {
	t.Parallel()

	a := fakeArchive{rows: []walletverify.ArchiveRow{
		{RowKey: "e1", Account: "Denys EUR", Amount: "-12.00", Date: day(2020, 1, 3)},
	}}
	sentinel := errors.New("no _category_map.csv")
	_, err := run(t, a, fakeLoad{err: sentinel}, fakeWallet{}, walletverify.Options{})
	require.ErrorIs(t, err, sentinel)
}
