// Package wallethttp is the outbound adapter for the BudgetBakers Wallet REST
// API. It implements the core walletload.Catalog and walletload.Records ports
// with the standard library, translates HTTP failures into core sentinels at
// the boundary, and never lets the bearer token leak into an error or log.
package wallethttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

const (
	pageLimit    = 200
	maxBodyBytes = 1 << 20
	epochStart   = "1970-01-01T00:00:00Z"

	// maxGetRetries bounds automatic 429 retries for idempotent calls
	// (catalogue GETs, deletes). Record creation is retried by the core so its
	// Summary can account for it.
	maxGetRetries = 3
	// pacingThreshold: when the server reports this many requests or fewer left
	// in the window, slow down pre-emptively instead of racing into a 429.
	pacingThreshold = 3
	pacingPause     = time.Second
)

// Client talks to one Wallet API base URL with one bearer token.
type Client struct {
	http    *http.Client
	baseURL string
	token   string
}

// New returns a Client. hc must be non-nil (set its Timeout from config).
func New(hc *http.Client, baseURL, token string) *Client {
	return &Client{http: hc, baseURL: strings.TrimRight(baseURL, "/"), token: token}
}

// --- ports: Catalog -------------------------------------------------------

type accountDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CurrencyCode string `json:"currencyCode"`
}

type categoryDTO struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SystemID       string `json:"systemId"`
	CustomCategory bool   `json:"customCategory"`
	ParentID       string `json:"parentId"`
	Group          struct {
		Name string `json:"name"`
	} `json:"group"`
}

// Accounts lists every account, following pagination.
func (c *Client) Accounts(ctx context.Context) ([]core.Account, error) {
	var out []core.Account
	err := c.paginate(ctx, "/v1/api/accounts", url.Values{}, func(page json.RawMessage) (int, error) {
		var body struct {
			Accounts   []accountDTO `json:"accounts"`
			NextOffset *int         `json:"nextOffset"`
		}
		if err := json.Unmarshal(page, &body); err != nil {
			return 0, fmt.Errorf("decoding accounts page: %w", err)
		}
		for _, a := range body.Accounts {
			out = append(out, core.Account{ID: a.ID, Name: a.Name, CurrencyCode: a.CurrencyCode})
		}
		return offsetOrDone(body.NextOffset), nil
	})
	return out, err
}

// Categories lists every category (system and custom), following pagination.
func (c *Client) Categories(ctx context.Context) ([]core.Category, error) {
	var out []core.Category
	err := c.paginate(ctx, "/v1/api/categories", url.Values{}, func(page json.RawMessage) (int, error) {
		var body struct {
			Categories []categoryDTO `json:"categories"`
			NextOffset *int          `json:"nextOffset"`
		}
		if err := json.Unmarshal(page, &body); err != nil {
			return 0, fmt.Errorf("decoding categories page: %w", err)
		}
		for _, cd := range body.Categories {
			out = append(out, core.Category{
				ID: cd.ID, Name: cd.Name, GroupName: cd.Group.Name,
				SystemID: cd.SystemID, Custom: cd.CustomCategory,
			})
		}
		return offsetOrDone(body.NextOffset), nil
	})
	return out, err
}

// CreateCustomCategory creates one custom subcategory under a system parent.
func (c *Client) CreateCustomCategory(ctx context.Context, name, parentID string) (core.Category, error) {
	reqBody := map[string]string{"name": name, "parentId": parentID}
	var body struct {
		Category categoryDTO `json:"category"`
	}
	if err := c.send(ctx, http.MethodPost, "/v1/api/categories/custom", nil, reqBody, &body); err != nil {
		return core.Category{}, err
	}
	cd := body.Category
	return core.Category{
		ID: cd.ID, Name: cd.Name, GroupName: cd.Group.Name,
		SystemID: cd.SystemID, Custom: cd.CustomCategory,
	}, nil
}

// --- ports: Records ------------------------------------------------------

type amountDTO struct {
	Value        json.Number `json:"value"`
	CurrencyCode string      `json:"currencyCode,omitempty"`
}

type createRecordDTO struct {
	AccountID    string    `json:"accountId"`
	Amount       amountDTO `json:"amount"`
	RecordDate   string    `json:"recordDate"`
	CategoryID   string    `json:"categoryId,omitempty"`
	CounterParty string    `json:"counterParty,omitempty"`
	Note         string    `json:"note,omitempty"`
}

type batchResult struct {
	InputIndex int    `json:"inputIndex"`
	ID         string `json:"id"`
	Success    bool   `json:"success"`
	Error      string `json:"error"`
}

// CreateRecords creates a batch of records. Per-item failures ride in the
// results; only a whole-request failure returns an error.
func (c *Client) CreateRecords(ctx context.Context, in []core.RecordInput) ([]core.RecordResult, error) {
	payload := make([]createRecordDTO, len(in))
	for i, r := range in {
		payload[i] = createRecordDTO{
			AccountID:    r.AccountID,
			Amount:       amountDTO{Value: json.Number(r.Amount), CurrencyCode: r.CurrencyCode},
			RecordDate:   r.RecordDate,
			CategoryID:   r.CategoryID,
			CounterParty: r.CounterParty,
			Note:         r.Note,
		}
	}
	status, data, err := c.do(ctx, http.MethodPost, "/v1/api/records", nil, payload)
	if err != nil {
		return nil, err
	}
	if (status >= 200 && status < 300) || status == http.StatusMultiStatus || status == http.StatusBadRequest {
		return decodeBatch(in, data)
	}
	return nil, statusError(status, data)
}

// DeleteRecords deletes records by id (batches of <= 10, enforced by the core).
func (c *Client) DeleteRecords(ctx context.Context, ids []string) ([]core.RecordResult, error) {
	var body struct {
		Results []batchResult `json:"results"`
	}
	if err := c.send(ctx, http.MethodDelete, "/v1/api/records", nil, map[string][]string{"ids": ids}, &body); err != nil {
		return nil, err
	}
	out := make([]core.RecordResult, len(body.Results))
	for i, r := range body.Results {
		out[i] = core.RecordResult{RecordID: r.ID, OK: r.Success, Err: r.Error}
	}
	return out, nil
}

type recordDTO struct {
	ID         string    `json:"id"`
	Amount     amountDTO `json:"amount"`
	RecordDate string    `json:"recordDate"`
	Note       string    `json:"note"`
}

// FindRecords lists records matching q, following pagination. An empty
// q.FromDate widens the search past the API's implicit 3-month default.
func (c *Client) FindRecords(ctx context.Context, q core.RecordQuery) ([]core.RemoteRecord, error) {
	qv := url.Values{}
	if q.AccountID != "" {
		qv.Set("accountId", q.AccountID)
	}
	if q.Source != "" {
		qv.Set("source", q.Source)
	}
	from := q.FromDate
	if from == "" {
		from = epochStart
	}
	qv.Add("recordDate", "gte."+from)
	if q.ToDate != "" {
		qv.Add("recordDate", "lte."+q.ToDate)
	}

	var out []core.RemoteRecord
	err := c.paginate(ctx, "/v1/api/records", qv, func(page json.RawMessage) (int, error) {
		var body struct {
			Records    []recordDTO `json:"records"`
			NextOffset *int        `json:"nextOffset"`
		}
		if err := json.Unmarshal(page, &body); err != nil {
			return 0, fmt.Errorf("decoding records page: %w", err)
		}
		for _, r := range body.Records {
			out = append(out, core.RemoteRecord{
				ID: r.ID, Amount: r.Amount.Value.String(),
				RecordDate: r.RecordDate, Note: r.Note,
			})
		}
		return offsetOrDone(body.NextOffset), nil
	})
	return out, err
}

// --- transport ---------------------------------------------------------

func decodeBatch(in []core.RecordInput, data []byte) ([]core.RecordResult, error) {
	var body struct {
		Results []batchResult `json:"results"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("decoding records response: %w", err)
	}
	out := make([]core.RecordResult, 0, len(body.Results))
	for _, r := range body.Results {
		rowKey := ""
		if r.InputIndex >= 0 && r.InputIndex < len(in) {
			rowKey = in[r.InputIndex].RowKey
		}
		out = append(out, core.RecordResult{RowKey: rowKey, RecordID: r.ID, OK: r.Success, Err: r.Error})
	}
	return out, nil
}

// paginate calls path with increasing offset until handle reports done (-1).
func (c *Client) paginate(
	ctx context.Context, path string, base url.Values, handle func(json.RawMessage) (int, error),
) error {
	offset := 0
	for {
		q := cloneValues(base)
		q.Set("limit", strconv.Itoa(pageLimit))
		q.Set("offset", strconv.Itoa(offset))

		status, data, err := c.doRetrying(ctx, http.MethodGet, path, q, nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return statusError(status, data)
		}
		next, err := handle(data)
		if err != nil {
			return err
		}
		if next < 0 {
			return nil
		}
		offset = next
	}
}

// send issues a request (with automatic 429 retry) and decodes a successful
// JSON body into out.
func (c *Client) send(ctx context.Context, method, path string, q url.Values, reqBody, out any) error {
	status, data, err := c.doRetrying(ctx, method, path, q, reqBody)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return statusError(status, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding %s response: %w", path, err)
	}
	return nil
}

// doRetrying wraps do with bounded automatic retries on 429, honouring
// Retry-After. Use it for idempotent calls; record creation stays on do so the
// core owns its retry accounting.
func (c *Client) doRetrying(
	ctx context.Context, method, path string, q url.Values, reqBody any,
) (int, []byte, error) {
	for attempt := 0; ; attempt++ {
		status, data, err := c.do(ctx, method, path, q, reqBody)
		var rl *core.ErrRateLimited
		if !errors.As(err, &rl) || attempt >= maxGetRetries {
			return status, data, err
		}
		if werr := sleepCtx(ctx, rl.RetryAfter); werr != nil {
			return 0, nil, werr
		}
	}
}

// do performs one HTTP round trip and returns the status and the (bounded) body.
func (c *Client) do(
	ctx context.Context, method, path string, q url.Values, reqBody any,
) (int, []byte, error) {
	req, err := c.buildRequest(ctx, method, path, q, reqBody)
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("calling wallet api: %w", scrub(err))
	}
	data, err := readBody(resp)
	if err != nil {
		return 0, nil, err
	}
	if is429(resp.StatusCode) {
		return resp.StatusCode, data, &core.ErrRateLimited{RetryAfter: retryAfter(resp.Header)}
	}
	if err := pace(ctx, resp.Header); err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, data, nil
}

func (c *Client) buildRequest(
	ctx context.Context, method, path string, q url.Values, reqBody any,
) (*http.Request, error) {
	var reader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func readBody(resp *http.Response) ([]byte, error) {
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("reading wallet api response: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing wallet api response: %w", closeErr)
	}
	return data, nil
}

// pace slows the client pre-emptively when the server says the rate window is
// nearly spent, so a burst of writes approaches the limit instead of slamming
// into it.
func pace(ctx context.Context, h http.Header) error {
	rem := strings.TrimSpace(h.Get("X-RateLimit-Remaining"))
	if rem == "" {
		return nil
	}
	n, convErr := strconv.Atoi(rem)
	if convErr != nil || n > pacingThreshold {
		return nil //nolint:nilerr // an unparseable header just means no pacing signal
	}
	return sleepCtx(ctx, pacingPause)
}

// sleepCtx waits for d or ctx cancellation, whichever is first. It uses a timer
// rather than time.Sleep so a cancelled run stops promptly.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting on rate limit: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

func statusError(status int, body []byte) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return core.ErrUnauthorized
	}
	return fmt.Errorf("wallet api status %d: %s", status, snippet(body))
}

func is429(status int) bool { return status == http.StatusTooManyRequests }

func retryAfter(h http.Header) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return time.Second
}

func offsetOrDone(next *int) int {
	if next == nil {
		return -1
	}
	return *next
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// scrub removes a bearer token from an error string if a transport ever
// includes the request URL or headers verbatim.
func scrub(err error) error {
	msg := err.Error()
	if !strings.Contains(msg, "Bearer ") {
		return err
	}
	return fmt.Errorf("%s", redactBearer(msg)) //nolint:err113 // deliberately flattening a scrubbed transport error
}

func redactBearer(s string) string {
	for {
		i := strings.Index(s, "Bearer ")
		if i < 0 {
			return s
		}
		end := i + len("Bearer ")
		for end < len(s) && s[end] != ' ' && s[end] != '"' && s[end] != '\n' {
			end++
		}
		s = s[:i] + "Bearer [redacted]" + s[end:]
	}
}
