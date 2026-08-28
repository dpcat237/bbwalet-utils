package walletload

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrUnmappedAccount is returned by Load when an export account name has no
// resolved target account and no --account-map entry.
var ErrUnmappedAccount = errors.New("export account has no target account")

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

// Catalog is the read/create surface for accounts and categories.
type Catalog interface {
	Accounts(ctx context.Context) ([]Account, error)
	Categories(ctx context.Context) ([]Category, error)
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
	Reader   ExportReader
	Catalog  Catalog
	Records  Records
	State    ResumeState
	Journal  InFlightJournal
	Waiter   Waiter
	Progress Progress
}
