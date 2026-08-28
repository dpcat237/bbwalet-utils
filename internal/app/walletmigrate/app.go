// Package walletmigrate wires and runs the wallet-migrate command: it loads a
// BudgetBakers Wallet CSV export into a reset EUR-base Wallet through the Wallet
// REST API — per account, categories mapped/created, batched, rate-limited,
// resumable and reversible.
package walletmigrate

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dpcat237/bbwalet-utils/config"
	"github.com/dpcat237/bbwalet-utils/internal/adapters/resumestore"
	"github.com/dpcat237/bbwalet-utils/internal/adapters/walletcsv"
	"github.com/dpcat237/bbwalet-utils/internal/adapters/wallethttp"
	"github.com/dpcat237/bbwalet-utils/internal/core/walletload"
	"github.com/dpcat237/bbwalet-utils/internal/platform/logger"
)

type cliOptions struct {
	export         string
	dryRun         bool
	rollback       bool
	limit          int
	batchSize      int
	tzOffset       string
	accountMap     string
	fallbackParent string
	fallbackCatID  string
	resumeState    string
}

// Run is the testable entry point behind cmd/wallet-migrate.
func Run(ctx context.Context, _ string, args []string, stdout, stderr io.Writer) error {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}
	if err := config.Load(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger.Init()
	return run(ctx, opts, stdout)
}

func parseFlags(args []string, stderr io.Writer) (cliOptions, error) {
	fs := flag.NewFlagSet("wallet-migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var o cliOptions
	fs.StringVar(&o.export, "export", "", "path to the Wallet CSV export")
	fs.BoolVar(&o.dryRun, "dry-run", false, "validate mapping and print planned calls without writing")
	fs.BoolVar(&o.rollback, "rollback", false, "delete the records this tool created (uses the resume state)")
	fs.IntVar(&o.limit, "limit", 0, "stop after creating N records (0 = no limit)")
	fs.IntVar(&o.batchSize, "batch-size", 50, "records per POST /records call (1-50)")
	fs.StringVar(&o.tzOffset, "tz-offset", "+00:00", "zone offset appended to each record date")
	fs.StringVar(&o.accountMap, "account-map", "", "CSV file mapping export account name -> Wallet account id")
	fs.StringVar(&o.fallbackParent, "fallback-parent", "Others", "system category to parent unmatched custom categories")
	fs.StringVar(&o.fallbackCatID, "fallback-category", "", "category id for unmatched non-custom categories")
	fs.StringVar(&o.resumeState, "resume-state", "", "created-records file (default <output>/_created_records.csv)")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err //nolint:wrapcheck // flag already printed usage to stderr; caller special-cases ErrHelp
	}
	return o, nil
}

func run(ctx context.Context, o cliOptions, stdout io.Writer) error {
	cfg := config.Settings
	resumePath := o.resumeState
	if resumePath == "" {
		resumePath = filepath.Join(cfg.OutputDir, "_created_records.csv")
	}

	store, err := resumestore.New(resumePath)
	if err != nil {
		return fmt.Errorf("opening resume store: %w", err)
	}
	if (o.rollback || !o.dryRun) && cfg.WalletAPIToken == "" {
		return errors.New("APP_WALLET_API_TOKEN is required for a live run — set it in .env.dev or .env.local")
	}

	client := wallethttp.New(&http.Client{Timeout: cfg.HTTPTimeout}, cfg.WalletBaseURL, cfg.WalletAPIToken)
	svc := walletload.New(walletload.Deps{
		Reader:  walletcsv.New(o.export),
		Catalog: client,
		Records: client,
		State:   store,
		Journal: store,
		Waiter:  waiter{},
	})

	if o.rollback {
		return doRollback(ctx, svc, cfg.OutputDir, stdout)
	}
	return doLoad(ctx, svc, o, cfg.OutputDir, stdout)
}

func doRollback(ctx context.Context, svc *walletload.Service, outDir string, stdout io.Writer) error {
	sum, err := svc.Rollback(ctx)
	if err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	if err := writeReports(outDir, sum); err != nil {
		return err
	}
	return printSummary(stdout, sum, "rolled back")
}

func doLoad(ctx context.Context, svc *walletload.Service, o cliOptions, outDir string, stdout io.Writer) error {
	if o.export == "" {
		return errors.New("--export is required")
	}
	accountMap, err := loadAccountMap(o.accountMap)
	if err != nil {
		return err
	}
	sum, err := svc.Load(ctx, walletload.LoadOptions{
		BatchSize:          o.batchSize,
		Limit:              o.limit,
		TZOffset:           o.tzOffset,
		AccountMap:         accountMap,
		FallbackParent:     o.fallbackParent,
		FallbackCategoryID: o.fallbackCatID,
		DryRun:             o.dryRun,
	})
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if err := writeReports(outDir, sum); err != nil {
		return err
	}
	mode := "loaded"
	if o.dryRun {
		mode = "planned (dry run)"
	}
	return printSummary(stdout, sum, mode)
}

func loadAccountMap(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == "" {
		return out, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is an operator-supplied CLI argument
	if err != nil {
		return nil, fmt.Errorf("reading account map: %w", err)
	}
	records, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing account map: %w", err)
	}
	for _, rec := range records {
		if len(rec) < 2 {
			continue
		}
		name := strings.TrimSpace(rec[0])
		if name == "" || strings.EqualFold(name, "export_name") || strings.EqualFold(name, "account") {
			continue
		}
		out[name] = strings.TrimSpace(rec[1])
	}
	return out, nil
}

func printSummary(stdout io.Writer, sum walletload.Summary, mode string) error {
	created := 0
	for _, n := range sum.PerAccountCreated {
		created += n
	}
	_, err := fmt.Fprintf(stdout,
		"wallet-migrate: %s — rows in %d, records %d, skipped %d, categories +%d, requests %d, retries %d\n",
		mode, sum.RowsIn, created, len(sum.SkippedRows), sum.CategoriesCreated+sum.CategoriesFallback,
		sum.Requests, sum.Retries)
	if err != nil {
		return fmt.Errorf("writing summary: %w", err)
	}
	return nil
}
