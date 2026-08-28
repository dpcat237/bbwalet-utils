package walletmigrate

import (
	"context"
	"time"
)

// waiter is the real walletload.Waiter: a cancellable sleep for rate-limit
// backoff. It uses time.After rather than time.Sleep so a cancelled context
// returns immediately (and so forbidigo stays happy).
type waiter struct{}

// Wait blocks for d or until ctx is done, whichever comes first.
func (waiter) Wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // returning the raw context error is the intended contract
	case <-t.C:
		return nil
	}
}
