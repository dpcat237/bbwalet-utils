package walletload

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrUnmappedAccount is returned by Load when an export account name has no
// resolved target account and no --account-map entry (and --create-missing is
// off).
var ErrUnmappedAccount = errors.New("export account has no target account")

// ErrAmbiguousAccountCurrency is returned when --create-missing is set but the
// currency of an account to create cannot be inferred from its export rows and
// no --account-currency override was given.
var ErrAmbiguousAccountCurrency = errors.New("cannot infer account currency from export rows")

// ErrUnresolvedCategories is returned by a live load when --create-missing is
// set, one or more export categories still resolve to nothing, and no
// --fallback-category was given.
var ErrUnresolvedCategories = errors.New("export categories could not be resolved")

// ErrBadAlias is returned when a categories-alias.csv target names a category
// that is not present in the live catalogue.
var ErrBadAlias = errors.New("category alias target is not a live category")

// ErrMissingParent is returned when a category to create names a parent that is
// not a live system category.
var ErrMissingParent = errors.New("category parent not found in the live catalogue")

// ErrUnauthorized is the core translation of a 401/403 from the Wallet API.
var ErrUnauthorized = errors.New("wallet api rejected the token")

// ErrRetriesExhausted is returned when a batch keeps hitting the rate limit
// past the retry budget.
var ErrRetriesExhausted = errors.New("record create retries exhausted")

// ErrExportUnreadable is the core translation of a failure to open or parse the
// export file.
var ErrExportUnreadable = errors.New("export file unreadable")

// ErrUnknownHeader is returned by an ExportReader when a required column is
// absent from the export header.
var ErrUnknownHeader = errors.New("export is missing a required column")

// ErrRateLimited carries the server's Retry-After hint for a 429 response.
type ErrRateLimited struct {
	RetryAfter time.Duration
}

// Error implements error.
func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("wallet api rate limited, retry after %s", e.RetryAfter)
}

// ExportReader reads and parses a Wallet CSV export.
type ExportReader interface {
	Read(ctx context.Context) ([]ExportRow, error)
}

// Catalog is the read surface for accounts and categories.
type Catalog interface {
	Accounts(ctx context.Context) ([]Account, error)
	Categories(ctx context.Context) ([]Category, error)
}

// CatalogWriter creates accounts and custom categories in the target Wallet. It
// is only used when --create-missing is set.
type CatalogWriter interface {
	CreateAccount(ctx context.Context, in CreateAccountInput) (Account, error)
	CreateCustomCategory(ctx context.Context, name, parentID string) (Category, error)
}

// Records is the record write/read surface.
type Records interface {
	CreateRecords(ctx context.Context, in []RecordInput) ([]RecordResult, error)
	DeleteRecords(ctx context.Context, ids []string) ([]RecordResult, error)
	FindRecords(ctx context.Context, q RecordQuery) ([]RemoteRecord, error)
}

// ResumeState tracks which export rows have already been written.
type ResumeState interface {
	Loaded(rowKey string) bool
	Commit(rowKey, recordID string) error
	CreatedIDs() ([]string, error)
}

// InFlightJournal records a batch immediately before its create call so a
// crash mid-batch can be reconciled on the next run.
type InFlightJournal interface {
	MarkInFlight(b Batch) error
	ClearInFlight(batchID string) error
	InFlight() ([]Batch, error)
}

// Waiter is a cancellable sleep used for rate-limit backoff. Keeping it a port
// keeps time.Sleep out of the core and lets tests run instantly.
type Waiter interface {
	Wait(ctx context.Context, d time.Duration) error
}

// Progress receives load-progress events at phase boundaries, batch boundaries,
// and rate-limit wait entry/exit. A nil Deps.Progress is a silent no-op; a
// Report error disables further reporting for the rest of the run.
type Progress interface {
	Report(ev ProgressEvent) error
}

// Deps bundles every port a Service needs.
type Deps struct {
	Reader        ExportReader
	Catalog       Catalog
	CatalogWriter CatalogWriter
	Records       Records
	State         ResumeState
	Journal       InFlightJournal
	Waiter        Waiter
	Progress      Progress
}
