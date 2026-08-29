package walletverify_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/app/walletverify"
)

const accountsJSON = `{"accounts":[{"id":"acc-eur","name":"Denys EUR","currencyCode":"EUR"}]}`

const bothRecordsJSON = `{"records":[
	{"id":"w1","amount":{"value":"-12.00","currencyCode":"EUR"},"category":{"name":"Internet"},
	 "recordDate":"2020-01-15T10:00:00Z","source":"rest","accountId":"acc-eur","accountName":"Denys EUR"},
	{"id":"w2","amount":{"value":"-8.50","currencyCode":"EUR"},"category":{"name":"Food"},
	 "recordDate":"2021-06-09T08:00:00Z","source":"rest","accountId":"acc-eur","accountName":"Denys EUR"}
]}`

const oneRecordJSON = `{"records":[
	{"id":"w1","amount":{"value":"-12.00","currencyCode":"EUR"},"category":{"name":"Internet"},
	 "recordDate":"2020-01-15T10:00:00Z","source":"rest","accountId":"acc-eur","accountName":"Denys EUR"}
]}`

// walletStub answers the endpoints wallet-verify reads.
func walletStub(t *testing.T, recordsJSON string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/accounts":
			_, _ = w.Write([]byte(accountsJSON))
		case "/v1/api/records":
			_, _ = w.Write([]byte(recordsJSON))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setEnv(t *testing.T, baseURL, token, outDir string) {
	t.Helper()
	t.Setenv("APP_HTTP_TIMEOUT", "5s")
	t.Setenv("APP_WALLET_BASE_URL", baseURL)
	t.Setenv("APP_WALLET_API_TOKEN", token)
	t.Setenv("APP_OUTPUT_DIR", outDir)
}

func runVerify(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := walletverify.Run(context.Background(), "test", args, &out, &bytes.Buffer{})
	return out.String(), err
}

func TestRun_Verify_HappyWritesReport(t *testing.T) {
	srv := walletStub(t, bothRecordsJSON)
	outDir := t.TempDir()
	setEnv(t, srv.URL, "tok", outDir)

	stdout, err := runVerify(t,
		"--export", filepath.Join("testdata", "archive.csv"),
		"--load-report-dir", "testdata")
	require.NoError(t, err)
	require.Contains(t, stdout, "PASS")

	body, err := os.ReadFile(filepath.Join(outDir, "_verify_report.txt"))
	require.NoError(t, err)
	require.Contains(t, string(body), "Denys EUR")
	require.Contains(t, string(body), "R3 — main currency")
	require.NotContains(t, string(body), "tok")
}

func TestRun_Verify_CountMismatchReturnsErrMismatch(t *testing.T) {
	srv := walletStub(t, oneRecordJSON)
	outDir := t.TempDir()
	setEnv(t, srv.URL, "tok", outDir)

	stdout, err := runVerify(t,
		"--export", filepath.Join("testdata", "archive.csv"),
		"--load-report-dir", "testdata")
	require.ErrorIs(t, err, walletverify.ErrMismatch)
	require.Contains(t, stdout, "MISMATCH")

	body, err := os.ReadFile(filepath.Join(outDir, "_verify_report.txt"))
	require.NoError(t, err)
	require.Contains(t, string(body), "count-mismatch")
}

func TestRun_Verify_MissingTokenIsError(t *testing.T) {
	srv := walletStub(t, bothRecordsJSON)
	setEnv(t, srv.URL, "", t.TempDir())

	_, err := runVerify(t, "--export", filepath.Join("testdata", "archive.csv"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "APP_WALLET_API_TOKEN")
}

func TestRun_Verify_MissingExportIsError(t *testing.T) {
	srv := walletStub(t, bothRecordsJSON)
	setEnv(t, srv.URL, "tok", t.TempDir())

	_, err := runVerify(t)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--export")
}

func TestRun_Verify_MissingCategoryMapIsError(t *testing.T) {
	srv := walletStub(t, bothRecordsJSON)
	setEnv(t, srv.URL, "tok", t.TempDir())

	_, err := runVerify(t,
		"--export", filepath.Join("testdata", "archive.csv"),
		"--load-report-dir", t.TempDir()) // empty dir: no _category_map.csv
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "load report")
}
