// Package walletload is the core domain for loading a BudgetBakers Wallet CSV
// export into a reset EUR-base Wallet via the Wallet REST API. It groups rows
// per account, resolves and creates categories, batches record creation, and
// tracks progress for a resumable, reversible load. It carries no
// infrastructure imports: the CSV, HTTP, and file-store details live behind the
// ports in ports.go.
package walletload

import "time"

// ExportRow is one parsed transaction line from a Wallet CSV export.
type ExportRow struct {
	// RowKey is a stable identifier for this physical row, used to skip
	// already-loaded rows on a resumed run.
	RowKey         string
	Account        string
	Category       string
	Currency       string
	Amount         string // normalised decimal literal, e.g. "-12.00"
	Payee          string
	Note           string
	Date           time.Time // wall-clock from the export, no zone
	CustomCategory bool
}

// Account is a target Wallet account.
type Account struct {
	ID           string
	Name         string
	CurrencyCode string
}

// Category is a Wallet category, system or custom.
type Category struct {
	ID        string
	Name      string
	GroupName string
	SystemID  string
	Custom    bool
}

// RecordInput is a single record to create through the Wallet API.
type RecordInput struct {
	RowKey       string
	AccountID    string
	Amount       string // decimal literal; the adapter emits it as a JSON number
	CurrencyCode string
	RecordDate   string // ISO 8601 with a zone offset
	CategoryID   string
	CounterParty string
	Note         string
}

// RecordResult is the outcome of one record create or delete.
type RecordResult struct {
	RowKey   string
	RecordID string
	OK       bool
	Err      string
}

// RemoteRecord is a minimal view of a record already stored in Wallet, used to
// reconcile an interrupted batch.
type RemoteRecord struct {
	ID         string
	Amount     string
	RecordDate string
	Note       string
}

// RecordQuery filters a FindRecords lookup.
type RecordQuery struct {
	AccountID string
	Source    string
	FromDate  string
	ToDate    string
}

// Batch is a group of rows recorded in the in-flight journal immediately before
// its create call, so a crash mid-batch can be reconciled on the next run.
type Batch struct {
	ID          string   `json:"id"`
	AccountID   string   `json:"accountId"`
	RowKeys     []string `json:"rowKeys"`
	PayloadHash string   `json:"payloadHash"`
	MinDate     string   `json:"minDate"`
	MaxDate     string   `json:"maxDate"`
}

// CategoryAction records what the loader did with one export category.
type CategoryAction string

// Category actions reported in the category map.
const (
	CategoryResolved       CategoryAction = "resolved"
	CategoryCreated        CategoryAction = "created"
	CategoryFallbackParent CategoryAction = "fallback-parent"
	CategoryNone           CategoryAction = "none"
)

// CategoryMapping is one row of the category-map report (R3).
type CategoryMapping struct {
	ExportCategory string
	Action         CategoryAction
	ResolvedID     string
}

// SkipReason explains why a row was diverted to the review file instead of
// being sent.
type SkipReason string

// Skip reasons written to the review file (R6, R4 edge cases).
const (
	SkipZeroAmount      SkipReason = "zero-amount"
	SkipForeignCurrency SkipReason = "foreign-currency"
	SkipFutureDate      SkipReason = "future-date"
)

// SkippedRow is a row that will not be sent, with the reason.
type SkippedRow struct {
	RowKey string
	Reason SkipReason
	Row    ExportRow
}

// PlannedCategory is a custom category the loader will create.
type PlannedCategory struct {
	Name     string
	ParentID string
}

// LoadOptions are the per-run choices (CLI flags).
type LoadOptions struct {
	BatchSize          int
	Limit              int    // 0 = no limit
	TZOffset           string // e.g. "+00:00"
	AccountMap         map[string]string
	FallbackParent     string // system category name for unmatched custom categories
	FallbackCategoryID string // used for unmatched non-custom categories; "" = no category
	DryRun             bool
}

// Plan is the outcome of validating an export against the live catalogue,
// without writing anything.
type Plan struct {
	Rows               int
	PerAccountToSend   map[string]int
	CategoriesToCreate []PlannedCategory
	CategoryMap        []CategoryMapping
	Skipped            []SkippedRow
	UnmappedAccounts   []string
}

// Summary is the load or rollback report (R8).
type Summary struct {
	RowsIn             int
	Skipped            map[SkipReason]int
	SkippedRows        []SkippedRow
	PerAccountCreated  map[string]int
	CategoriesResolved int
	CategoriesCreated  int
	CategoriesFallback int
	CategoriesNone     int
	CategoryMap        []CategoryMapping
	Requests           int
	Retries            int
	RateLimitHits      int
	Deleted            int
	Orphans            []string
	Failures           []RecordResult
}

func newSummary() *Summary {
	return &Summary{
		Skipped:           map[SkipReason]int{},
		PerAccountCreated: map[string]int{},
	}
}
