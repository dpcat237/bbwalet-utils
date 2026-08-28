package walletload_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	wl "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

// --- fakes ----------------------------------------------------------------

type fakeReader struct {
	rows []wl.ExportRow
	err  error
}

func (f fakeReader) Read(context.Context) ([]wl.ExportRow, error) { return f.rows, f.err }

type fakeCatalog struct {
	accounts   []wl.Account
	categories []wl.Category
	created    []wl.PlannedCategory
	err        error
}

func (f *fakeCatalog) Accounts(context.Context) ([]wl.Account, error) { return f.accounts, f.err }

func (f *fakeCatalog) Categories(context.Context) ([]wl.Category, error) {
	return f.categories, f.err
}

func (f *fakeCatalog) CreateCustomCategory(_ context.Context, name, parentID string) (wl.Category, error) {
	f.created = append(f.created, wl.PlannedCategory{Name: name, ParentID: parentID})
	return wl.Category{ID: "new-" + name, Name: name, Custom: true}, nil
}

type fakeRecords struct {
	createCalls [][]wl.RecordInput
	responder   func(call int, in []wl.RecordInput) ([]wl.RecordResult, error)
	deleteResp  func(ids []string) ([]wl.RecordResult, error)
	findResp    []wl.RemoteRecord
	calls       int
}

func (f *fakeRecords) CreateRecords(_ context.Context, in []wl.RecordInput) ([]wl.RecordResult, error) {
	call := f.calls
	f.calls++
	f.createCalls = append(f.createCalls, in)
	if f.responder != nil {
		return f.responder(call, in)
	}
	return okResults(in), nil
}

func (f *fakeRecords) DeleteRecords(_ context.Context, ids []string) ([]wl.RecordResult, error) {
	if f.deleteResp != nil {
		return f.deleteResp(ids)
	}
	out := make([]wl.RecordResult, len(ids))
	for i, id := range ids {
		out[i] = wl.RecordResult{RecordID: id, OK: true}
	}
	return out, nil
}

func (f *fakeRecords) FindRecords(_ context.Context, _ wl.RecordQuery) ([]wl.RemoteRecord, error) {
	return f.findResp, nil
}

func okResults(in []wl.RecordInput) []wl.RecordResult {
	out := make([]wl.RecordResult, len(in))
	for i, r := range in {
		out[i] = wl.RecordResult{RowKey: r.RowKey, RecordID: "rec-" + r.RowKey, OK: true}
	}
	return out
}

type fakeState struct {
	loaded     map[string]bool
	commits    map[string]string
	createdIDs []string
	commitErr  error
}

func newFakeState() *fakeState {
	return &fakeState{loaded: map[string]bool{}, commits: map[string]string{}}
}

func (f *fakeState) Loaded(k string) bool { return f.loaded[k] }

func (f *fakeState) Commit(k, id string) error {
	if f.commitErr != nil {
		return f.commitErr
	}
	f.commits[k] = id
	f.loaded[k] = true
	return nil
}

func (f *fakeState) CreatedIDs() ([]string, error) { return f.createdIDs, nil }

type fakeJournal struct {
	marked   []wl.Batch
	cleared  []string
	inflight []wl.Batch
}

func (f *fakeJournal) MarkInFlight(b wl.Batch) error { f.marked = append(f.marked, b); return nil }
func (f *fakeJournal) ClearInFlight(id string) error { f.cleared = append(f.cleared, id); return nil }
func (f *fakeJournal) InFlight() ([]wl.Batch, error) { return f.inflight, nil }

type fakeWaiter struct{ waits []time.Duration }

func (f *fakeWaiter) Wait(_ context.Context, d time.Duration) error {
	f.waits = append(f.waits, d)
	return nil
}

// --- fixtures -----------------------------------------------------------

func sampleAccounts() []wl.Account {
	return []wl.Account{
		{ID: "acc-eur", Name: "Denys EUR", CurrencyCode: "EUR"},
		{ID: "acc-uah", Name: "Denys UAH", CurrencyCode: "UAH"},
	}
}

func sampleCategories() []wl.Category {
	return []wl.Category{
		{ID: "cat-food", Name: "Food", GroupName: "Food & Drinks"},
		{ID: "cat-others", Name: "Others"},
	}
}

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		panic(err)
	}
	return t
}

// sendable + skippable rows; r6 (unmapped account) is added only where needed.
func sampleRows() []wl.ExportRow {
	return []wl.ExportRow{
		{RowKey: "k1", Account: "Denys EUR", Category: "Food", Currency: "EUR", Amount: "-12.00", Payee: "Shop", Note: "lunch", Date: at("2024-02-03 10:00:00")},
		{RowKey: "k2", Account: "Denys EUR", Category: "Pharmacy", Currency: "EUR", Amount: "-5.00", Date: at("2023-01-01 09:00:00"), CustomCategory: true},
		{RowKey: "k3", Account: "Denys UAH", Category: "Food", Currency: "UAH", Amount: "-100.00", Date: at("2022-06-06 12:00:00")},
		{RowKey: "k4", Account: "Denys EUR", Category: "Food", Currency: "USD", Amount: "-3.00", Date: at("2021-05-05 08:00:00")},
		{RowKey: "k5", Account: "Denys EUR", Category: "Food", Currency: "EUR", Amount: "0", Date: at("2020-04-04 07:00:00")},
	}
}

func newService(r wl.ExportReader, c wl.Catalog, rec wl.Records, st wl.ResumeState, j wl.InFlightJournal, w wl.Waiter) *wl.Service {
	return wl.New(wl.Deps{Reader: r, Catalog: c, Records: rec, State: st, Journal: j, Waiter: w})
}

func baseOpts() wl.LoadOptions {
	return wl.LoadOptions{TZOffset: "+00:00", FallbackParent: "Others"}
}

// --- tests -------------------------------------------------------------

func TestService_Plan_ReportsSkipsAndCategories(t *testing.T) {
	t.Parallel()

	cat := &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}
	svc := newService(fakeReader{rows: sampleRows()}, cat, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	plan, err := svc.Plan(context.Background(), baseOpts())
	require.NoError(t, err)

	require.Equal(t, 5, plan.Rows)
	require.Empty(t, plan.UnmappedAccounts)
	require.Equal(t, 2, plan.PerAccountToSend["Denys EUR"]) // k1 + k2 (k2's category is pending creation but the row still sends)
	require.Equal(t, 1, plan.PerAccountToSend["Denys UAH"])
	require.Len(t, plan.CategoriesToCreate, 1)
	require.Equal(t, "Pharmacy", plan.CategoriesToCreate[0].Name)
	require.Equal(t, "cat-others", plan.CategoriesToCreate[0].ParentID)

	reasons := map[wl.SkipReason]int{}
	for _, s := range plan.Skipped {
		reasons[s.Reason]++
	}
	require.Equal(t, 1, reasons[wl.SkipForeignCurrency])
	require.Equal(t, 1, reasons[wl.SkipZeroAmount])
}

func TestService_Load_UnmappedAccount_Errors(t *testing.T) {
	t.Parallel()

	rows := append(sampleRows(), wl.ExportRow{
		RowKey: "k6", Account: "Ghost Account", Category: "Food", Currency: "EUR", Amount: "-1.00", Date: at("2024-01-01 00:00:00"),
	})
	cat := &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}
	rec := &fakeRecords{}
	svc := newService(fakeReader{rows: rows}, cat, rec, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	_, err := svc.Load(context.Background(), baseOpts())
	require.ErrorIs(t, err, wl.ErrUnmappedAccount)
	require.Contains(t, err.Error(), "Ghost Account")
	require.Empty(t, rec.createCalls, "no writes when an account is unmapped")
}

func TestService_Load_AccountMapResolvesRename(t *testing.T) {
	t.Parallel()

	rows := []wl.ExportRow{
		{RowKey: "k1", Account: "Old Name", Category: "Food", Currency: "EUR", Amount: "-9.00", Date: at("2024-01-01 00:00:00")},
	}
	cat := &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}
	rec := &fakeRecords{}
	svc := newService(fakeReader{rows: rows}, cat, rec, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	opts := baseOpts()
	opts.AccountMap = map[string]string{"Old Name": "acc-eur"}

	sum, err := svc.Load(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, sum.PerAccountCreated["acc-eur"])
}

func TestService_Load_CreatesCategoriesAndRecords(t *testing.T) {
	t.Parallel()

	cat := &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}
	rec := &fakeRecords{}
	st := newFakeState()
	j := &fakeJournal{}
	svc := newService(fakeReader{rows: sampleRows()}, cat, rec, st, j, &fakeWaiter{})

	sum, err := svc.Load(context.Background(), baseOpts())
	require.NoError(t, err)

	require.Len(t, cat.created, 1)
	require.Equal(t, "Pharmacy", cat.created[0].Name)

	require.Equal(t, "rec-k1", st.commits["k1"])
	require.Equal(t, "rec-k2", st.commits["k2"])
	require.Equal(t, "rec-k3", st.commits["k3"])
	require.Equal(t, 2, sum.PerAccountCreated["acc-eur"])
	require.Equal(t, 1, sum.PerAccountCreated["acc-uah"])
	require.Equal(t, 1, sum.CategoriesResolved) // Food
	require.Equal(t, 1, sum.CategoriesCreated)  // Pharmacy (backfilled)
	require.NotEmpty(t, j.marked)
	require.Len(t, j.cleared, len(j.marked))

	// k2's record carries the created category id
	var k2 wl.RecordInput
	for _, call := range rec.createCalls {
		for _, in := range call {
			if in.RowKey == "k2" {
				k2 = in
			}
		}
	}
	require.Equal(t, "new-Pharmacy", k2.CategoryID)
	require.Equal(t, "2023-01-01T09:00:00+00:00", k2.RecordDate)
	require.Equal(t, "EUR", k2.CurrencyCode)
}

func TestService_Load_ForeignAndZeroRows_Reviewed(t *testing.T) {
	t.Parallel()

	rec := &fakeRecords{}
	svc := newService(fakeReader{rows: sampleRows()}, &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}, rec, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	sum, err := svc.Load(context.Background(), baseOpts())
	require.NoError(t, err)

	require.Equal(t, 1, sum.Skipped[wl.SkipForeignCurrency])
	require.Equal(t, 1, sum.Skipped[wl.SkipZeroAmount])
	for _, call := range rec.createCalls {
		for _, in := range call {
			require.NotEqual(t, "k4", in.RowKey)
			require.NotEqual(t, "k5", in.RowKey)
		}
	}
}

func TestService_Load_RateLimit_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	rec := &fakeRecords{responder: func(call int, in []wl.RecordInput) ([]wl.RecordResult, error) {
		if call == 0 {
			return nil, &wl.ErrRateLimited{RetryAfter: 2 * time.Second}
		}
		return okResults(in), nil
	}}
	w := &fakeWaiter{}
	st := newFakeState()
	rows := []wl.ExportRow{{RowKey: "k1", Account: "Denys EUR", Category: "Food", Currency: "EUR", Amount: "-1.00", Date: at("2024-01-01 00:00:00")}}
	svc := newService(fakeReader{rows: rows}, &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}, rec, st, &fakeJournal{}, w)

	sum, err := svc.Load(context.Background(), baseOpts())
	require.NoError(t, err)
	require.Equal(t, []time.Duration{2 * time.Second}, w.waits)
	require.Equal(t, 1, sum.Retries)
	require.Equal(t, 1, sum.RateLimitHits)
	require.Equal(t, "rec-k1", st.commits["k1"])
}

func TestService_Load_RateLimit_RetriesExhausted(t *testing.T) {
	t.Parallel()

	rec := &fakeRecords{responder: func(int, []wl.RecordInput) ([]wl.RecordResult, error) {
		return nil, &wl.ErrRateLimited{RetryAfter: time.Second}
	}}
	w := &fakeWaiter{}
	rows := []wl.ExportRow{{RowKey: "k1", Account: "Denys EUR", Category: "Food", Currency: "EUR", Amount: "-1.00", Date: at("2024-01-01 00:00:00")}}
	svc := newService(fakeReader{rows: rows}, &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}, rec, newFakeState(), &fakeJournal{}, w)

	_, err := svc.Load(context.Background(), baseOpts())
	require.ErrorIs(t, err, wl.ErrRetriesExhausted)
	require.Len(t, w.waits, 5)
}

func TestService_Load_ResumeSkipsAlreadyLoaded(t *testing.T) {
	t.Parallel()

	st := newFakeState()
	st.loaded["k1"] = true
	rec := &fakeRecords{}
	svc := newService(fakeReader{rows: sampleRows()}, &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}, rec, st, &fakeJournal{}, &fakeWaiter{})

	_, err := svc.Load(context.Background(), baseOpts())
	require.NoError(t, err)
	for _, call := range rec.createCalls {
		for _, in := range call {
			require.NotEqual(t, "k1", in.RowKey, "already-loaded row must not be re-sent")
		}
	}
}

func TestService_Load_ReconcilesInterruptedBatch(t *testing.T) {
	t.Parallel()

	st := newFakeState()
	j := &fakeJournal{inflight: []wl.Batch{{
		ID: "acc-eur:k1", AccountID: "acc-eur", RowKeys: []string{"k1"},
		MinDate: "2024-02-03T10:00:00+00:00", MaxDate: "2024-02-03T10:00:00+00:00",
	}}}
	rec := &fakeRecords{findResp: []wl.RemoteRecord{
		// API echoes recordDate in UTC "Z" form; the core emitted "+00:00".
		{ID: "remote-1", Amount: "-12.0", RecordDate: "2024-02-03T10:00:00Z", Note: "lunch"},
	}}
	rows := []wl.ExportRow{sampleRows()[0]} // just k1
	svc := newService(fakeReader{rows: rows}, &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}, rec, st, j, &fakeWaiter{})

	_, err := svc.Load(context.Background(), baseOpts())
	require.NoError(t, err)
	require.Equal(t, "remote-1", st.commits["k1"], "in-flight row reconciled to its remote id")
	require.Contains(t, j.cleared, "acc-eur:k1")
	require.Empty(t, rec.createCalls, "reconciled row is not re-created")
}

func TestService_Load_LimitStopsEarly(t *testing.T) {
	t.Parallel()

	rec := &fakeRecords{}
	st := newFakeState()
	svc := newService(fakeReader{rows: sampleRows()}, &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}, rec, st, &fakeJournal{}, &fakeWaiter{})

	opts := baseOpts()
	opts.Limit = 1

	sum, err := svc.Load(context.Background(), opts)
	require.NoError(t, err)

	total := 0
	for _, v := range sum.PerAccountCreated {
		total += v
	}
	require.Equal(t, 1, total)
}

func TestService_Load_DryRun_WritesNothing(t *testing.T) {
	t.Parallel()

	cat := &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}
	rec := &fakeRecords{}
	svc := newService(fakeReader{rows: sampleRows()}, cat, rec, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	opts := baseOpts()
	opts.DryRun = true

	sum, err := svc.Load(context.Background(), opts)
	require.NoError(t, err)
	require.Empty(t, cat.created)
	require.Empty(t, rec.createCalls)
	require.Equal(t, 5, sum.RowsIn)
	require.NotEmpty(t, sum.CategoryMap)
	require.Equal(t, 1, sum.CategoriesFallback) // Pharmacy, not yet created
}

func TestService_Load_SingleRowAccount(t *testing.T) {
	t.Parallel()

	rows := []wl.ExportRow{
		{RowKey: "only", Account: "Denys UAH", Category: "Food", Currency: "UAH", Amount: "-42.00", Date: at("2022-02-02 02:02:02")},
	}
	rec := &fakeRecords{}
	st := newFakeState()
	svc := newService(fakeReader{rows: rows}, &fakeCatalog{accounts: sampleAccounts(), categories: sampleCategories()}, rec, st, &fakeJournal{}, &fakeWaiter{})

	sum, err := svc.Load(context.Background(), baseOpts())
	require.NoError(t, err)
	require.Equal(t, 1, sum.PerAccountCreated["acc-uah"])
	require.Len(t, rec.createCalls, 1)
	require.Len(t, rec.createCalls[0], 1)
}

func TestService_Load_ReaderError_Wrapped(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("disk gone")
	svc := newService(fakeReader{err: sentinel}, &fakeCatalog{}, &fakeRecords{}, newFakeState(), &fakeJournal{}, &fakeWaiter{})

	_, err := svc.Load(context.Background(), baseOpts())
	require.ErrorIs(t, err, sentinel)
}

func TestService_Rollback_DeletesKnownAndReportsOrphans(t *testing.T) {
	t.Parallel()

	st := newFakeState()
	st.createdIDs = []string{"id1", "id2"}
	rec := &fakeRecords{findResp: []wl.RemoteRecord{{ID: "id1"}, {ID: "id3"}}}
	svc := newService(fakeReader{}, &fakeCatalog{}, rec, st, &fakeJournal{}, &fakeWaiter{})

	sum, err := svc.Rollback(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, sum.Deleted)
	require.Equal(t, []string{"id3"}, sum.Orphans)
}
