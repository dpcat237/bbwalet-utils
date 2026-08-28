package walletmigrate_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/app/walletmigrate"
)

const accountsJSON = `{"accounts":[{"id":"acc-eur","name":"Denys EUR","currencyCode":"EUR"}]}`

const categoriesJSON = `{"categories":[` +
	`{"id":"cat-food","name":"Food","customCategory":false,"group":{"name":"Food & Drinks"}},` +
	`{"id":"cat-others","name":"Others","customCategory":false,"group":{"name":"Others"}}` +
	`]}`

// walletStub answers the handful of endpoints the app calls during a load.
func walletStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/api/accounts":
			_, _ = w.Write([]byte(accountsJSON))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/api/categories":
			_, _ = w.Write([]byte(categoriesJSON))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/api/categories/custom":
			_, _ = w.Write([]byte(`{"category":{"id":"new-books","name":"Books","customCategory":true}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/api/records":
			_, _ = w.Write([]byte(`{"results":[{"inputIndex":0,"id":"rec-1","success":true},{"inputIndex":1,"id":"rec-2","success":true}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/api/records":
			_, _ = w.Write([]byte(`{"records":[]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/api/records":
			_, _ = w.Write([]byte(`{"results":[{"inputIndex":0,"id":"rec-1","success":true}]}`))
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

func exportPath() string { return filepath.Join("testdata", "export.csv") }

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := walletmigrate.Run(context.Background(), "test", args, &out, &out)
	return out.String(), err
}

func TestRun_DryRun_WritesReportsNoRecords(t *testing.T) {
	srv := walletStub(t)
	outDir := t.TempDir()
	setEnv(t, srv.URL, "tok", outDir)

	stdout, err := run(t, "--export", exportPath(), "--dry-run")
	require.NoError(t, err)
	require.Contains(t, stdout, "planned (dry run)")
	require.Contains(t, stdout, "would create 2 records across 1 accounts")
	require.Contains(t, stdout, "1 custom categories to create")

	require.FileExists(t, filepath.Join(outDir, "_load_summary.txt"))
	require.FileExists(t, filepath.Join(outDir, "_category_map.csv"))
	require.FileExists(t, filepath.Join(outDir, "_review.csv"))
	require.NoFileExists(t, filepath.Join(outDir, "_created_records.csv"))

	review, err := os.ReadFile(filepath.Join(outDir, "_review.csv"))
	require.NoError(t, err)
	require.Contains(t, string(review), "foreign-currency") // the USD row
}

func TestRun_FullLoad_CreatesRecordsFile(t *testing.T) {
	srv := walletStub(t)
	outDir := t.TempDir()
	setEnv(t, srv.URL, "tok", outDir)

	stdout, err := run(t, "--export", exportPath())
	require.NoError(t, err)
	require.Contains(t, stdout, "loaded")

	created, err := os.ReadFile(filepath.Join(outDir, "_created_records.csv"))
	require.NoError(t, err)
	require.Contains(t, string(created), "rec-1")
	require.Contains(t, string(created), "rec-2")

	catMap, err := os.ReadFile(filepath.Join(outDir, "_category_map.csv"))
	require.NoError(t, err)
	require.Contains(t, string(catMap), "Books")
}

func TestRun_LiveRunWithoutToken_Errors(t *testing.T) {
	srv := walletStub(t)
	setEnv(t, srv.URL, "", t.TempDir())

	_, err := run(t, "--export", exportPath())
	require.Error(t, err)
	require.Contains(t, err.Error(), "APP_WALLET_API_TOKEN")
}

func TestRun_UnmappedAccount_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/api/accounts" {
			_, _ = w.Write([]byte(`{"accounts":[{"id":"x","name":"Someone Else","currencyCode":"EUR"}]}`))
			return
		}
		_, _ = w.Write([]byte(categoriesJSON))
	}))
	t.Cleanup(srv.Close)
	setEnv(t, srv.URL, "tok", t.TempDir())

	_, err := run(t, "--export", exportPath())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Denys EUR")
}

func TestRun_MissingExportFlag_Errors(t *testing.T) {
	setEnv(t, "http://unused.example", "tok", t.TempDir())

	_, err := run(t)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--export")
}

func TestRun_Rollback_DeletesAndReports(t *testing.T) {
	srv := walletStub(t)
	outDir := t.TempDir()
	setEnv(t, srv.URL, "tok", outDir)
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "_created_records.csv"), []byte("k1,rec-1\n"), 0o600))

	stdout, err := run(t, "--rollback")
	require.NoError(t, err)
	require.Contains(t, stdout, "rolled back")

	summary, err := os.ReadFile(filepath.Join(outDir, "_load_summary.txt"))
	require.NoError(t, err)
	require.Contains(t, string(summary), "records deleted:")
}

func TestRun_Help_ReturnsNil(t *testing.T) {
	setEnv(t, "http://unused.example", "tok", t.TempDir())
	_, err := run(t, "-h")
	require.NoError(t, err)
}

func TestRun_Progress_LiveLoadEmitsProgressLine(t *testing.T) {
	srv := walletStub(t)
	outDir := t.TempDir()
	setEnv(t, srv.URL, "tok", outDir)

	// interval 0 disables the heartbeat; phase/wait events still fire.
	out, err := run(t, "--export", exportPath(), "--progress-interval", "0")
	require.NoError(t, err)
	require.Contains(t, out, "msg=progress")
	require.Contains(t, out, "phase=creating-records")
}
