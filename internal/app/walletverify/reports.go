package walletverify

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dpcat237/bbwalet-utils/internal/core/walletverify"
)

const (
	outFilePerm = 0o600
	outDirPerm  = 0o750
)

// writeReport renders the verification report to path.
func writeReport(path string, rep walletverify.Report) error {
	if err := os.MkdirAll(filepath.Dir(path), outDirPerm); err != nil {
		return fmt.Errorf("creating report directory: %w", err)
	}
	if err := os.WriteFile(path, reportText(rep), outFilePerm); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}

func reportText(rep walletverify.Report) []byte {
	var b bytes.Buffer
	writeAccountSection(&b, rep)
	writeCurrencySection(&b)
	writeRateSection(&b, rep)
	writeCategorySection(&b, rep)
	fmt.Fprintf(&b, "\nnote: actual = the account's source=rest records. Accurate for a\n")
	fmt.Fprintf(&b, "freshly reset wallet loaded only by wallet-migrate; manual or mcp-created\n")
	fmt.Fprintf(&b, "records would inflate it.\n")
	return b.Bytes()
}

func writeAccountSection(b *bytes.Buffer, rep walletverify.Report) {
	fmt.Fprintf(b, "R2 — per-account count and sum(amount)\n")
	fmt.Fprintf(b, "%-18s %10s %10s %16s %16s  %s\n", "account", "exp n", "act n", "exp sum", "act sum", "status")
	for _, a := range rep.Accounts {
		fmt.Fprintf(b, "%-18s %10d %10d %16s %16s  %s\n",
			a.Account, a.ExpectedCount, a.ActualCount, a.ExpectedSum, a.ActualSum, a.Status)
	}
}

func writeCurrencySection(b *bytes.Buffer) {
	fmt.Fprintf(b, "\nR3 — main currency\n")
	fmt.Fprintf(b, "the Wallet REST API exposes no base/main-currency; it cannot be verified here.\n")
	fmt.Fprintf(b, "confirm the app's main-currency total reads EUR by eye (task 6a).\n")
}

func writeRateSection(b *bytes.Buffer, rep walletverify.Report) {
	fmt.Fprintf(b, "\nR3 — historical ref-rate diagnostic (Wallet computes this; it cannot be set)\n")
	for _, f := range rep.Rates {
		fmt.Fprintf(b, "%s: %d distinct ratio(s) across the sample -> %s\n", f.Currency, f.DistinctRatios, f.Verdict)
	}
	if len(rep.RateSample) > 0 {
		fmt.Fprintf(b, "%-8s %-12s %14s %14s %12s\n", "currency", "date", "amount", "converted", "ratio")
		for _, r := range rep.RateSample {
			fmt.Fprintf(b, "%-8s %-12s %14s %14s %12s\n",
				r.Currency, r.Date.Format("2006-01-02"), r.Amount, r.ConvertedEUR, r.Ratio)
		}
	}
}

func writeCategorySection(b *bytes.Buffer, rep walletverify.Report) {
	c := rep.Categories
	fmt.Fprintf(b, "\nR4 — category spot-check\n")
	fmt.Fprintf(b, "sampled %d  matched %d  changed %d  unmatched %d\n", c.Sampled, c.Matched, c.Changed, c.Unmatched)
	for _, ch := range c.Changes {
		fmt.Fprintf(b, "  %s %s %s: %q -> %q\n",
			ch.Account, ch.Date.Format("2006-01-02"), ch.Amount, ch.Archive, ch.Loaded)
	}
	if len(c.ManualPass) > 0 {
		fmt.Fprintf(b, "categories left for the manual pass (from _category_map.csv):\n")
		for _, name := range c.ManualPass {
			fmt.Fprintf(b, "  %s\n", name)
		}
	}
}

// summaryLine writes the one-line stdout result.
func summaryLine(w io.Writer, rep walletverify.Report, path string) error {
	verdict := "PASS"
	if !rep.OK {
		verdict = "MISMATCH"
	}
	_, err := fmt.Fprintf(w,
		"wallet-verify: %s — %d accounts checked; report at %s\n",
		verdict, len(rep.Accounts), path)
	if err != nil {
		return fmt.Errorf("writing summary: %w", err)
	}
	return nil
}
