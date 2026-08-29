package walletmigrate

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

const (
	summaryFile     = "_load_summary.txt"
	categoryMapFile = "_category_map.csv"
	reviewFile      = "_review.csv"
	outFilePerm     = 0o600
	outDirPerm      = 0o750
)

// writeReports writes the three human-facing output files for a load or
// rollback. The created-records file is owned by the resume store.
func writeReports(outDir string, sum walletload.Summary) error {
	if err := os.MkdirAll(outDir, outDirPerm); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	catCSV, err := toCSV(categoryMapRows(sum))
	if err != nil {
		return err
	}
	reviewCSVData, err := toCSV(reviewRows(sum))
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		summaryFile:     summaryText(sum),
		categoryMapFile: catCSV,
		reviewFile:      reviewCSVData,
	} {
		if err := writeFile(filepath.Join(outDir, name), data); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, outFilePerm); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}

func toCSV(rows [][]string) ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	if err := w.WriteAll(rows); err != nil {
		return nil, fmt.Errorf("encoding csv: %w", err)
	}
	return b.Bytes(), nil
}

func categoryMapRows(sum walletload.Summary) [][]string {
	rows := [][]string{{"export_category", "action", "resolved_id"}}
	for _, m := range sum.CategoryMap {
		rows = append(rows, []string{m.ExportCategory, string(m.Action), m.ResolvedID})
	}
	return rows
}

func reviewRows(sum walletload.Summary) [][]string {
	rows := [][]string{{"row_key", "reason", "account", "category", "currency", "amount", "date"}}
	for _, s := range sum.SkippedRows {
		rows = append(rows, []string{
			s.RowKey, string(s.Reason), s.Row.Account, s.Row.Category,
			s.Row.Currency, s.Row.Amount, s.Row.Date.Format("2006-01-02 15:04:05"),
		})
	}
	return rows
}

func summaryText(sum walletload.Summary) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "rows in:             %d\n", sum.RowsIn)
	if len(sum.AccountMap) > 0 {
		fmt.Fprintf(&b, "accounts resolved:   %d\n", sum.AccountsResolved)
		fmt.Fprintf(&b, "accounts created:    %d\n", sum.AccountsCreated)
		for _, m := range sum.AccountMap {
			fmt.Fprintf(&b, "  %-26s %s\n", m.ExportAccount, m.Action)
		}
	}
	fmt.Fprintf(&b, "records created:     %d\n", totalCreated(sum))
	for _, acc := range sortedIntKeys(sum.PerAccountCreated) {
		fmt.Fprintf(&b, "  %-26s %d\n", acc, sum.PerAccountCreated[acc])
	}
	fmt.Fprintf(&b, "rows skipped:        %d\n", len(sum.SkippedRows))
	for _, reason := range sortedReasonKeys(sum.Skipped) {
		fmt.Fprintf(&b, "  %-26s %d\n", reason, sum.Skipped[reason])
	}
	fmt.Fprintf(&b, "categories resolved: %d\n", sum.CategoriesResolved)
	fmt.Fprintf(&b, "categories created:  %d\n", sum.CategoriesCreated)
	fmt.Fprintf(&b, "categories fallback: %d\n", sum.CategoriesFallback)
	fmt.Fprintf(&b, "categories none:     %d\n", sum.CategoriesNone)
	fmt.Fprintf(&b, "requests made:       %d\n", sum.Requests)
	fmt.Fprintf(&b, "retries:             %d\n", sum.Retries)
	fmt.Fprintf(&b, "rate-limit hits:     %d\n", sum.RateLimitHits)
	if sum.Deleted > 0 || len(sum.Orphans) > 0 {
		fmt.Fprintf(&b, "records deleted:     %d\n", sum.Deleted)
		fmt.Fprintf(&b, "orphan rest records: %d\n", len(sum.Orphans))
		for _, id := range sum.Orphans {
			fmt.Fprintf(&b, "  %s\n", id)
		}
	}
	if len(sum.Failures) > 0 {
		fmt.Fprintf(&b, "failures:            %d\n", len(sum.Failures))
		for _, f := range sum.Failures {
			fmt.Fprintf(&b, "  %s: %s\n", f.RowKey, f.Err)
		}
	}
	return b.Bytes()
}

func totalCreated(sum walletload.Summary) int {
	n := 0
	for _, v := range sum.PerAccountCreated {
		n += v
	}
	return n
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedReasonKeys(m map[walletload.SkipReason]int) []walletload.SkipReason {
	out := make([]walletload.SkipReason, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
