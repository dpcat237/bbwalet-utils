// Package walletverify is the core domain for verifying a completed
// BudgetBakers Wallet migration. It compares the archived CSV export against the
// loaded Wallet state (read through ports) and reports, per account, transaction
// count and sum(amount) agreement (R2), a main-currency / historical-rate
// diagnostic (R3), and a category spot-check (R4). It carries no infrastructure
// imports and does not import the walletload domain: the CSV, HTTP, and
// report-file details live behind the ports in ports.go.
package walletverify

import "time"

// Base currency and the foreign currencies present in this migration. Declared
// as consts because they recur across the comparison and sampling code.
const (
	currencyEUR = "EUR"
	sourceREST  = "rest"
)

// ArchiveRow is one transaction line parsed from the archived Wallet export.
type ArchiveRow struct {
	// RowKey is a stable identifier for this physical row.
	RowKey   string
	Account  string
	Currency string
	Amount   string // decimal literal, e.g. "-12.00"
	Category string
	Date     time.Time // wall-clock from the export, no zone
}

// SkippedRow mirrors one row of task 5's out/_review.csv: an export row the
// loader deliberately did not send.
type SkippedRow struct {
	RowKey   string
	Account  string
	Currency string
	Amount   string
	Reason   string // "foreign-currency" | "zero-amount" | "future-date"
}

// CategoryAction mirrors one row of task 5's out/_category_map.csv.
type CategoryAction struct {
	ExportCategory string
	Action         string // "resolved" | "created" | "fallback-parent" | "none"
	ResolvedID     string
}

// Account is a Wallet account as read from the REST API.
type Account struct {
	ID           string
	Name         string
	CurrencyCode string
}

// Record is one loaded Wallet record, with the conversion Wallet computed.
type Record struct {
	ID                string
	AccountID         string
	AccountName       string
	CategoryName      string
	Amount            string // account-currency decimal literal
	CurrencyCode      string
	ConvertedValue    string // converted value Wallet computed ("" when absent)
	ConvertedRatio    string // Wallet's exchange rate ("" when absent)
	ConvertedCurrency string // currency of ConvertedValue ("" when absent)
	Date              time.Time
	Source            string
}

// RecordQuery filters a WalletReader.Records call. An empty ConvertTo omits the
// convertTo parameter, so ConvertedCurrency then reflects the wallet's base.
type RecordQuery struct {
	AccountID string
	Source    string
	ConvertTo string
}

// AccountStatus is the R2 verdict for one account.
type AccountStatus string

// R2 account verdicts.
const (
	AccountPass             AccountStatus = "pass"
	AccountCountMismatch    AccountStatus = "count-mismatch"
	AccountSumMismatch      AccountStatus = "sum-mismatch"
	AccountMissingInWallet  AccountStatus = "missing-in-wallet"
	AccountMissingInArchive AccountStatus = "missing-in-archive"
)

// AccountResult is the per-account R2 comparison.
type AccountResult struct {
	Account       string
	ExpectedCount int
	ActualCount   int
	ExpectedSum   string
	ActualSum     string
	Status        AccountStatus
}

// RateSampleRow is one sampled historical record for the R3 diagnostic.
type RateSampleRow struct {
	Currency     string
	Date         time.Time
	Amount       string
	ConvertedEUR string
	Ratio        string
}

// RateVerdict is the R3 diagnostic hint for one foreign currency.
type RateVerdict string

// R3 rate verdicts.
const (
	RateDateAccurate RateVerdict = "date-accurate"
	RateCurrent      RateVerdict = "current-rate"
	RateNoSignal     RateVerdict = "no-signal"
)

// RateFinding summarises the ratio sample for one foreign currency.
type RateFinding struct {
	Currency       string
	DistinctRatios int
	Verdict        RateVerdict
}

// CategoryChange is one sampled record whose loaded category differs from the
// archived one.
type CategoryChange struct {
	Account string
	Date    time.Time
	Amount  string
	Archive string
	Loaded  string
}

// CategoryFinding is the R4 spot-check result.
type CategoryFinding struct {
	Sampled    int
	Matched    int
	Changed    int
	Unmatched  int
	Changes    []CategoryChange
	ManualPass []string // _category_map.csv categories with action none|fallback-parent
}

// Report is the full verification outcome. The main/base currency is not part
// of it: the Wallet REST API does not expose it (see WalletReader) — confirm it
// by eye in the app (task 6a).
type Report struct {
	Accounts   []AccountResult
	RateSample []RateSampleRow
	Rates      []RateFinding
	Categories CategoryFinding
	OK         bool
}

// Options are the per-run choices (CLI flags).
type Options struct {
	AccountMap         map[string]string // archived account name -> Wallet account id
	RateSampleSize     int
	CategorySampleSize int
	ConvertTo          string // default "EUR"
}

func normalise(o Options) Options {
	if o.ConvertTo == "" {
		o.ConvertTo = currencyEUR
	}
	if o.RateSampleSize <= 0 {
		o.RateSampleSize = 20
	}
	if o.CategorySampleSize <= 0 {
		o.CategorySampleSize = 50
	}
	return o
}
