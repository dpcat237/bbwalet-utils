// Package walletverify wires and runs the wallet-verify command: a read-only
// check that compares the archived Wallet CSV export against the loaded Wallet
// state via the REST API and reports per-account transaction-count and
// sum(amount) agreement (R2), a main-currency and historical-rate diagnostic
// (R3), and a category spot-check (R4). It never writes to Wallet.
package walletverify

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
	"github.com/dpcat237/bbwalet-utils/internal/adapters/loadreport"
	"github.com/dpcat237/bbwalet-utils/internal/adapters/walletcsv"
	"github.com/dpcat237/bbwalet-utils/internal/adapters/wallethttp"
	"github.com/dpcat237/bbwalet-utils/internal/core/walletverify"
	"github.com/dpcat237/bbwalet-utils/internal/platform/logger"
)

const reportFileName = "_verify_report.txt"

// ErrMismatch is returned when verification finds at least one account whose
// count or sum disagrees with the archive. The report is written first, so the
// caller can exit non-zero while still leaving the details on disk.
var ErrMismatch = errors.New("verification found account mismatches")

type cliOptions struct {
	export         string
	loadReportDir  string
	accountMap     string
	rateSampleSize int
	categorySample int
	convertTo      string
	out            string
}

// Run is the testable entry point behind cmd/wallet-verify.
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
	logger.Init(stderr)
	return run(ctx, opts, stdout)
}

func parseFlags(args []string, stderr io.Writer) (cliOptions, error) {
	fs := flag.NewFlagSet("wallet-verify", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var o cliOptions
	fs.StringVar(&o.export, "export", "", "path to the archived Wallet CSV export (required)")
	fs.StringVar(&o.loadReportDir, "load-report-dir", "", "task 5 out/ directory (default: APP_OUTPUT_DIR)")
	fs.StringVar(&o.accountMap, "account-map", "", "CSV file mapping archived account name -> Wallet account id")
	fs.IntVar(&o.rateSampleSize, "rate-sample-size", 20, "historical records per foreign currency for the rate diagnostic")
	fs.IntVar(&o.categorySample, "category-sample-size", 50, "records to spot-check for category agreement")
	fs.StringVar(&o.convertTo, "convert-to", "EUR", "currency Wallet is asked to convert records to")
	fs.StringVar(&o.out, "out", "", "report file path (default: <APP_OUTPUT_DIR>/_verify_report.txt)")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err //nolint:wrapcheck // flag already printed usage to stderr; caller special-cases ErrHelp
	}
	return o, nil
}

func run(ctx context.Context, o cliOptions, stdout io.Writer) error {
	if o.export == "" {
		return errors.New("--export is required")
	}
	cfg := config.Settings
	if cfg.WalletAPIToken == "" {
		return errors.New("APP_WALLET_API_TOKEN is required — set it in .env.dev or .env.local")
	}

	loadDir := o.loadReportDir
	if loadDir == "" {
		loadDir = cfg.OutputDir
	}
	outPath := o.out
	if outPath == "" {
		outPath = filepath.Join(cfg.OutputDir, reportFileName)
	}

	accountMap, err := loadAccountMap(o.accountMap)
	if err != nil {
		return err
	}

	client := wallethttp.NewVerify(&http.Client{Timeout: cfg.HTTPTimeout}, cfg.WalletBaseURL, cfg.WalletAPIToken)
	svc := walletverify.New(walletverify.Deps{
		Archive: walletcsv.NewArchiveReader(o.export),
		Load:    loadreport.New(loadDir),
		Wallet:  client,
	})

	rep, err := svc.Verify(ctx, walletverify.Options{
		AccountMap:         accountMap,
		RateSampleSize:     o.rateSampleSize,
		CategorySampleSize: o.categorySample,
		ConvertTo:          o.convertTo,
	})
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}

	if werr := writeReport(outPath, rep); werr != nil {
		return werr
	}
	if serr := summaryLine(stdout, rep, outPath); serr != nil {
		return serr
	}
	if !rep.OK {
		return ErrMismatch
	}
	return nil
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
