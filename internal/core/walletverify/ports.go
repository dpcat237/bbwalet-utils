package walletverify

import (
	"context"
	"errors"
)

// ErrArchiveUnreadable is returned when the archived export cannot be opened or
// parsed.
var ErrArchiveUnreadable = errors.New("archived export unreadable")

// ErrLoadReportUnreadable is returned when task 5's out/ reports cannot be read.
var ErrLoadReportUnreadable = errors.New("load report unreadable")

// ErrWalletRead is returned when a Wallet REST API read fails.
var ErrWalletRead = errors.New("wallet api read failed")

// ArchiveReader reads the archived Wallet CSV export.
type ArchiveReader interface {
	Read(ctx context.Context) ([]ArchiveRow, error)
}

// LoadReport exposes the parts of task 5's out/ directory the verifier needs.
type LoadReport interface {
	Skipped(ctx context.Context) ([]SkippedRow, error)
	Categories(ctx context.Context) ([]CategoryAction, error)
}

// WalletReader is the read-only Wallet REST API surface the verifier needs.
// The Wallet REST API exposes no base/main-currency endpoint (and returns a
// converted amount only when convertTo is passed), so the verifier cannot read
// the wallet's base currency — that check is left for a manual pass (task 6a).
type WalletReader interface {
	Accounts(ctx context.Context) ([]Account, error)
	Records(ctx context.Context, q RecordQuery) ([]Record, error)
}

// Deps bundles every port a Service needs.
type Deps struct {
	Archive ArchiveReader
	Load    LoadReport
	Wallet  WalletReader
}
