//go:build e2e

package wallethttp_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/wallethttp"
	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

// requireE2E gates a live test on a disposable Wallet. It skips (never fails)
// unless APP_WALLET_E2E=1 and both APP_WALLET_BASE_URL and APP_WALLET_API_TOKEN
// are set — point them at a THROWAWAY Wallet, never a real one.
func requireE2E(t *testing.T) *wallethttp.Client {
	t.Helper()
	if os.Getenv("APP_WALLET_E2E") != "1" {
		t.Skip("set APP_WALLET_E2E=1 (and a disposable-Wallet .env.e2e) to run e2e tests")
	}
	base := os.Getenv("APP_WALLET_BASE_URL")
	token := os.Getenv("APP_WALLET_API_TOKEN")
	if base == "" || token == "" {
		t.Skip("APP_WALLET_BASE_URL / APP_WALLET_API_TOKEN not set")
	}
	return wallethttp.New(&http.Client{Timeout: 30 * time.Second}, base, token)
}

// TestClient_E2E_AccountAndCategoryRoundTrip proves the create paths against the
// live API: an already-present fixture resolves without a create; an absent one
// is created and then shows up in the catalogue. Accounts/categories are not
// deleted afterwards (the API has no delete) — the developer wipes the test
// Wallet periodically. Any record created is deleted.
func TestClient_E2E_AccountAndCategoryRoundTrip(t *testing.T) {
	c := requireE2E(t)
	ctx := context.Background()

	const acctName = "E2E Probe EUR"
	accts, err := c.Accounts(ctx)
	require.NoError(t, err)

	var acctID string
	for _, a := range accts {
		if a.Name == acctName {
			acctID = a.ID
		}
	}
	if acctID == "" {
		created, cerr := c.CreateAccount(ctx, core.CreateAccountInput{
			Name: acctName, CurrencyCode: "EUR", AccountType: "General", InitialBalance: "0",
		})
		require.NoError(t, cerr)
		acctID = created.ID

		after, aerr := c.Accounts(ctx)
		require.NoError(t, aerr)
		require.Contains(t, accountNames(after), acctName, "created account appears in the catalogue")
	}
	require.NotEmpty(t, acctID)

	// custom category under "Others"
	cats, err := c.Categories(ctx)
	require.NoError(t, err)
	var othersID string
	for _, cat := range cats {
		if cat.Name == "Others" {
			othersID = cat.ID
		}
	}
	require.NotEmpty(t, othersID, "system Others category present")

	const catName = "E2E Probe Cat"
	if !containsCat(cats, catName) {
		_, cerr := c.CreateCustomCategory(ctx, catName, othersID)
		require.NoError(t, cerr)
		after, aerr := c.Categories(ctx)
		require.NoError(t, aerr)
		require.True(t, containsCat(after, catName), "created category appears in the catalogue")
	}
}

func accountNames(as []core.Account) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name
	}
	return out
}

func containsCat(cs []core.Category, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}
