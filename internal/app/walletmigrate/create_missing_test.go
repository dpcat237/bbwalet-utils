package walletmigrate_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	walletload "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

// createStub answers a load where the export account is absent from the
// catalogue (so --create-missing must POST it) and one category needs the alias.
func createStub(t *testing.T, catExtra string) (*httptest.Server, *createCalls) {
	t.Helper()
	calls := &createCalls{}
	cats := `{"categories":[` +
		`{"id":"cat-bar","name":"Bar cafe","customCategory":false,"group":{"name":"Food & Drinks"}},` +
		`{"id":"cat-food","name":"Food","customCategory":false,"group":{"name":"Food & Drinks"}},` +
		`{"id":"cat-others","name":"Others","customCategory":false,"group":{"name":"Others"}}` + catExtra +
		`]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/api/accounts":
			calls.record("GET accounts")
			_, _ = w.Write([]byte(`{"accounts":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/api/categories":
			_, _ = w.Write([]byte(cats))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/api/accounts":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			calls.recordAccount(body)
			_, _ = w.Write([]byte(`{"account":{"id":"acc-new","name":"Denys USD","currencyCode":"USD"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/api/records":
			calls.record("POST records")
			_, _ = w.Write([]byte(`{"results":[{"inputIndex":0,"id":"r1","success":true},{"inputIndex":1,"id":"r2","success":true}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/api/records":
			_, _ = w.Write([]byte(`{"records":[]}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

type createCalls struct {
	mu       sync.Mutex
	seen     []string
	accounts []map[string]any
}

func (c *createCalls) record(s string) { c.mu.Lock(); c.seen = append(c.seen, s); c.mu.Unlock() }
func (c *createCalls) recordAccount(b map[string]any) {
	c.mu.Lock()
	c.accounts = append(c.accounts, b)
	c.mu.Unlock()
}

func TestRun_CreateMissing_CreatesAccountAndLoads(t *testing.T) {
	srv, calls := createStub(t, "")
	outDir := t.TempDir()
	setEnv(t, srv.URL, "tok", outDir)

	stdout, err := run(t,
		"--export", "testdata/export_create.csv",
		"--create-missing",
		"--category-alias", "testdata/alias.csv",
	)
	require.NoError(t, err)
	require.Contains(t, stdout, "loaded")

	require.Len(t, calls.accounts, 1)
	require.Equal(t, "Denys USD", calls.accounts[0]["name"])
	require.Equal(t, "USD", calls.accounts[0]["currencyCode"])
	require.Equal(t, "General", calls.accounts[0]["accountType"])
}

func TestRun_CreateMissing_DryRunListsWithoutWriting(t *testing.T) {
	srv, calls := createStub(t, "")
	outDir := t.TempDir()
	setEnv(t, srv.URL, "tok", outDir)

	stdout, err := run(t,
		"--export", "testdata/export_create.csv",
		"--create-missing", "--dry-run",
		"--category-alias", "testdata/alias.csv",
	)
	require.NoError(t, err)
	require.Contains(t, stdout, `+ account "Denys USD" (USD, General, balance 0)`)
	require.Empty(t, calls.accounts, "dry run must not POST an account")
}

func TestRun_CreateMissing_MalformedInitialBalance_Errors(t *testing.T) {
	srv, _ := createStub(t, "")
	setEnv(t, srv.URL, "tok", t.TempDir())

	_, err := run(t,
		"--export", "testdata/export_create.csv",
		"--create-missing",
		"--account-initial-balance", "Denys USD",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--account-initial-balance")
}

func TestRun_CreateMissing_ExplicitAliasFileMissing_Errors(t *testing.T) {
	srv, _ := createStub(t, "")
	setEnv(t, srv.URL, "tok", t.TempDir())

	_, err := run(t,
		"--export", "testdata/export_create.csv",
		"--create-missing",
		"--category-alias", "testdata/does-not-exist.csv",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestRun_CreateMissing_AccountCurrencyOverrideParsed(t *testing.T) {
	srv, calls := createStub(t, "")
	setEnv(t, srv.URL, "tok", t.TempDir())

	_, err := run(t,
		"--export", "testdata/export_create.csv",
		"--create-missing",
		"--category-alias", "testdata/alias.csv",
		"--account-currency", "Denys USD=EUR",
		"--account-initial-balance", "Denys USD=99.50",
	)
	require.NoError(t, err)
	require.Len(t, calls.accounts, 1)
	require.Equal(t, "EUR", calls.accounts[0]["currencyCode"])
	require.EqualValues(t, 99.5, calls.accounts[0]["initialBalance"])
}

func TestRun_CreateMissing_UnresolvedCategoryHardStops(t *testing.T) {
	srv, _ := createStub(t, "")
	setEnv(t, srv.URL, "tok", t.TempDir())

	// No alias file -> "Bar, cafe" normalises to "Bar cafe" and still resolves,
	// so add a row with a truly unknown category via a dedicated export.
	_, err := run(t,
		"--export", "testdata/export_create_unknown.csv",
		"--create-missing",
	)
	require.ErrorIs(t, err, walletload.ErrUnresolvedCategories)
}
