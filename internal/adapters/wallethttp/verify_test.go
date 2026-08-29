package wallethttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/wallethttp"
	verify "github.com/dpcat237/bbwalet-utils/internal/core/walletverify"
)

func newVerifyClient(t *testing.T, routes map[string]route) *wallethttp.VerifyClient {
	t.Helper()
	srv := server(t, routes)
	return wallethttp.NewVerify(&http.Client{Timeout: 5 * time.Second}, srv.URL, testToken)
}

// queryVerifyClient serves one fixture for every request and records the raw
// query of the last records request, for asserting param presence/absence.
func queryVerifyClient(t *testing.T, fixture string, lastQuery *string) *wallethttp.VerifyClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
		if r.URL.Path == "/v1/api/records" {
			*lastQuery = r.URL.RawQuery
		}
		b, err := os.ReadFile(filepath.Join("testdata", fixture))
		require.NoError(t, err)
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return wallethttp.NewVerify(&http.Client{Timeout: 5 * time.Second}, srv.URL, testToken)
}

func TestVerifyClient_Accounts(t *testing.T) {
	t.Parallel()

	c := newVerifyClient(t, map[string]route{
		"GET /v1/api/accounts": {status: 200, fixture: "verify_accounts.json"},
	})

	got, err := c.Accounts(context.Background())
	require.NoError(t, err)
	require.Equal(t, []verify.Account{
		{ID: "acc-eur", Name: "Denys EUR", CurrencyCode: "EUR"},
		{ID: "acc-usd", Name: "Denys USD", CurrencyCode: "USD"},
	}, got)
}

func TestVerifyClient_Records_PaginatesAndProjectsConversion(t *testing.T) {
	t.Parallel()

	c := newVerifyClient(t, map[string]route{
		"GET /v1/api/records":            {status: 200, fixture: "verify_records_page1.json"},
		"GET /v1/api/records?offset=200": {status: 200, fixture: "verify_records_page2.json"},
	})

	got, err := c.Records(context.Background(), verify.RecordQuery{
		AccountID: "acc-usd", Source: "rest", ConvertTo: "EUR",
	})
	require.NoError(t, err)
	require.Len(t, got, 3)

	require.Equal(t, "Food", got[0].CategoryName)
	require.Equal(t, "-12.00", got[0].Amount)
	require.Equal(t, "USD", got[0].CurrencyCode)
	require.Equal(t, "-11.00", got[0].ConvertedValue)
	require.Equal(t, "0.916667", got[0].ConvertedRatio)
	require.Equal(t, "EUR", got[0].ConvertedCurrency)
	require.Equal(t, time.Date(2020, 1, 15, 10, 0, 0, 0, time.UTC), got[0].Date)

	// r2 has no convertedAmount — empty ratio, not an error.
	require.Equal(t, "Salary", got[1].CategoryName)
	require.Empty(t, got[1].ConvertedRatio)
	require.Empty(t, got[1].ConvertedCurrency)

	require.Equal(t, "Pharmacy", got[2].CategoryName)
}

func TestVerifyClient_Records_SendsConvertToWhenSet(t *testing.T) {
	t.Parallel()

	var q string
	c := queryVerifyClient(t, "verify_records_page2.json", &q)

	_, err := c.Records(context.Background(), verify.RecordQuery{AccountID: "acc-usd", Source: "rest", ConvertTo: "EUR"})
	require.NoError(t, err)
	require.Contains(t, q, "convertTo=EUR")
	require.Contains(t, q, "recordDate=gte.1970")
}

func TestVerifyClient_Records_OmitsConvertToWhenEmpty(t *testing.T) {
	t.Parallel()

	var q string
	c := queryVerifyClient(t, "verify_records_page2.json", &q)

	_, err := c.Records(context.Background(), verify.RecordQuery{AccountID: "acc-usd", Source: "rest"})
	require.NoError(t, err)
	require.NotContains(t, q, "convertTo")
}

func TestVerifyClient_Records_BadRecordDate(t *testing.T) {
	t.Parallel()

	c := newVerifyClient(t, map[string]route{
		"GET /v1/api/records": {status: 200, fixture: "verify_records_baddate.json"},
	})

	_, err := c.Records(context.Background(), verify.RecordQuery{AccountID: "acc-usd"})
	require.ErrorIs(t, err, verify.ErrWalletRead)
}

func TestVerifyClient_Records_Unauthorized(t *testing.T) {
	t.Parallel()

	c := newVerifyClient(t, map[string]route{
		"GET /v1/api/records": {status: 401, fixture: "error_401.json"},
	})

	_, err := c.Records(context.Background(), verify.RecordQuery{AccountID: "acc-usd"})
	require.ErrorIs(t, err, verify.ErrWalletRead)
}

func TestVerifyClient_NoTokenInErrorOutput(t *testing.T) {
	t.Parallel()

	c := newVerifyClient(t, map[string]route{
		"GET /v1/api/records": {status: 500, fixture: "error_401.json"},
	})

	_, err := c.Records(context.Background(), verify.RecordQuery{AccountID: "acc-usd"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), testToken)
}
