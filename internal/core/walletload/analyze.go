package walletload

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

const isoLocalLayout = "2006-01-02T15:04:05"

type analysedRow struct {
	row        ExportRow
	accountID  string
	accountCur string
	recordDate string
	categoryID string
	pendingCat string // lower export category name awaiting creation; "" otherwise
	skip       *SkippedRow
}

type analysis struct {
	rowsIn           int
	rows             []analysedRow
	toCreate         []PlannedCategory
	categoryMap      []CategoryMapping
	unmappedAccounts []string
}

func (s *Service) analyse(rows []ExportRow, accts []Account, cats []Category, opts LoadOptions) analysis {
	acctIdx := indexAccounts(accts)
	catIdx := newCategoryIndex(cats)

	accByRow, unmapped := resolveAccounts(rows, acctIdx, opts.AccountMap)
	catPlan := planCategories(rows, catIdx, opts)

	now := time.Now()
	res := analysis{
		rowsIn:           len(rows),
		toCreate:         catPlan.toCreate,
		categoryMap:      catPlan.mappings,
		unmappedAccounts: unmapped,
	}
	for _, r := range rows {
		res.rows = append(res.rows, classifyRow(r, accByRow[r.RowKey], catPlan, opts, now))
	}
	return res
}

func indexAccounts(accts []Account) map[string]Account {
	idx := make(map[string]Account, len(accts))
	for _, a := range accts {
		idx[strings.ToLower(a.Name)] = a
	}
	return idx
}

func resolveAccounts(rows []ExportRow, idx map[string]Account, m map[string]string) (map[string]Account, []string) {
	byRow := make(map[string]Account, len(rows))
	unmapped := map[string]struct{}{}
	for _, r := range rows {
		acc, ok := lookupAccount(r.Account, idx, m)
		if !ok {
			unmapped[r.Account] = struct{}{}
			continue
		}
		byRow[r.RowKey] = acc
	}
	return byRow, sortedKeys(unmapped)
}

func lookupAccount(name string, idx map[string]Account, m map[string]string) (Account, bool) {
	if id, ok := m[name]; ok {
		for _, a := range idx {
			if a.ID == id {
				return a, true
			}
		}
		return Account{ID: id, Name: name}, true
	}
	a, ok := idx[strings.ToLower(name)]
	return a, ok
}

type categoryIndex struct {
	byName       map[string]Category
	systemByName map[string]Category
}

func newCategoryIndex(cats []Category) categoryIndex {
	idx := categoryIndex{
		byName:       map[string]Category{},
		systemByName: map[string]Category{},
	}
	for _, c := range cats {
		ln := strings.ToLower(c.Name)
		if _, ok := idx.byName[ln]; !ok {
			idx.byName[ln] = c
		}
		if !c.Custom {
			if _, ok := idx.systemByName[ln]; !ok {
				idx.systemByName[ln] = c
			}
		}
	}
	return idx
}

type categoryResolution struct {
	id      string
	pending string
	action  CategoryAction
}

type categoryPlan struct {
	resolved map[string]categoryResolution
	toCreate []PlannedCategory
	mappings []CategoryMapping
}

func (p categoryPlan) lookup(exportCat string) categoryResolution {
	return p.resolved[strings.ToLower(exportCat)]
}

type distinctCategory struct {
	name   string
	custom bool
}

func distinctCategories(rows []ExportRow) []distinctCategory {
	seen := map[string]bool{}
	var out []distinctCategory
	for _, r := range rows {
		ln := strings.ToLower(r.Category)
		if r.Category == "" || seen[ln] {
			continue
		}
		seen[ln] = true
		out = append(out, distinctCategory{name: r.Category, custom: r.CustomCategory})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func planCategories(rows []ExportRow, idx categoryIndex, opts LoadOptions) categoryPlan {
	plan := categoryPlan{resolved: map[string]categoryResolution{}}
	parent, hasParent := idx.systemByName[strings.ToLower(opts.FallbackParent)]

	for _, dc := range distinctCategories(rows) {
		res := resolveOneCategory(dc, idx, opts, hasParent)
		plan.resolved[strings.ToLower(dc.name)] = res
		if res.action == CategoryFallbackParent {
			plan.toCreate = append(plan.toCreate, PlannedCategory{Name: dc.name, ParentID: parent.ID})
		}
		plan.mappings = append(plan.mappings, CategoryMapping{
			ExportCategory: dc.name,
			Action:         res.action,
			ResolvedID:     res.id,
		})
	}
	return plan
}

func resolveOneCategory(
	dc distinctCategory, idx categoryIndex, opts LoadOptions, hasParent bool,
) categoryResolution {
	if c, ok := idx.byName[strings.ToLower(dc.name)]; ok {
		return categoryResolution{id: c.ID, action: CategoryResolved}
	}
	if dc.custom && hasParent {
		return categoryResolution{pending: strings.ToLower(dc.name), action: CategoryFallbackParent}
	}
	return categoryResolution{id: opts.FallbackCategoryID, action: CategoryNone}
}

func classifyRow(r ExportRow, acc Account, cp categoryPlan, opts LoadOptions, now time.Time) analysedRow {
	ar := analysedRow{
		row:        r,
		accountID:  acc.ID,
		accountCur: acc.CurrencyCode,
		recordDate: r.Date.Format(isoLocalLayout) + opts.TZOffset,
	}
	if acc.ID == "" {
		return ar
	}
	if acc.CurrencyCode != "" && !strings.EqualFold(r.Currency, acc.CurrencyCode) {
		ar.skip = skipRow(r, SkipForeignCurrency)
		return ar
	}
	if isZeroAmount(r.Amount) {
		ar.skip = skipRow(r, SkipZeroAmount)
		return ar
	}
	if tooFarInFuture(ar.recordDate, now) {
		ar.skip = skipRow(r, SkipFutureDate)
		return ar
	}
	res := cp.lookup(r.Category)
	ar.categoryID = res.id
	ar.pendingCat = res.pending
	return ar
}

func skipRow(r ExportRow, reason SkipReason) *SkippedRow {
	return &SkippedRow{RowKey: r.RowKey, Reason: reason, Row: r}
}

func isZeroAmount(s string) bool {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil && v == 0
}

func tooFarInFuture(iso string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return false
	}
	return t.After(now.Add(24 * time.Hour))
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
