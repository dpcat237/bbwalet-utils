package walletmigrate

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

// syncBuf is a mutex-guarded buffer so the heartbeat goroutine and the test
// goroutine can read/write slog output without a data race.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

func captureSlog(t *testing.T) *syncBuf {
	t.Helper()

	b := &syncBuf{}
	prev := slog.Default()

	slog.SetDefault(slog.New(slog.NewTextHandler(b, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return b
}

func waitClosed(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()

	require.Eventually(t, func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}, time.Second, 5*time.Millisecond, msg)
}

func TestProgressReporter_Heartbeat_FiresAndStopsOnCancel(t *testing.T) {
	buf := captureSlog(t)
	r := newProgressReporter(10*time.Millisecond, time.Now())
	require.NoError(t, r.Report(walletload.ProgressEvent{
		Kind: walletload.ProgressPhase, Phase: walletload.PhaseCreatingRecords,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		r.runHeartbeat(ctx, done)
		close(finished)
	}()

	require.Eventually(t, func() bool {
		return strings.Count(buf.String(), "msg=progress") >= 2
	}, 2*time.Second, 5*time.Millisecond, "heartbeat should emit repeatedly")

	cancel()
	waitClosed(t, finished, "heartbeat goroutine must return after ctx cancel")
	close(done)
}

func TestProgressReporter_Heartbeat_DisabledWhenIntervalZero(t *testing.T) {
	buf := captureSlog(t)
	r := newProgressReporter(0, time.Now())
	finished := make(chan struct{})

	go func() {
		r.runHeartbeat(context.Background(), make(chan struct{}))
		close(finished)
	}()

	waitClosed(t, finished, "runHeartbeat must return immediately at interval 0")
	require.Empty(t, buf.String(), "no heartbeat lines at interval 0")
}

func TestProgressReporter_Report_FoldsSnapshot(t *testing.T) {
	buf := captureSlog(t)
	r := newProgressReporter(time.Hour, time.Now())

	require.NoError(t, r.Report(walletload.ProgressEvent{
		Kind: walletload.ProgressStart, Total: 9187, AlreadyLoaded: 12,
	}))
	require.NoError(t, r.Report(walletload.ProgressEvent{
		Kind: walletload.ProgressPhase, Phase: walletload.PhaseCreatingRecords,
	}))
	require.NoError(t, r.Report(walletload.ProgressEvent{
		Kind: walletload.ProgressBatch, Phase: walletload.PhaseCreatingRecords,
		Account: "Denys UAH", Created: 1830, Total: 9175, Requests: 61, Retries: 2,
	}))

	r.mu.Lock()
	require.Equal(t, "Denys UAH", r.snap.account)
	require.Equal(t, 1830, r.snap.created)
	require.Equal(t, 12, r.snap.alreadyLoaded)
	r.mu.Unlock()

	// start + phase events emitted immediate lines; the batch event did not.
	require.Contains(t, buf.String(), "msg=progress")
	require.Contains(t, buf.String(), "phase=creating-records")
	require.Contains(t, buf.String(), "already_loaded=12") // R6: stated on the first line

	r.emitSnapshot()
	require.Contains(t, buf.String(), `account="Denys UAH"`)
	require.Contains(t, buf.String(), "created=1830")

	// a wait-enter event emits an immediate line carrying the wait detail.
	require.NoError(t, r.Report(walletload.ProgressEvent{
		Kind: walletload.ProgressWaitEnter, Phase: walletload.PhaseWaitingRateLimited,
		WaitDuration: 90 * time.Second, WaitReason: "429",
	}))
	require.Contains(t, buf.String(), "phase=waiting-rate-limited")
	require.Contains(t, buf.String(), "wait=1m30s")
	require.Contains(t, buf.String(), "reason=429")
}

func TestProgressReporter_Report_FailureIsContained(t *testing.T) {
	buf := captureSlog(t)
	r := newProgressReporter(time.Hour, time.Now())

	r.fail("test", "boom")

	r.mu.Lock()
	disabled := r.disabled
	r.mu.Unlock()
	require.True(t, disabled)

	require.NoError(t, r.Report(walletload.ProgressEvent{
		Kind: walletload.ProgressPhase, Phase: walletload.PhaseCreatingRecords,
	}))

	r.fail("test2", "boom2")

	require.Equal(t, 1, strings.Count(buf.String(), "progress path failed"), "logged once")
	require.NotContains(t, buf.String(), "level=INFO", "no progress line after disable")
}

func TestProgressReporter_Heartbeat_StopsOnDoneChannel(t *testing.T) {
	captureSlog(t)
	r := newProgressReporter(10*time.Millisecond, time.Now())
	done := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		r.runHeartbeat(context.Background(), done)
		close(finished)
	}()

	close(done)
	waitClosed(t, finished, "heartbeat must return when done is closed")
}

// panicHandler panics on every log call, standing in for a broken slog sink.
type panicHandler struct{}

func (panicHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (panicHandler) Handle(context.Context, slog.Record) error { panic("handler boom") }
func (panicHandler) WithAttrs([]slog.Attr) slog.Handler        { return panicHandler{} }
func (panicHandler) WithGroup(string) slog.Handler             { return panicHandler{} }

func TestProgressReporter_Report_RecoversSinkPanic(t *testing.T) {
	prev := slog.Default()
	slog.SetDefault(slog.New(panicHandler{}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := newProgressReporter(time.Hour, time.Now())

	require.NotPanics(t, func() {
		err := r.Report(walletload.ProgressEvent{
			Kind: walletload.ProgressPhase, Phase: walletload.PhaseCreatingRecords,
		})
		require.NoError(t, err)
	})

	r.mu.Lock()
	disabled := r.disabled
	r.mu.Unlock()
	require.True(t, disabled, "a panicking sink disables the progress path")
}
