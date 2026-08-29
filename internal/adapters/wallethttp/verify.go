package wallethttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	verify "github.com/dpcat237/bbwalet-utils/internal/core/walletverify"
)

// VerifyClient adapts the Wallet REST transport to the read-only
// walletverify.WalletReader port. It is a separate type because Client.Accounts
// and Client.Records already serve the loader's walletload ports with different
// return shapes. All reads are GETs; nothing here writes.
type VerifyClient struct {
	c *Client
}

// NewVerify returns a VerifyClient. hc must be non-nil (set its Timeout from
// config).
func NewVerify(hc *http.Client, baseURL, token string) *VerifyClient {
	return &VerifyClient{c: New(hc, baseURL, token)}
}

type verifyRecordDTO struct {
	ID       string    `json:"id"`
	Amount   amountDTO `json:"amount"`
	Category struct {
		Name string `json:"name"`
	} `json:"category"`
	ConvertedAmount struct {
		Value        json.Number `json:"value"`
		CurrencyCode string      `json:"currencyCode"`
		Ratio        json.Number `json:"ratio"`
	} `json:"convertedAmount"`
	RecordDate  string `json:"recordDate"`
	Source      string `json:"source"`
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
}

// Accounts lists every account, following pagination.
func (v *VerifyClient) Accounts(ctx context.Context) ([]verify.Account, error) {
	var out []verify.Account
	err := v.c.paginate(ctx, "/v1/api/accounts", url.Values{}, func(page json.RawMessage) (int, error) {
		var body struct {
			Accounts   []accountDTO `json:"accounts"`
			NextOffset *int         `json:"nextOffset"`
		}
		if err := json.Unmarshal(page, &body); err != nil {
			return 0, fmt.Errorf("decoding verify accounts page: %w", err)
		}
		for _, a := range body.Accounts {
			out = append(out, verify.Account{ID: a.ID, Name: a.Name, CurrencyCode: a.CurrencyCode})
		}
		return offsetOrDone(body.NextOffset), nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", verify.ErrWalletRead, err)
	}
	return out, nil
}

// Records lists every record matching q, following pagination. It sets convertTo
// only when q.ConvertTo is non-empty, and widens the API's implicit ~3-month
// window to the epoch so historical rows are returned.
func (v *VerifyClient) Records(ctx context.Context, q verify.RecordQuery) ([]verify.Record, error) {
	base := recordQueryValues(q)
	base.Add("recordDate", "gte."+epochStart)

	var out []verify.Record
	err := v.c.paginate(ctx, "/v1/api/records", base, func(page json.RawMessage) (int, error) {
		var body struct {
			Records    []verifyRecordDTO `json:"records"`
			NextOffset *int              `json:"nextOffset"`
		}
		if err := json.Unmarshal(page, &body); err != nil {
			return 0, fmt.Errorf("decoding verify records page: %w", err)
		}
		for _, r := range body.Records {
			rec, err := toVerifyRecord(r)
			if err != nil {
				return 0, err
			}
			out = append(out, rec)
		}
		return offsetOrDone(body.NextOffset), nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", verify.ErrWalletRead, err)
	}
	return out, nil
}

func recordQueryValues(q verify.RecordQuery) url.Values {
	v := url.Values{}
	if q.AccountID != "" {
		v.Set("accountId", q.AccountID)
	}
	if q.Source != "" {
		v.Set("source", q.Source)
	}
	if q.ConvertTo != "" {
		v.Set("convertTo", q.ConvertTo)
	}
	return v
}

func toVerifyRecord(d verifyRecordDTO) (verify.Record, error) {
	var date time.Time
	if d.RecordDate != "" {
		t, err := time.Parse(time.RFC3339, d.RecordDate)
		if err != nil {
			return verify.Record{}, fmt.Errorf("%w: parsing record date %q: %w", verify.ErrWalletRead, d.RecordDate, err)
		}
		date = t
	}
	return verify.Record{
		ID:                d.ID,
		AccountID:         d.AccountID,
		AccountName:       d.AccountName,
		CategoryName:      d.Category.Name,
		Amount:            d.Amount.Value.String(),
		CurrencyCode:      d.Amount.CurrencyCode,
		ConvertedValue:    d.ConvertedAmount.Value.String(),
		ConvertedRatio:    d.ConvertedAmount.Ratio.String(),
		ConvertedCurrency: d.ConvertedAmount.CurrencyCode,
		Date:              date,
		Source:            d.Source,
	}, nil
}
