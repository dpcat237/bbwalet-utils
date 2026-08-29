package walletload

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const isoLocalLayout = "2006-01-02T15:04:05"

type analysedRow struct {
	row            ExportRow
	accountID      string
	accountCur     string
	pendingAccount string // lower export account name awaiting creation; "" otherwise
	recordDate     string
	categoryID     string
	pendingCat     string // lower export category name awaiting creation; "" otherwise
	skip           *SkippedRow
}

type analysis struct {
	rowsIn           int
	rows             []analysedRow
	toCreate         []PlannedCategory
	toCreateAccounts []PlannedAccount
	categoryMap      []CategoryMapping
	catCanonical     map[string]string // lower export name -> lower canonical create name
	accountMap       []AccountMapping
	unmappedAccounts []string
	unresolvedCats   []string
	problems         []error // ambiguous currency / bad alias / missing parent
}

func (s *Service) analyse(rows []ExportRow, accts []Account, cats []Category, opts LoadOptions) analysis {
	acctIdx := indexAccounts(accts)
	catIdx := newCategoryIndex(cats)
	aliasIdx := newCategoryAliasIndex(opts.CategoryAliases)

	accPlan, accProblems := planAccounts(rows, acctIdx, opts)
	catPlan, catProblems := planCategories(rows, catIdx, aliasIdx, opts)

	now := time.Now()
	res := analysis{
		rowsIn:           len(rows),
		toCreate:         catPlan.toCreate,
		toCreateAccounts: accPlan.toCreate,
		categoryMap:      catPlan.mappings,
		catCanonical:     catPlan.canonical,
		accountMap:       accPlan.mappings,
		unmappedAccounts: accPlan.unmapped,
		problems:         append(accProblems, catProblems...),
	}
	for _, r := range rows {
		res.rows = append(res.rows, classifyRow(r, accPlan.byRow[r.RowKey], catPlan, opts, now))
	}
	for _, m := range res.categoryMap {
		if m.Action == CategoryNone {
			res.unresolvedCats = append(res.unresolvedCats, m.ExportCategory)
		}
	}
	return res
}

// --- accounts -------------------------------------------------------------

func indexAccounts(accts []Account) map[string]Account {
	idx := make(map[string]Account, len(accts))
	for _, a := range accts {
		idx[strings.ToLower(a.Name)] = a
	}
	return idx
}

type accountResolution struct {
	id      string
	pending string // lower export account name awaiting creation; "" otherwise
	curr    string // currency code (known even while pending)
	action  AccountAction
}

type accountPlan struct {
	byRow    map[string]accountResolution
	toCreate []PlannedAccount
	mappings []AccountMapping
	unmapped []string
}

func distinctAccounts(rows []ExportRow) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		ln := strings.ToLower(r.Account)
		if r.Account == "" || seen[ln] {
			continue
		}
		seen[ln] = true
		out = append(out, r.Account)
	}
	sort.Strings(out)
	return out
}

func planAccounts(rows []ExportRow, idx map[string]Account, opts LoadOptions) (accountPlan, []error) {
	plan := accountPlan{byRow: map[string]accountResolution{}}
	resolved := map[string]accountResolution{}
	unmapped := map[string]struct{}{}
	var problems []error

	for _, name := range distinctAccounts(rows) {
		res, planned, err := resolveOneAccount(name, rows, idx, opts)
		switch {
		case err != nil:
			problems = append(problems, err)
		case planned != nil:
			plan.toCreate = append(plan.toCreate, *planned)
		case res.id == "" && res.pending == "":
			unmapped[name] = struct{}{}
		}
		resolved[strings.ToLower(name)] = res
		plan.mappings = append(plan.mappings, AccountMapping{
			ExportAccount: name, Action: res.action, ResolvedID: res.id,
		})
	}
	for _, r := range rows {
		plan.byRow[r.RowKey] = resolved[strings.ToLower(r.Account)]
	}
	plan.unmapped = sortedKeys(unmapped)
	return plan, problems
}

func resolveOneAccount(
	name string, rows []ExportRow, idx map[string]Account, opts LoadOptions,
) (accountResolution, *PlannedAccount, error) {
	if id, ok := opts.AccountMap[name]; ok {
		for _, a := range idx {
			if a.ID == id {
				return accountResolution{id: a.ID, curr: a.CurrencyCode, action: AccountResolved}, nil, nil
			}
		}
		return accountResolution{id: id, action: AccountResolved}, nil, nil
	}
	if a, ok := idx[strings.ToLower(name)]; ok {
		return accountResolution{id: a.ID, curr: a.CurrencyCode, action: AccountResolved}, nil, nil
	}
	if !opts.CreateMissing {
		return accountResolution{}, nil, nil
	}

	cur := opts.AccountCurrency[name]
	if cur == "" {
		inferred, ok := inferAccountCurrency(rows, name)
		if !ok {
			return accountResolution{}, nil, fmt.Errorf("%w: %q", ErrAmbiguousAccountCurrency, name)
		}
		cur = inferred
	}
	bal := opts.AccountInitialBalance[name]
	if bal == "" {
		bal = "0"
	}
	planned := &PlannedAccount{Name: name, CurrencyCode: cur, AccountType: opts.AccountType, InitialBalance: bal}
	return accountResolution{pending: strings.ToLower(name), curr: cur, action: AccountCreated}, planned, nil
}

// inferAccountCurrency picks the currency for an account to create: the most
// common row currency, breaking a tie on the account name's trailing token when
// it is one of the tied currencies. Returns false when it cannot decide.
func inferAccountCurrency(rows []ExportRow, name string) (string, bool) {
	counts := map[string]int{}
	for _, r := range rows {
		if r.Account == name && r.Currency != "" {
			counts[r.Currency]++
		}
	}
	if len(counts) == 0 {
		return "", false
	}
	best, tie := topCount(counts)
	if !tie {
		return best, true
	}
	if suffix := nameSuffix(name); counts[suffix] > 0 {
		return suffix, true
	}
	return "", false
}

// topCount returns the highest-count key and whether the top count is tied.
func topCount(counts map[string]int) (string, bool) {
	best, bestN, tie := "", 0, false
	for k, n := range counts {
		switch {
		case n > bestN:
			best, bestN, tie = k, n, false
		case n == bestN:
			tie = true
		}
	}
	return best, tie
}

func nameSuffix(name string) string {
	f := strings.Fields(name)
	if len(f) == 0 {
		return ""
	}
	return strings.ToUpper(f[len(f)-1])
}

// --- categories ----------------------------------------------------------

type categoryIndex struct {
	byName       map[string]Category
	systemByName map[string]Category
	byNormalised map[string][]Category
}

func newCategoryIndex(cats []Category) categoryIndex {
	idx := categoryIndex{
		byName:       map[string]Category{},
		systemByName: map[string]Category{},
		byNormalised: map[string][]Category{},
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
		nn := normaliseCategory(c.Name)
		idx.byNormalised[nn] = append(idx.byNormalised[nn], c)
	}
	return idx
}

func newCategoryAliasIndex(aliases []CategoryAlias) map[string]CategoryAlias {
	idx := make(map[string]CategoryAlias, len(aliases))
	for _, a := range aliases {
		idx[strings.ToLower(strings.TrimSpace(a.ExportCategory))] = a
	}
	return idx
}

// normaliseCategory folds an export or live category name to a comparison key:
// lowercase, drop a trailing " (Group)" disambiguator, unify the separators
// "&" / "," / "-" / " and ", de-pluralise long tokens, and sort the tokens so
// word order does not matter.
func normaliseCategory(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = stripGroupSuffix(s)
	s = strings.NewReplacer("&", " ", ",", " ", "-", " ", " and ", " ").Replace(s)
	toks := strings.Fields(s)
	for i, t := range toks {
		if len(t) > 4 && strings.HasSuffix(t, "s") {
			toks[i] = strings.TrimSuffix(t, "s")
		}
	}
	sort.Strings(toks)
	return strings.Join(toks, " ")
}

func stripGroupSuffix(s string) string {
	s = strings.TrimRight(s, " ")
	if !strings.HasSuffix(s, ")") {
		return s
	}
	if i := strings.LastIndex(s, "("); i > 0 {
		return strings.TrimRight(s[:i], " ")
	}
	return s
}

type categoryResolution struct {
	id         string
	pending    string // lower canonical name awaiting creation; "" otherwise
	createName string // original-case canonical name to create; "" otherwise
	parentID   string
	action     CategoryAction
}

type categoryPlan struct {
	resolved  map[string]categoryResolution
	toCreate  []PlannedCategory
	mappings  []CategoryMapping
	canonical map[string]string // lower export name -> lower canonical create name (create resolutions only)
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

func planCategories(
	rows []ExportRow, idx categoryIndex, aliasIdx map[string]CategoryAlias, opts LoadOptions,
) (categoryPlan, []error) {
	plan := categoryPlan{resolved: map[string]categoryResolution{}, canonical: map[string]string{}}
	var problems []error
	plannedSeen := map[string]bool{}

	for _, dc := range distinctCategories(rows) {
		res, err := resolveOneCategory(dc, idx, aliasIdx, opts)
		if err != nil {
			problems = append(problems, err)
		}
		plan.resolved[strings.ToLower(dc.name)] = res
		if res.action == CategoryFallbackParent {
			key := strings.ToLower(res.createName)
			plan.canonical[strings.ToLower(dc.name)] = key
			if !plannedSeen[key] {
				plannedSeen[key] = true
				plan.toCreate = append(plan.toCreate, PlannedCategory{Name: res.createName, ParentID: res.parentID})
			}
		}
		plan.mappings = append(plan.mappings, CategoryMapping{
			ExportCategory: dc.name, Action: res.action, ResolvedID: res.id,
		})
	}
	return plan, problems
}

func resolveOneCategory(
	dc distinctCategory, idx categoryIndex, aliasIdx map[string]CategoryAlias, opts LoadOptions,
) (categoryResolution, error) {
	if a, ok := aliasIdx[strings.ToLower(dc.name)]; ok {
		return resolveAlias(dc.name, a.Target, idx, aliasIdx, map[string]bool{strings.ToLower(dc.name): true})
	}
	if c, ok := idx.byName[strings.ToLower(dc.name)]; ok {
		return categoryResolution{id: c.ID, action: CategoryResolved}, nil
	}
	if m := idx.byNormalised[normaliseCategory(dc.name)]; len(m) == 1 {
		return categoryResolution{id: m[0].ID, action: CategoryResolvedNormalised}, nil
	}
	if dc.custom {
		p, ok := idx.systemByName[strings.ToLower(opts.FallbackParent)]
		if !ok {
			return categoryResolution{id: opts.FallbackCategoryID, action: CategoryNone},
				fmt.Errorf("%w: fallback parent %q", ErrMissingParent, opts.FallbackParent)
		}
		return categoryResolution{
			pending: strings.ToLower(dc.name), createName: dc.name,
			parentID: p.ID, action: CategoryFallbackParent,
		}, nil
	}
	return categoryResolution{id: opts.FallbackCategoryID, action: CategoryNone}, nil
}

// resolveAlias resolves an alias target: an existing live category, a
// "create:<Parent>" directive, or the name of another alias entry (a chain,
// e.g. `Vet -> Veterinary -> create:Pets & animals`). `seen` guards cycles.
func resolveAlias(
	name, target string, idx categoryIndex, aliasIdx map[string]CategoryAlias, seen map[string]bool,
) (categoryResolution, error) {
	target = strings.TrimSpace(target)
	if parent, ok := strings.CutPrefix(target, CreateCategoryPrefix); ok {
		p, has := idx.systemByName[strings.ToLower(strings.TrimSpace(parent))]
		if !has {
			return categoryResolution{action: CategoryNone},
				fmt.Errorf("%w: %q -> parent %q", ErrMissingParent, name, strings.TrimSpace(parent))
		}
		return categoryResolution{
			pending: strings.ToLower(name), createName: name,
			parentID: p.ID, action: CategoryFallbackParent,
		}, nil
	}
	if next, ok := aliasIdx[strings.ToLower(target)]; ok {
		if seen[strings.ToLower(target)] {
			return categoryResolution{action: CategoryNone},
				fmt.Errorf("%w: alias cycle at %q", ErrBadAlias, target)
		}
		seen[strings.ToLower(target)] = true
		return resolveAlias(target, next.Target, idx, aliasIdx, seen)
	}
	if c, ok := idx.byName[strings.ToLower(target)]; ok {
		return categoryResolution{id: c.ID, action: CategoryResolvedAlias}, nil
	}
	return categoryResolution{action: CategoryNone}, fmt.Errorf("%w: %q -> %q", ErrBadAlias, name, target)
}

// --- row classification --------------------------------------------------

func classifyRow(r ExportRow, ares accountResolution, cp categoryPlan, opts LoadOptions, now time.Time) analysedRow {
	ar := analysedRow{
		row:            r,
		accountID:      ares.id,
		accountCur:     ares.curr,
		pendingAccount: ares.pending,
		recordDate:     r.Date.Format(isoLocalLayout) + opts.TZOffset,
	}
	if ares.id == "" && ares.pending == "" {
		return ar
	}
	if ares.curr != "" && !strings.EqualFold(r.Currency, ares.curr) {
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
