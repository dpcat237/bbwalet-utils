package walletmigrate

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

// progressSnapshot is the latest known load-progress state: updated on every
// event, read by the heartbeat.
type progressSnapshot struct {
	phase         walletload.LoadPhase
	account       string
	created       int
	total         int
	alreadyLoaded int
	requests      int
	retries       int
}

// progressReporter implements walletload.Progress. It keeps a mutex-guarded
// snapshot, emits an immediate slog line on phase and rate-limit-wait events,
// and drives a heartbeat that re-emits the snapshot on an interval. A panic or
// failure anywhere in the progress path is logged once and disables further
// output — the load itself is never affected.
type progressReporter struct {
	started  time.Time
	interval time.Duration

	mu       sync.Mutex
	snap     progressSnapshot
	disabled bool
}

// newProgressReporter returns a reporter that measures elapsed time from now
// and beats every interval (a non-positive interval disables the heartbeat).
func newProgressReporter(interval time.Duration, now time.Time) *progressReporter {
	return &progressReporter{started: now, interval: interval}
}

// Report folds ev into the snapshot and, for phase and wait events, emits an
// immediate progress line. It always returns nil: progress is best-effort and
// self-contained, so the core never sees a failure here.
func (r *progressReporter) Report(ev walletload.ProgressEvent) error {
	defer r.recoverPanic("report")

	r.mu.Lock()
	if r.disabled {
		r.mu.Unlock()
		return nil
	}

	r.fold(ev)
	snap := r.snap
	r.mu.Unlock()

	if immediate(ev.Kind) {
		r.emit(snap, ev)
	}

	return nil
}

func immediate(k walletload.ProgressKind) bool {
	switch k {
	case walletload.ProgressStart, walletload.ProgressPhase,
		walletload.ProgressWaitEnter, walletload.ProgressWaitExit:
		return true
	case walletload.ProgressBatch:
		return false
	default:
		return false
	}
}

func (r *progressReporter) fold(ev walletload.ProgressEvent) {
	switch ev.Kind {
	case walletload.ProgressStart:
		r.snap.total = ev.Total
		r.snap.alreadyLoaded = ev.AlreadyLoaded
	case walletload.ProgressPhase:
		r.snap.phase = ev.Phase
	case walletload.ProgressBatch:
		r.snap.phase = ev.Phase
		r.snap.account = ev.Account
		r.snap.created = ev.Created
		r.snap.total = ev.Total
		r.snap.requests = ev.Requests
		r.snap.retries = ev.Retries
	case walletload.ProgressWaitEnter, walletload.ProgressWaitExit:
		r.snap.phase = ev.Phase
		r.snap.requests = ev.Requests
		r.snap.retries = ev.Retries
	}
}

// emit writes one progress line to the default slog logger (stderr).
func (r *progressReporter) emit(snap progressSnapshot, ev walletload.ProgressEvent) {
	defer r.recoverPanic("emit")

	args := []any{
		"phase", string(snap.phase),
		"created", snap.created,
		"total", snap.total,
		"already_loaded", snap.alreadyLoaded,
		"account", snap.account,
		"requests", snap.requests,
		"retries", snap.retries,
		"elapsed", time.Since(r.started).Round(time.Second),
	}
	if ev.Kind == walletload.ProgressWaitEnter {
		args = append(args, "wait", ev.WaitDuration, "reason", ev.WaitReason)
	}

	slog.Info("progress", args...)
}

// emitSnapshot re-emits the current snapshot. The heartbeat calls it on a tick.
func (r *progressReporter) emitSnapshot() {
	defer r.recoverPanic("heartbeat")

	r.mu.Lock()
	if r.disabled {
		r.mu.Unlock()
		return
	}

	snap := r.snap
	r.mu.Unlock()

	r.emit(snap, walletload.ProgressEvent{})
}

// runHeartbeat re-emits the progress snapshot every interval until ctx is done
// or done is closed. It returns immediately when the interval is not positive.
// ctx is a parameter, never stored, so a cancelled run stops the beat promptly.
func (r *progressReporter) runHeartbeat(ctx context.Context, done <-chan struct{}) {
	if r.interval <= 0 {
		return
	}

	t := time.NewTicker(r.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			r.emitSnapshot()
		}
	}
}

// fail disables the progress path and logs the cause exactly once. The warn is
// itself best-effort: a slog sink that panics must not crash the load.
func (r *progressReporter) fail(where, msg string) {
	r.mu.Lock()
	already := r.disabled
	r.disabled = true
	r.mu.Unlock()

	if already {
		return
	}

	defer func() { _ = recover() }() //nolint:errcheck // best-effort: a broken slog sink must not crash the load
	slog.Warn("progress path failed", "at", where, "err", msg)
}

func (r *progressReporter) recoverPanic(where string) {
	if rec := recover(); rec != nil {
		r.fail(where, fmt.Sprint(rec))
	}
}
