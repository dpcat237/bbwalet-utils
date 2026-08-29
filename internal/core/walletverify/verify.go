package walletverify

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

// inputs bundles everything Verify reads before it starts comparing.
type inputs struct {
	archive    []ArchiveRow
	skipped    []SkippedRow
	categories []CategoryAction
	accounts   []Account
	loaded     []Record
}

// aggregate is a running count + exact sum for one account.
type aggregate struct {
	count int
	sum   *big.Rat
}

// Verify compares the archived export against the loaded Wallet state and
// returns the per-account count/sum result (R2), the main-currency and
// historical-rate diagnostic (R3), and the category spot-check (R4).
func (s *Service) Verify(ctx context.Context, opts Options) (Report, error) {
	opts = normalise(opts)

	in, err := s.fetchInputs(ctx, opts)
	if err != nil {
		return Report{}, err
	}

	accResults, err := compareAccounts(in, opts.AccountMap)
	if err != nil {
		return Report{}, err
	}

	sample, findings := rateDiagnostic(in.loaded, opts.ConvertTo, opts.RateSampleSize)

	rep := Report{
		Accounts:   accResults,
		RateSample: sample,
		Rates:      findings,
		Categories: categorySpotCheck(in, opts.AccountMap, opts.CategorySampleSize),
	}
	rep.OK = reportOK(rep)
	return rep, nil
}

func (s *Service) fetchInputs(ctx context.Context, opts Options) (inputs, error) {
	archive, err := s.deps.Archive.Read(ctx)
	if err != nil {
		return inputs{}, fmt.Errorf("reading archive: %w", err)
	}
	skipped, err := s.deps.Load.Skipped(ctx)
	if err != nil {
		return inputs{}, fmt.Errorf("reading skipped rows: %w", err)
	}
	cats, err := s.deps.Load.Categories(ctx)
	if err != nil {
		return inputs{}, fmt.Errorf("reading category map: %w", err)
	}
	accounts, err := s.deps.Wallet.Accounts(ctx)
	if err != nil {
		return inputs{}, fmt.Errorf("reading wallet accounts: %w", err)
	}
	loaded, err := s.loadRecords(ctx, accounts, opts)
	if err != nil {
		return inputs{}, err
	}
	return inputs{archive: archive, skipped: skipped, categories: cats, accounts: accounts, loaded: loaded}, nil
}

func (s *Service) loadRecords(ctx context.Context, accounts []Account, opts Options) ([]Record, error) {
	var out []Record
	for _, acc := range accounts {
		recs, err := s.deps.Wallet.Records(ctx, RecordQuery{
			AccountID: acc.ID, Source: sourceREST, ConvertTo: opts.ConvertTo,
		})
		if err != nil {
			return nil, fmt.Errorf("reading records for account %q: %w", acc.Name, err)
		}
		out = append(out, recs...)
	}
	return out, nil
}

// --- R2: per-account count + sum ------------------------------------------

func compareAccounts(in inputs, accountMap map[string]string) ([]AccountResult, error) {
	skip := skippedKeys(in.skipped)
	byName := indexAccountsByName(in.accounts)

	expected, err := archiveAggregates(in.archive, skip)
	if err != nil {
		return nil, err
	}
	actual, err := recordAggregates(in.loaded)
	if err != nil {
		return nil, err
	}

	out := make([]AccountResult, 0, len(expected)+len(actual))
	matched := map[string]bool{}
	for _, name := range sortedKeys(expected) {
		acc, ok := resolveAccount(name, byName, accountMap, in.accounts)
		act := aggregate{sum: new(big.Rat)}
		if ok {
			matched[acc.ID] = true
			if a, has := actual[acc.ID]; has {
				act = a
			}
		}
		out = append(out, accountResult(name, expected[name], act, ok))
	}
	return append(out, orphanAccounts(in.accounts, actual, matched)...), nil
}

func accountResult(name string, exp, act aggregate, resolved bool) AccountResult {
	r := AccountResult{
		Account:       name,
		ExpectedCount: exp.count,
		ActualCount:   act.count,
		ExpectedSum:   ratString(exp.sum),
		ActualSum:     ratString(act.sum),
	}
	switch {
	case !resolved:
		r.Status = AccountMissingInWallet
	case exp.count != act.count:
		r.Status = AccountCountMismatch
	case exp.sum.Cmp(act.sum) != 0:
		r.Status = AccountSumMismatch
	default:
		r.Status = AccountPass
	}
	return r
}

func orphanAccounts(accounts []Account, actual map[string]aggregate, matched map[string]bool) []AccountResult {
	var out []AccountResult
	for _, acc := range accounts {
		agg, has := actual[acc.ID]
		if !has || matched[acc.ID] {
			continue
		}
		out = append(out, AccountResult{
			Account:     acc.Name,
			ActualCount: agg.count,
			ExpectedSum: "0.00",
			ActualSum:   ratString(agg.sum),
			Status:      AccountMissingInArchive,
		})
	}
	return out
}

func archiveAggregates(archive []ArchiveRow, skip map[string]bool) (map[string]aggregate, error) {
	out := map[string]aggregate{}
	for _, row := range archive {
		if skip[row.RowKey] {
			continue
		}
		v, ok := new(big.Rat).SetString(strings.TrimSpace(row.Amount))
		if !ok {
			return nil, fmt.Errorf("%w: amount %q for account %q", ErrArchiveUnreadable, row.Amount, row.Account)
		}
		agg := out[row.Account]
		if agg.sum == nil {
			agg.sum = new(big.Rat)
		}
		agg.sum.Add(agg.sum, v)
		agg.count++
		out[row.Account] = agg
	}
	return out, nil
}

func recordAggregates(loaded []Record) (map[string]aggregate, error) {
	out := map[string]aggregate{}
	for _, r := range loaded {
		v, ok := new(big.Rat).SetString(strings.TrimSpace(r.Amount))
		if !ok {
			return nil, fmt.Errorf("%w: record %s amount %q", ErrWalletRead, r.ID, r.Amount)
		}
		agg := out[r.AccountID]
		if agg.sum == nil {
			agg.sum = new(big.Rat)
		}
		agg.sum.Add(agg.sum, v)
		agg.count++
		out[r.AccountID] = agg
	}
	return out, nil
}

// --- R3: rate diagnostic -------------------------------------------------

func rateDiagnostic(loaded []Record, convertTo string, sampleSize int) ([]RateSampleRow, []RateFinding) {
	byCurrency := map[string][]Record{}
	for _, r := range loaded {
		if r.CurrencyCode == "" || strings.EqualFold(r.CurrencyCode, convertTo) {
			continue
		}
		byCurrency[r.CurrencyCode] = append(byCurrency[r.CurrencyCode], r)
	}

	var sample []RateSampleRow
	var findings []RateFinding
	for _, cur := range sortedKeys(byCurrency) {
		rows := rateRows(cur, byCurrency[cur], sampleSize)
		distinct, verdict := ratioVerdict(rows)
		findings = append(findings, RateFinding{Currency: cur, DistinctRatios: distinct, Verdict: verdict})
		sample = append(sample, rows...)
	}
	return sample, findings
}

func rateRows(currency string, recs []Record, sampleSize int) []RateSampleRow {
	sorted := append([]Record(nil), recs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date.Before(sorted[j].Date) })

	rows := make([]RateSampleRow, 0, sampleSize)
	for _, idx := range evenIndices(len(sorted), sampleSize) {
		r := sorted[idx]
		rows = append(rows, RateSampleRow{
			Currency:     currency,
			Date:         r.Date,
			Amount:       r.Amount,
			ConvertedEUR: r.ConvertedValue,
			Ratio:        r.ConvertedRatio,
		})
	}
	return rows
}

func ratioVerdict(rows []RateSampleRow) (int, RateVerdict) {
	seen := map[string]bool{}
	for _, r := range rows {
		v, ok := new(big.Rat).SetString(strings.TrimSpace(r.Ratio))
		if !ok || v.Sign() == 0 {
			continue
		}
		seen[v.FloatString(6)] = true
	}
	switch len(seen) {
	case 0:
		return 0, RateNoSignal
	case 1:
		return 1, RateCurrent
	default:
		return len(seen), RateDateAccurate
	}
}

// --- R4: category spot-check -------------------------------------------

type matchState int

const (
	matchNone matchState = iota
	matchOne
	matchAmbiguous
)

func categorySpotCheck(in inputs, accountMap map[string]string, sampleSize int) CategoryFinding {
	f := CategoryFinding{ManualPass: manualPassList(in.categories)}
	skip := skippedKeys(in.skipped)
	byName := indexAccountsByName(in.accounts)

	sent := make([]ArchiveRow, 0, len(in.archive))
	for _, row := range in.archive {
		if !skip[row.RowKey] {
			sent = append(sent, row)
		}
	}

	for _, idx := range evenIndices(len(sent), sampleSize) {
		scoreCategoryRow(sent[idx], in, byName, accountMap, &f)
	}
	return f
}

func scoreCategoryRow(
	row ArchiveRow, in inputs, byName map[string]Account, accountMap map[string]string, f *CategoryFinding,
) {
	acc, ok := resolveAccount(row.Account, byName, accountMap, in.accounts)
	if !ok {
		f.Unmatched++
		return
	}
	rec, state := matchLoaded(row, acc.ID, in.loaded)
	switch state {
	case matchNone, matchAmbiguous:
		f.Unmatched++
	case matchOne:
		f.Sampled++
		if strings.EqualFold(strings.TrimSpace(row.Category), strings.TrimSpace(rec.CategoryName)) {
			f.Matched++
			return
		}
		f.Changed++
		f.Changes = append(f.Changes, CategoryChange{
			Account: row.Account, Date: row.Date, Amount: row.Amount,
			Archive: row.Category, Loaded: rec.CategoryName,
		})
	}
}

func matchLoaded(row ArchiveRow, accountID string, loaded []Record) (Record, matchState) {
	var found Record
	n := 0
	for _, r := range loaded {
		if r.AccountID != accountID || !sameInstant(r.Date, row.Date) || !amountsEqual(r.Amount, row.Amount) {
			continue
		}
		found = r
		n++
	}
	switch n {
	case 0:
		return Record{}, matchNone
	case 1:
		return found, matchOne
	default:
		return Record{}, matchAmbiguous
	}
}

func manualPassList(actions []CategoryAction) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range actions {
		if a.Action != "none" && a.Action != "fallback-parent" {
			continue
		}
		if seen[a.ExportCategory] {
			continue
		}
		seen[a.ExportCategory] = true
		out = append(out, a.ExportCategory)
	}
	sort.Strings(out)
	return out
}

// --- shared helpers ---------------------------------------------------

func reportOK(rep Report) bool {
	for _, a := range rep.Accounts {
		if a.Status != AccountPass {
			return false
		}
	}
	return true
}

func skippedKeys(rows []SkippedRow) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.RowKey] = true
	}
	return out
}

func indexAccountsByName(accounts []Account) map[string]Account {
	out := make(map[string]Account, len(accounts))
	for _, a := range accounts {
		out[strings.ToLower(strings.TrimSpace(a.Name))] = a
	}
	return out
}

func resolveAccount(
	name string, byName map[string]Account, accountMap map[string]string, accounts []Account,
) (Account, bool) {
	if id, ok := accountMap[name]; ok {
		for _, a := range accounts {
			if a.ID == id {
				return a, true
			}
		}
		return Account{ID: id, Name: name}, true
	}
	a, ok := byName[strings.ToLower(strings.TrimSpace(name))]
	return a, ok
}

// evenIndices returns up to n indices spread evenly across [0,total).
func evenIndices(total, n int) []int {
	if n <= 0 || total == 0 {
		return nil
	}
	if total <= n {
		out := make([]int, total)
		for i := range out {
			out[i] = i
		}
		return out
	}
	if n == 1 {
		return []int{total / 2}
	}
	out := make([]int, n)
	for i := range out {
		out[i] = i * (total - 1) / (n - 1)
	}
	return out
}

func ratString(r *big.Rat) string {
	if r == nil {
		return "0.00"
	}
	return r.FloatString(2)
}

// sameInstant compares an archive wall-clock time (parsed as UTC) with a loaded
// record's timestamp. It is exact when the load used the default "+00:00"
// tz-offset; a different offset shifts the match and shows up as an unmatched
// row in the spot-check.
func sameInstant(a, b time.Time) bool {
	return a.Equal(b)
}

func amountsEqual(a, b string) bool {
	ar, aok := new(big.Rat).SetString(strings.TrimSpace(a))
	br, bok := new(big.Rat).SetString(strings.TrimSpace(b))
	return aok && bok && ar.Cmp(br) == 0
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
