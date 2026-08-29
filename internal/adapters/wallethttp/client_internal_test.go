package wallethttp

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
