package walletload

import (
	"context"
	"fmt"
	"strings"
)

const (
	defaultBatchSize = 50
	maxBatchSize     = 50
	maxCreateRetries = 5
	deleteChunkSize  = 10
	sourceREST       = "rest"
)

// Service loads a Wallet export into Wallet through the ports in Deps.
type Service struct {
	deps        Deps
	progressOff bool
}

// New returns a Service backed by the given ports. A nil Deps.Progress is
// replaced with a no-op so call sites never need a nil check.
func New(d Deps) *Service {
	if d.Progress == nil {
		d.Progress = noopProgress{}
	}
	return &Service{deps: d}
}

type noopProgress struct{}

func (noopProgress) Report(ProgressEvent) error { return nil }

// report forwards ev to the Progress port. Progress is best-effort: the first
// Report error disables reporting for the rest of this Service's lifetime and
// is otherwise swallowed — it never affects the load.
func (s *Service) report(ev ProgressEvent) {
	if s.progressOff {
		return
	}
	if err := s.deps.Progress.Report(ev); err != nil {
		s.progressOff = true
	}
}

// Plan validates the export against the live catalogue and reports what a load
// would do, without writing anything.
func (s *Service) Plan(ctx context.Context, opts LoadOptions) (Plan, error) {
	an, err := s.analyseExport(ctx, normaliseOptions(opts))
	if err != nil {
		return Plan{}, err
	}
	return toPlan(an), nil
}

// Load runs the full load: create missing custom categories, reconcile any
// batch interrupted by an earlier crash, then create records per account in
// rate-limited batches. With opts.DryRun it stops after analysis.
func (s *Service) Load(ctx context.Context, opts LoadOptions) (Summary, error) {
	opts = normaliseOptions(opts)

	if !opts.DryRun {
		s.report(ProgressEvent{Kind: ProgressPhase, Phase: PhaseLoadingCatalogue})
	}

	an, err := s.analyseExport(ctx, opts)
	if err != nil {
		return Summary{}, err
	}
	if len(an.unmappedAccounts) > 0 {
		return Summary{}, fmt.Errorf("%w: %s", ErrUnmappedAccount, strings.Join(an.unmappedAccounts, ", "))
	}

	sum := newSummary()
	sum.RowsIn = an.rowsIn
	fillSkipSummary(sum, an)
	fillPlannedSummary(sum, an)

	if opts.DryRun {
		sum.CategoryMap = an.categoryMap
		countCategoryActions(sum)
		return *sum, nil
	}

	s.reportLoadStart(an, sum)
	if err := s.runLoad(ctx, an, opts, sum); err != nil {
		return *sum, err
	}
	return *sum, nil
}

// reportLoadStart emits the ProgressStart event: the count of records this run
// still has to send and how many committed rows it will skip (R6).
func (s *Service) reportLoadStart(an analysis, sum *Summary) {
	planned := 0
	for _, n := range sum.PerAccountPlanned {
		planned += n
	}
	already := 0
	for _, ar := range an.rows {
		if ar.skip == nil && ar.accountID != "" && s.deps.State.Loaded(ar.row.RowKey) {
			already++
		}
	}
	s.report(ProgressEvent{
		Kind:          ProgressStart,
		Total:         planned - already,
		AlreadyLoaded: already,
	})
}

func (s *Service) runLoad(ctx context.Context, an analysis, opts LoadOptions, sum *Summary) error {
	s.report(ProgressEvent{Kind: ProgressPhase, Phase: PhaseCreatingCategories})
	made, err := s.createCategories(ctx, an.toCreate, sum)
	if err != nil {
		return err
	}
	backfillCategories(&an, made)
	sum.CategoryMap = an.categoryMap
	countCategoryActions(sum)

	s.report(ProgressEvent{Kind: ProgressPhase, Phase: PhaseCreatingRecords})
	inputs := collectInputs(an)
	if err := s.reconcile(ctx, indexInputs(inputs), sum); err != nil {
		return err
	}
	return s.writeAll(ctx, inputs, accountNames(an), opts, sum)
}

// Rollback deletes every record this tool recorded in the resume store and
// warns about any rest-sourced record in Wallet that the store does not know
// about.
func (s *Service) Rollback(ctx context.Context) (Summary, error) {
	sum := newSummary()

	ids, err := s.deps.State.CreatedIDs()
	if err != nil {
		return *sum, fmt.Errorf("reading created records: %w", err)
	}
	if err := s.deleteAll(ctx, ids, sum); err != nil {
		return *sum, err
	}
	if err := s.findOrphans(ctx, ids, sum); err != nil {
		return *sum, err
	}
	return *sum, nil
}

func (s *Service) analyseExport(ctx context.Context, opts LoadOptions) (analysis, error) {
	rows, err := s.deps.Reader.Read(ctx)
	if err != nil {
		return analysis{}, fmt.Errorf("reading export: %w", err)
	}
	accts, err := s.deps.Catalog.Accounts(ctx)
	if err != nil {
		return analysis{}, fmt.Errorf("loading accounts: %w", err)
	}
	cats, err := s.deps.Catalog.Categories(ctx)
	if err != nil {
		return analysis{}, fmt.Errorf("loading categories: %w", err)
	}
	return s.analyse(rows, accts, cats, opts), nil
}

func normaliseOptions(opts LoadOptions) LoadOptions {
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.BatchSize > maxBatchSize {
		opts.BatchSize = maxBatchSize
	}
	if opts.TZOffset == "" {
		opts.TZOffset = "+00:00"
	}
	if opts.FallbackParent == "" {
		opts.FallbackParent = "Others"
	}
	return opts
}
