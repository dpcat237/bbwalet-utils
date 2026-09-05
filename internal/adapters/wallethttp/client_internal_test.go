package wallethttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

func TestRetryAfter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		header string
		set    bool
		want   time.Duration
	}{
		{name: "positive delta-seconds", header: "7", set: true, want: 7 * time.Second},
		{name: "zero falls back to default", header: "0", set: true, want: defaultRetryAfter},
		{name: "negative falls back to default", header: "-5", set: true, want: defaultRetryAfter},
		{name: "non-numeric falls back to default", header: "Wed, 21 Oct 2026 07:28:00 GMT", set: true, want: defaultRetryAfter},
		{name: "missing falls back to default", set: false, want: defaultRetryAfter},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			if tc.set {
				h.Set("Retry-After", tc.header)
			}
			require.Equal(t, tc.want, retryAfter(h))
			require.Positive(t, retryAfter(h), "retryAfter must never return a zero wait")
		})
	}
}

func TestStatusError_NameConflict(t *testing.T) {
	t.Parallel()

	t.Run("400 name_conflict with disclosed id -> ErrAlreadyExists{ID}", func(t *testing.T) {
		t.Parallel()
		body := []byte(`{"error":"name_conflict","message":"a category with name 'X' already exists (id: 53cbff21-5039-4287-94b6-7ce71986f06a, custom category)"}`)
		err := statusError(http.StatusBadRequest, body)
		var ex *core.ErrAlreadyExists
		require.ErrorAs(t, err, &ex)
		require.Equal(t, "53cbff21-5039-4287-94b6-7ce71986f06a", ex.ID)
	})

	t.Run("409 name_conflict without an id -> ErrAlreadyExists{}", func(t *testing.T) {
		t.Parallel()
		err := statusError(http.StatusConflict, []byte(`{"error":"name_conflict","message":"already exists"}`))
		var ex *core.ErrAlreadyExists
		require.ErrorAs(t, err, &ex)
		require.Empty(t, ex.ID)
	})

	t.Run("other 400 is a plain status error", func(t *testing.T) {
		t.Parallel()
		err := statusError(http.StatusBadRequest, []byte(`{"error":"validation","message":"bad field"}`))
		var ex *core.ErrAlreadyExists
		require.False(t, errors.As(err, &ex))
	})
}

func TestIsTransientTransport(t *testing.T) {
	t.Parallel()

	require.True(t, isTransientTransport(io.EOF))
	require.True(t, isTransientTransport(fmt.Errorf("calling wallet api: %w", io.ErrUnexpectedEOF)))
	require.False(t, isTransientTransport(nil))
	require.False(t, isTransientTransport(context.Canceled))
	require.False(t, isTransientTransport(context.DeadlineExceeded))
	require.False(t, isTransientTransport(errors.New("some domain error")))
}
