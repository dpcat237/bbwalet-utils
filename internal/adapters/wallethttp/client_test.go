package wallethttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/wallethttp"
	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

const testToken = "secret-token-value"

type route struct {
	status  int
	fixture string
	headers map[string]string
}

// server routes "METHOD /path" (with query stripped, page-aware for offset) to a
// fixture and asserts every request carries a bearer header (never the value).
func server(t *testing.T, routes map[string]route) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))

		key := r.Method + " " + r.URL.Path
		if off := r.URL.Query().Get("offset"); off != "" && off != "0" {
			key += "?offset=" + off
		}
		rt, ok := routes[key]
		if !ok {
			t.Fatalf("unexpected request %s (query %s)", key, r.URL.RawQuery)
		}
		for k, v := range rt.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(rt.status)
		if rt.fixture != "" {
			b, err := os.ReadFile(filepath.Join("testdata", rt.fixture))
			require.NoError(t, err)
			_, _ = w.Write(b)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T, routes map[string]route) *wallethttp.Client {
	t.Helper()
	srv := server(t, routes)
	return wallethttp.New(&http.Client{Timeout: 5 * time.Second}, srv.URL, testToken)
}

// seqClient serves each key's responses in order, repeating the last entry.
func seqClient(t *testing.T, seqs map[string][]route) *wallethttp.Client {
	t.Helper()
	calls := map[string]int{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
		key := r.Method + " " + r.URL.Path
		list, ok := seqs[key]
		if !ok {
			t.Fatalf("unexpected request %s", key)
		}
		mu.Lock()
		i := calls[key]
		calls[key]++
		mu.Unlock()
		if i >= len(list) {
			i = len(list) - 1
		}
		rt := list[i]
		for k, v := range rt.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(rt.status)
		b, err := os.ReadFile(filepath.Join("testdata", rt.fixture))
		require.NoError(t, err)
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return wallethttp.New(&http.Client{Timeout: 5 * time.Second}, srv.URL, testToken)
}

func TestClient_Accounts_FollowsPagination(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"GET /v1/api/accounts":            {status: 200, fixture: "accounts_page1.json"},
		"GET /v1/api/accounts?offset=200": {status: 200, fixture: "accounts_page2.json"},
	})

	got, err := c.Accounts(context.Background())
	require.NoError(t, err)
	require.Equal(t, []core.Account{
		{ID: "acc-eur", Name: "Denys EUR", CurrencyCode: "EUR"},
		{ID: "acc-uah", Name: "Denys UAH", CurrencyCode: "UAH"},
		{ID: "acc-usd", Name: "Catherine USD", CurrencyCode: "USD"},
	}, got)
}

func TestClient_Categories_MapsCustomAndGroup(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"GET /v1/api/categories": {status: 200, fixture: "categories_page1.json"},
	})

	got, err := c.Categories(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "Food & Drinks", got[0].GroupName)
	require.False(t, got[0].Custom)
	require.True(t, got[2].Custom)
	require.Equal(t, "Pharmacy", got[2].Name)
}

func TestClient_CreateCustomCategory(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"POST /v1/api/categories/custom": {status: 201, fixture: "category_created.json"},
	})

	got, err := c.CreateCustomCategory(context.Background(), "Books", "cat-others")
	require.NoError(t, err)
	require.Equal(t, "new-cat-1", got.ID)
	require.True(t, got.Custom)
}

func TestClient_CreateRecords_AllOK(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"POST /v1/api/records": {status: 200, fixture: "records_201.json"},
	})

	in := []core.RecordInput{{RowKey: "k1", Amount: "-12.00"}, {RowKey: "k2", Amount: "-5.00"}}
	got, err := c.CreateRecords(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, []core.RecordResult{
		{RowKey: "k1", RecordID: "rec-1", OK: true},
		{RowKey: "k2", RecordID: "rec-2", OK: true},
	}, got)
}

func TestClient_CreateRecords_PartialFailure_207(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"POST /v1/api/records": {status: http.StatusMultiStatus, fixture: "records_207.json"},
	})

	in := []core.RecordInput{{RowKey: "k1"}, {RowKey: "k2"}}
	got, err := c.CreateRecords(context.Background(), in)
	require.NoError(t, err)
	require.True(t, got[0].OK)
	require.False(t, got[1].OK)
	require.Equal(t, "k2", got[1].RowKey)
	require.Contains(t, got[1].Err, "zero")
}

func TestClient_CreateRecords_AllFailed_400(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"POST /v1/api/records": {status: 400, fixture: "records_400.json"},
	})

	got, err := c.CreateRecords(context.Background(), []core.RecordInput{{RowKey: "k1"}})
	require.NoError(t, err) // 400 batch body is data, not a transport error
	require.False(t, got[0].OK)
	require.Contains(t, got[0].Err, "not found")
}

func TestClient_CreateRecords_Unauthorized(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"POST /v1/api/records": {status: 401, fixture: "error_401.json"},
	})

	_, err := c.CreateRecords(context.Background(), []core.RecordInput{{RowKey: "k1"}})
	require.ErrorIs(t, err, core.ErrUnauthorized)
}

func TestClient_CreateRecords_RateLimited_ParsesRetryAfter(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"POST /v1/api/records": {
			status:  429,
			fixture: "error_429.json",
			headers: map[string]string{"Retry-After": "7"},
		},
	})

	_, err := c.CreateRecords(context.Background(), []core.RecordInput{{RowKey: "k1"}})
	var rl *core.ErrRateLimited
	require.ErrorAs(t, err, &rl)
	require.Equal(t, 7*time.Second, rl.RetryAfter)
}

func TestClient_DeleteRecords(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"DELETE /v1/api/records": {status: 200, fixture: "delete_200.json"},
	})

	got, err := c.DeleteRecords(context.Background(), []string{"rec-1", "rec-2"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.True(t, got[0].OK)
	require.Equal(t, "rec-1", got[0].RecordID)
}

func TestClient_FindRecords(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"GET /v1/api/records": {status: 200, fixture: "records_find.json"},
	})

	got, err := c.FindRecords(context.Background(), core.RecordQuery{Source: "rest", AccountID: "acc-eur"})
	require.NoError(t, err)
	require.Equal(t, []core.RemoteRecord{
		{ID: "remote-1", Amount: "-12.0", RecordDate: "2024-02-03T10:00:00Z", Note: "lunch"},
	}, got)
}

func TestClient_Accounts_RetriesCatalogueGetOn429(t *testing.T) {
	t.Parallel()

	c := seqClient(t, map[string][]route{
		"GET /v1/api/accounts": {
			{status: 429, fixture: "error_429.json", headers: map[string]string{"Retry-After": "0"}},
			{status: 200, fixture: "accounts_page2.json"},
		},
	})

	got, err := c.Accounts(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1) // page2 has one account; the 429 was retried, not fatal
}

func TestClient_DeleteRecords_RetriesOn429(t *testing.T) {
	t.Parallel()

	c := seqClient(t, map[string][]route{
		"DELETE /v1/api/records": {
			{status: 429, fixture: "error_429.json", headers: map[string]string{"Retry-After": "0"}},
			{status: 200, fixture: "delete_200.json"},
		},
	})

	got, err := c.DeleteRecords(context.Background(), []string{"rec-1", "rec-2"})
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestClient_NoTokenInErrorOutput(t *testing.T) {
	t.Parallel()

	c := newClient(t, map[string]route{
		"POST /v1/api/records": {status: 500, fixture: "error_401.json"},
	})

	_, err := c.CreateRecords(context.Background(), []core.RecordInput{{RowKey: "k1"}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), testToken)
}
