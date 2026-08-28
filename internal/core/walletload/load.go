package walletload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --- category creation -------------------------------------------------------

func (s *Service) createCategories(
	ctx context.Context, toCreate []PlannedCategory, sum *Summary,
) (map[string]string, error) {
	made := make(map[string]string, len(toCreate))
	for _, c := range toCreate {
		sum.Requests++
		cat, err := s.deps.Catalog.CreateCustomCategory(ctx, c.Name, c.ParentID)
		if err != nil {
			return nil, fmt.Errorf("creating custom category %q: %w", c.Name, err)
		}
		made[strings.ToLower(c.Name)] = cat.ID
	}
	return made, nil
}

func backfillCategories(an *analysis, made map[string]string) {
	for i := range an.rows {
		if id, ok := made[an.rows[i].pendingCat]; ok {
			an.rows[i].categoryID = id
			an.rows[i].pendingCat = ""
		}
	}
	for i := range an.categoryMap {
		if id, ok := made[strings.ToLower(an.categoryMap[i].ExportCategory)]; ok {
			an.categoryMap[i].Action = CategoryCreated
			an.categoryMap[i].ResolvedID = id
		}
	}
}

// --- record inputs ----------------------------------------------------------

func collectInputs(an analysis) []RecordInput {
	var out []RecordInput
	for _, ar := range an.rows {
		if ar.skip != nil || ar.accountID == "" {
			continue
		}
		out = append(out, RecordInput{
			RowKey:       ar.row.RowKey,
			AccountID:    ar.accountID,
			Amount:       ar.row.Amount,
			CurrencyCode: ar.accountCur,
			RecordDate:   ar.recordDate,
			CategoryID:   ar.categoryID,
			CounterParty: ar.row.Payee,
			Note:         ar.row.Note,
		})
	}
	return out
}

func indexInputs(in []RecordInput) map[string]RecordInput {
	m := make(map[string]RecordInput, len(in))
	for _, i := range in {
		m[i.RowKey] = i
	}
	return m
}

// --- reconciliation of an interrupted batch --------------------------------

func (s *Service) reconcile(ctx context.Context, byKey map[string]RecordInput, sum *Summary) error {
	batches, err := s.deps.Journal.InFlight()
	if err != nil {
		return fmt.Errorf("reading in-flight journal: %w", err)
	}
	for _, b := range batches {
		if err := s.reconcileBatch(ctx, b, byKey, sum); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileBatch(
	ctx context.Context, b Batch, byKey map[string]RecordInput, sum *Summary,
) error {
	sum.Requests++
	remote, err := s.deps.Records.FindRecords(ctx, RecordQuery{
		AccountID: b.AccountID,
		Source:    sourceREST,
		FromDate:  b.MinDate,
		ToDate:    b.MaxDate,
	})
	if err != nil {
		return fmt.Errorf("finding records to reconcile: %w", err)
	}
	for _, rk := range b.RowKeys {
		if err := s.reconcileRow(rk, byKey, remote); err != nil {
			return err
		}
	}
	if err := s.deps.Journal.ClearInFlight(b.ID); err != nil {
		return fmt.Errorf("clearing reconciled batch: %w", err)
	}
	return nil
}

func (s *Service) reconcileRow(rk string, byKey map[string]RecordInput, remote []RemoteRecord) error {
	if s.deps.State.Loaded(rk) {
		return nil
	}
	in, ok := byKey[rk]
	if !ok {
		return nil
	}
	match, ok := matchRemote(in, remote)
	if !ok {
		return nil
	}
	if err := s.deps.State.Commit(rk, match.ID); err != nil {
		return fmt.Errorf("committing reconciled record: %w", err)
	}
	return nil
}

func matchRemote(in RecordInput, remote []RemoteRecord) (RemoteRecord, bool) {
	for _, r := range remote {
		if sameInstant(r.RecordDate, in.RecordDate) && r.Note == in.Note && amountsEqual(r.Amount, in.Amount) {
			return r, true
		}
	}
	return RemoteRecord{}, false
}

// sameInstant compares two RFC3339 timestamps by the moment they name (the API
// echoes recordDate in UTC "Z" form; the core emits it with a zone offset).
func sameInstant(a, b string) bool {
	ta, aerr := time.Parse(time.RFC3339, a)
	tb, berr := time.Parse(time.RFC3339, b)
	if aerr != nil || berr != nil {
		return a == b
	}
	return ta.Equal(tb)
}

func amountsEqual(a, b string) bool {
	av, aerr := strconv.ParseFloat(strings.TrimSpace(a), 64)
	bv, berr := strconv.ParseFloat(strings.TrimSpace(b), 64)
	return aerr == nil && berr == nil && av == bv
}

// --- batched record creation ----------------------------------------------

func (s *Service) writeAll(ctx context.Context, inputs []RecordInput, opts LoadOptions, sum *Summary) error {
	created := 0
	for _, accountID := range accountOrder(inputs) {
		pending := s.unloaded(inputs, accountID)
		for _, chunk := range chunkSlice(pending, opts.BatchSize) {
			if opts.Limit > 0 && created >= opts.Limit {
				return nil
			}
			chunk = trimToLimit(chunk, opts.Limit, created)
			n, err := s.writeChunk(ctx, accountID, chunk, sum)
			if err != nil {
				return err
			}
			created += n
		}
	}
	return nil
}

func (s *Service) unloaded(inputs []RecordInput, accountID string) []RecordInput {
	var out []RecordInput
	for _, in := range inputs {
		if in.AccountID == accountID && !s.deps.State.Loaded(in.RowKey) {
			out = append(out, in)
		}
	}
	return out
}

func (s *Service) writeChunk(
	ctx context.Context, accountID string, chunk []RecordInput, sum *Summary,
) (int, error) {
	if len(chunk) == 0 {
		return 0, nil
	}
	batch := newBatch(accountID, chunk)
	if err := s.deps.Journal.MarkInFlight(batch); err != nil {
		return 0, fmt.Errorf("marking batch in flight: %w", err)
	}
	results, err := s.createWithRetry(ctx, chunk, sum)
	if err != nil {
		return 0, err
	}
	ok := 0
	for _, r := range results {
		if r.OK {
			if cerr := s.deps.State.Commit(r.RowKey, r.RecordID); cerr != nil {
				return ok, fmt.Errorf("committing created record: %w", cerr)
			}
			sum.PerAccountCreated[accountID]++
			ok++
			continue
		}
		sum.Failures = append(sum.Failures, r)
	}
	if err := s.deps.Journal.ClearInFlight(batch.ID); err != nil {
		return ok, fmt.Errorf("clearing batch: %w", err)
	}
	return ok, nil
}

func (s *Service) createWithRetry(
	ctx context.Context, chunk []RecordInput, sum *Summary,
) ([]RecordResult, error) {
	for attempt := 0; attempt < maxCreateRetries; attempt++ {
		sum.Requests++
		results, err := s.deps.Records.CreateRecords(ctx, chunk)
		if err == nil {
			return results, nil
		}
		var rl *ErrRateLimited
		if !errors.As(err, &rl) {
			return nil, fmt.Errorf("creating records: %w", err)
		}
		sum.RateLimitHits++
		sum.Retries++
		if werr := s.deps.Waiter.Wait(ctx, rl.RetryAfter); werr != nil {
			return nil, fmt.Errorf("waiting out rate limit: %w", werr)
		}
	}
	return nil, ErrRetriesExhausted
}

// --- rollback -------------------------------------------------------------

func (s *Service) deleteAll(ctx context.Context, ids []string, sum *Summary) error {
	for _, chunk := range chunkSlice(ids, deleteChunkSize) {
		sum.Requests++
		results, err := s.deps.Records.DeleteRecords(ctx, chunk)
		if err != nil {
			return fmt.Errorf("deleting records: %w", err)
		}
		for _, r := range results {
			if r.OK {
				sum.Deleted++
				continue
			}
			sum.Failures = append(sum.Failures, r)
		}
	}
	return nil
}

func (s *Service) findOrphans(ctx context.Context, known []string, sum *Summary) error {
	sum.Requests++
	remote, err := s.deps.Records.FindRecords(ctx, RecordQuery{Source: sourceREST})
	if err != nil {
		return fmt.Errorf("finding rest-sourced records: %w", err)
	}
	knownSet := make(map[string]bool, len(known))
	for _, id := range known {
		knownSet[id] = true
	}
	for _, r := range remote {
		if !knownSet[r.ID] {
			sum.Orphans = append(sum.Orphans, r.ID)
		}
	}
	sort.Strings(sum.Orphans)
	return nil
}

// --- chunking & batches --------------------------------------------------

func accountOrder(inputs []RecordInput) []string {
	seen := map[string]bool{}
	var out []string
	for _, in := range inputs {
		if !seen[in.AccountID] {
			seen[in.AccountID] = true
			out = append(out, in.AccountID)
		}
	}
	sort.Strings(out)
	return out
}

func chunkSlice[T any](in []T, size int) [][]T {
	var out [][]T
	for i := 0; i < len(in); i += size {
		end := i + size
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[i:end])
	}
	return out
}

func trimToLimit(chunk []RecordInput, limit, created int) []RecordInput {
	if limit <= 0 {
		return chunk
	}
	room := limit - created
	if room < len(chunk) {
		return chunk[:room]
	}
	return chunk
}

func newBatch(accountID string, chunk []RecordInput) Batch {
	keys := make([]string, len(chunk))
	minD, maxD := chunk[0].RecordDate, chunk[0].RecordDate
	var payload strings.Builder
	for i, in := range chunk {
		keys[i] = in.RowKey
		if in.RecordDate < minD {
			minD = in.RecordDate
		}
		if in.RecordDate > maxD {
			maxD = in.RecordDate
		}
		payload.WriteString(in.RowKey + "|" + in.Amount + "|" + in.RecordDate + "\n")
	}
	hash := sha256.Sum256([]byte(payload.String()))
	return Batch{
		ID:          accountID + ":" + keys[0],
		AccountID:   accountID,
		RowKeys:     keys,
		PayloadHash: hex.EncodeToString(hash[:]),
		MinDate:     minD,
		MaxDate:     maxD,
	}
}

// --- reporting -----------------------------------------------------------

func toPlan(an analysis) Plan {
	p := Plan{
		Rows:               an.rowsIn,
		PerAccountToSend:   map[string]int{},
		CategoriesToCreate: an.toCreate,
		CategoryMap:        an.categoryMap,
		UnmappedAccounts:   an.unmappedAccounts,
	}
	for _, ar := range an.rows {
		if ar.skip != nil {
			p.Skipped = append(p.Skipped, *ar.skip)
			continue
		}
		if ar.accountID != "" {
			p.PerAccountToSend[ar.row.Account]++
		}
	}
	return p
}

func fillSkipSummary(sum *Summary, an analysis) {
	for _, ar := range an.rows {
		if ar.skip != nil {
			sum.Skipped[ar.skip.Reason]++
			sum.SkippedRows = append(sum.SkippedRows, *ar.skip)
		}
	}
}

func countCategoryActions(sum *Summary) {
	for _, m := range sum.CategoryMap {
		switch m.Action {
		case CategoryResolved:
			sum.CategoriesResolved++
		case CategoryCreated:
			sum.CategoriesCreated++
		case CategoryFallbackParent:
			sum.CategoriesFallback++
		case CategoryNone:
			sum.CategoriesNone++
		}
	}
}
