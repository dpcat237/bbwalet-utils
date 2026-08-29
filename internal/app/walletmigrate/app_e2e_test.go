//go:build e2e

package walletmigrate_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/wallethttp"
	"github.com/dpcat237/bbwalet-utils/internal/app/walletmigrate"
	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

func requireE2EEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("APP_WALLET_E2E") != "1" {
		t.Skip("set APP_WALLET_E2E=1 (and a disposable-Wallet .env.e2e) to run e2e tests")
	}
	if os.Getenv("APP_WALLET_BASE_URL") == "" || os.Getenv("APP_WALLET_API_TOKEN") == "" {
		t.Skip("APP_WALLET_BASE_URL / APP_WALLET_API_TOKEN not set")
	}
}

const e2eExport = `account;category;currency;amount;ref_currency_amount;type;payment_type;payment_type_local;note;date;gps_latitude;gps_longitude;gps_accuracy_in_meters;warranty_in_month;transfer;payee;labels;envelope_id;custom_category
E2E Probe EUR;Groceries;EUR;-3.00;-3.00;Expenses;CASH;Cash;e2e-a;2024-02-03 10:27:52;;;;0;false;;;7002;false
E2E Probe EUR;Groceries;EUR;-4.00;-4.00;Expenses;CASH;Cash;e2e-b;2024-02-04 11:00:00;;;;0;false;;;7002;false
`

// TestRun_E2E_CreateMissingIdempotent runs a tiny --create-missing load against
// a disposable Wallet twice: the first run creates the "E2E Probe EUR" account
// and 2 records; the second run creates nothing new. The records it made are
// deleted in cleanup; the account persists (no API delete) for the next run.
func TestRun_E2E_CreateMissingIdempotent(t *testing.T) {
	requireE2EEnv(t)
	ctx := context.Background()

	dir := t.TempDir()
	exp := filepath.Join(dir, "export.csv")
	require.NoError(t, os.WriteFile(exp, []byte(e2eExport), 0o600))

	base := os.Getenv("APP_WALLET_BASE_URL")
	token := os.Getenv("APP_WALLET_API_TOKEN")
	t.Setenv("APP_HTTP_TIMEOUT", "30s")
	t.Setenv("APP_OUTPUT_DIR", dir)

	client := wallethttp.New(&http.Client{Timeout: 30 * time.Second}, base, token)
	t.Cleanup(func() { deleteE2ERecords(context.Background(), t, client) })

	args := []string{"--export", exp, "--create-missing", "--progress-interval", "0"}

	var out1 bytes.Buffer
	require.NoError(t, walletmigrate.Run(ctx, "e2e", args, &out1, &out1))
	require.Contains(t, out1.String(), "loaded")

	var out2 bytes.Buffer
	require.NoError(t, walletmigrate.Run(ctx, "e2e", args, &out2, &out2))
	require.Contains(t, out2.String(), "records 0", "second run is idempotent — no new records")
}

func deleteE2ERecords(ctx context.Context, t *testing.T, c *wallethttp.Client) {
	t.Helper()
	recs, err := c.FindRecords(ctx, core.RecordQuery{Source: "rest"})
	if err != nil {
		t.Logf("e2e cleanup: listing records: %v", err)
		return
	}
	var ids []string
	for _, r := range recs {
		if r.Note == "e2e-a" || r.Note == "e2e-b" {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	if _, err := c.DeleteRecords(ctx, ids); err != nil {
		t.Logf("e2e cleanup: deleting %d records: %v", len(ids), err)
	}
}
