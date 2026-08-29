package walletcsv

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

// ReadCategoryAlias parses a categories-alias.csv file into
// []walletload.CategoryAlias. The file has a header row
// `export_category,target[,note]`; the optional third column is ignored. A
// missing file is not an error (aliases are optional) — it returns nil. A parse
// failure or a row with fewer than two columns returns core.ErrBadAlias.
func ReadCategoryAlias(path string) ([]core.CategoryAlias, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is an operator-supplied CLI argument (--category-alias)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", core.ErrBadAlias, err)
	}

	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	var out []core.CategoryAlias
	line := 0
	for {
		rec, rerr := cr.Read()
		if errors.Is(rerr, io.EOF) {
			return out, nil
		}
		line++
		if rerr != nil {
			return nil, fmt.Errorf("%w: line %d: %w", core.ErrBadAlias, line, rerr)
		}
		alias, skip, aerr := parseAliasRow(rec, line == 1)
		if aerr != nil {
			return nil, fmt.Errorf("%w: line %d: %w", core.ErrBadAlias, line, aerr)
		}
		if !skip {
			out = append(out, alias)
		}
	}
}

func parseAliasRow(rec []string, isFirst bool) (core.CategoryAlias, bool, error) {
	if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
		return core.CategoryAlias{}, true, nil
	}
	if isFirst && len(rec) > 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "export_category") {
		return core.CategoryAlias{}, true, nil
	}
	if len(rec) < 2 {
		return core.CategoryAlias{}, false, errors.New("need export_category,target")
	}
	export := strings.TrimSpace(rec[0])
	target := strings.TrimSpace(rec[1])
	if export == "" || target == "" {
		return core.CategoryAlias{}, false, errors.New("empty export_category or target")
	}
	return core.CategoryAlias{ExportCategory: export, Target: target}, false, nil
}
