package storage

import (
	"context"
	"encoding/json"
	"io"
	"runtime"
)

// A folder batch used to list a full page of every folder it was given and
// then publish only the prefix that fitted one writer page, discarding the
// rest (SRV029). The budgeted batch shares one raw-name allowance across its
// folders instead: it lists folders in order until the allowance is spent and
// marks the rest skipped, so nothing it lists is thrown away.

// Budget envelope outcomes.
const (
	BudgetPage    = "page"
	BudgetError   = "error"
	BudgetSkipped = "skipped"
)

// BudgetEnvelope is one folder's answer in a budgeted batch. RawUsed counts the
// names the folder's listing read, including those the page then dropped.
type BudgetEnvelope struct {
	RelativePath string         `json:"relativePath"`
	Outcome      string         `json:"outcome"`
	RawUsed      int            `json:"rawUsed,omitempty"`
	Page         *InventoryPage `json:"page,omitempty"`
	ErrorCode    string         `json:"errorCode,omitempty"`
}

// MaxInventoryBudget is the largest raw-name allowance one batch may share:
// one writer page at the fastest scan speed.
const MaxInventoryBudget = 128

// LocalInventoryPagesBudget lists requests in order under one shared raw-name
// budget. Each folder gets at most the budget left; once it is spent, or after
// a folder's error or an unstable listing, every later folder is skipped. The
// helper and the server's in-process path both call this, so both obey the
// same rule. It reads retained-stream cursors nowhere, so it is Linux only.
func LocalInventoryPagesBudget(requests []InventoryRequest, budget int) ([]BudgetEnvelope, error) {
	if runtime.GOOS != "linux" || budget < 1 || budget > MaxInventoryBudget || len(requests) == 0 || len(requests) > MaxInventoryBatch {
		return nil, errInventoryBatch
	}
	for _, r := range requests {
		if r.Root != requests[0].Root || r.Stream {
			return nil, errInventoryBatch
		}
	}
	out := make([]BudgetEnvelope, len(requests))
	remaining, stopped := budget, false
	for i, r := range requests {
		out[i] = BudgetEnvelope{RelativePath: r.RelativePath, Outcome: BudgetSkipped}
		if stopped || remaining == 0 {
			stopped = true
			continue
		}
		r.Limit = min(r.Limit, remaining)
		page, raw, err := localInventoryPage(r)
		if err != nil {
			// What the failed listing read is unknown, but no more than this
			// folder's limit, which was at most what remained.
			out[i] = BudgetEnvelope{RelativePath: r.RelativePath, Outcome: BudgetError, ErrorCode: inventoryErrorCode(err)}
			stopped = true
			continue
		}
		out[i] = BudgetEnvelope{RelativePath: r.RelativePath, Outcome: BudgetPage, RawUsed: raw, Page: &page}
		remaining -= raw
		stopped = page.Unstable
	}
	return out, nil
}

func inventoryBudgetHelper(requests []InventoryRequest, budget int, out io.Writer) error {
	envelopes, err := LocalInventoryPagesBudget(requests, budget)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	for _, e := range envelopes {
		if err = encoder.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

// InventoryPagesBudget lists a batch of one root's folders in one helper under
// a shared raw-name budget (LocalInventoryPagesBudget). Linux only; the returned
// error means nothing was listed and the caller lists one folder instead.
func (c *Client) InventoryPagesBudget(ctx context.Context, key string, requests []InventoryRequest, budget int) ([]BudgetEnvelope, error) {
	if runtime.GOOS != "linux" || budget < 1 || budget > MaxInventoryBudget {
		return nil, errInventoryBatch
	}
	if err := c.checkInventoryBatch(requests); err != nil {
		return nil, err
	}
	envelopes := make([]BudgetEnvelope, len(requests))
	err := c.inventoryBatchRequest(ctx, key, "inventory-pages-budget", requests, budget, func(i int, d *json.Decoder) error {
		return d.Decode(&envelopes[i])
	})
	if err != nil {
		return nil, err
	}
	return envelopes, nil
}

// CheckBudgetResponse admits a budgeted batch's answer before anything of it is
// published. Any violation rejects it whole: no partial trust.
//
//   - one envelope per request, naming that request's folder;
//   - pages first, then at most one error or unstable page, then only skips;
//   - a page reads no more than the budget left before it, publishes no more
//     entries than it read, and is complete only if it stopped short of its
//     limit (the reader reports EOF only then);
//   - an error carries a known code and nothing else; a skip carries nothing.
func CheckBudgetResponse(requests []InventoryRequest, budget int, envelopes []BudgetEnvelope) error {
	if len(envelopes) != len(requests) || budget < 1 || budget > MaxInventoryBudget {
		return errInventoryBatch
	}
	remaining, stopped := budget, false
	for i, e := range envelopes {
		r := requests[i]
		if e.RelativePath != r.RelativePath {
			return errInventoryBatch
		}
		switch e.Outcome {
		case BudgetSkipped:
			if e.Page != nil || e.ErrorCode != "" || e.RawUsed != 0 {
				return errInventoryBatch
			}
			stopped = true
		case BudgetError:
			if stopped || e.Page != nil || e.RawUsed != 0 || !knownInventoryCode(e.ErrorCode) {
				return errInventoryBatch
			}
			stopped = true
		case BudgetPage:
			p := e.Page
			limit := min(r.Limit, remaining)
			if stopped || p == nil || e.ErrorCode != "" || p.ErrorCode != "" || limit < 1 {
				return errInventoryBatch
			}
			if e.RawUsed < len(p.Entries) || e.RawUsed > limit || len(p.Entries) > 128 || len(p.Issues) > 512 {
				return errInventoryBatch
			}
			if p.RootIdentity == "" || r.RootIdentity != "" && !SameRoot(r.RootIdentity, p.RootIdentity) {
				return errInventoryBatch
			}
			if p.Complete == (p.NextCursor != "") {
				return errInventoryBatch
			}
			if p.Complete && !p.Unstable && e.RawUsed >= limit {
				return errInventoryBatch
			}
			remaining -= e.RawUsed
			stopped = p.Unstable
		default:
			return errInventoryBatch
		}
	}
	return nil
}

func knownInventoryCode(code string) bool {
	return code == "not_found" || code == "changed" || code == "unreadable"
}
