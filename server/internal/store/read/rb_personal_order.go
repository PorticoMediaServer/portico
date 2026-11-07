package read

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

type PersonalOrder string

const (
	PersonalRatingOrder    PersonalOrder = "personalRating"
	PersonalPlayedOrder    PersonalOrder = "lastPlayed"
	PersonalPlayCountOrder PersonalOrder = "playCount"
	personalOrderMaximum                 = 256
)

type PersonalOrderInput struct {
	View  BrowseInput
	Order PersonalOrder
	After *PersonalOrderCursor
}
type PersonalOrderCursor struct {
	fence browseFence
	order PersonalOrder
	key   rbPersonalOrderKey
}
type PersonalOrderPage struct {
	Items     []BrowseItem
	After     *PersonalOrderCursor
	Exhausted bool
	// Total is exact only because the entire <=256-row raw set was exhausted.
	Total, RawRows int
}
type rbPersonalOrderKey struct {
	valued      bool
	number      int64
	text, title string
	id          int64
}
type rbPersonalOrderValue struct {
	item BrowseItem
	key  rbPersonalOrderKey
}

// PersonalOrderExact is a complete-set fallback, not an indexed large-library
// order claim. It fetches at most257 raw rows including the exhaustion probe.
// Retired/restricted/missing-personal rows never shrink that admission ceiling.
// Larger scopes refuse for leased preparation rather than returning a top-K
// sample as an exact global order. Container/label refusals stay in the common
// default recipe until the corresponding aggregate eligibility is composed.
func (r *BrowseReader) PersonalOrderExact(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in PersonalOrderInput) (PersonalOrderPage, BrowseReadResult, error) {
	if r == nil {
		return PersonalOrderPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (PersonalOrderPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.personalOrderExact(ctx, request, budget, out, in)
	})
}
func (r *BrowseReader) personalOrderExact(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in PersonalOrderInput) (PersonalOrderPage, BrowseReadResult, error) {
	if in.Order != PersonalRatingOrder && in.Order != PersonalPlayedOrder && in.Order != PersonalPlayCountOrder || in.View.After != nil {
		return PersonalOrderPage{}, BrowseReadResult{}, ErrInput
	}
	if in.View.KeepArticles {
		return PersonalOrderPage{}, BrowseReadResult{}, ErrUnqualified
	}
	if request.Budget.RawRows < personalOrderMaximum+1+5 {
		return PersonalOrderPage{}, BrowseReadResult{}, ErrInput
	}
	if in.After != nil && in.After.order != in.Order {
		return PersonalOrderPage{}, BrowseReadResult{}, ErrStale
	}
	return publishRead(ctx, r.snapshots, request, budget, out, in.View, []readStep[PersonalOrderPage]{func(ctx context.Context, q browseQuery, f browseFence, page *PersonalOrderPage) error {
		if in.After != nil && in.After.fence != f {
			return ErrStale
		}
		rows, e := q("rb.personal.order.source", f.profile, in.View.Library, in.View.Kind, int64(personalOrderMaximum+1))
		if e != nil {
			return e
		}
		if len(rows) > personalOrderMaximum {
			return ErrNotReady
		}
		page.RawRows = len(rows)
		values := make([]rbPersonalOrderValue, 0, len(rows))
		for _, row := range rows {
			if e := ctx.Err(); e != nil {
				return e
			}
			if !admittedItem(row, f) {
				continue
			}
			item, e := itemValue(row)
			if e != nil {
				return e
			}
			key := rbPersonalOrderKey{id: item.ID, title: row[1].(string)}
			if !utf8.ValidString(key.title) {
				return ErrUnqualified
			}
			switch in.Order {
			case PersonalRatingOrder:
				if row[7] != nil {
					key.number = row[7].(int64)
				}
				key.valued = key.number > 0
			case PersonalPlayCountOrder:
				if row[8] != nil {
					key.number = row[8].(int64)
				}
				key.valued = key.number > 0
			case PersonalPlayedOrder:
				if row[9] != nil {
					key.text = row[9].(string)
				}
				if !utf8.ValidString(key.text) {
					return ErrUnqualified
				}
				key.valued = key.text != ""
			}
			values = append(values, rbPersonalOrderValue{item, key})
		}
		slices.SortFunc(values, func(a, b rbPersonalOrderValue) int {
			return rbComparePersonalOrder(a.key, b.key, in.Order, in.View.Descending)
		})
		page.Total = len(values)
		for _, value := range values {
			if in.After != nil && rbComparePersonalOrder(value.key, in.After.key, in.Order, in.View.Descending) <= 0 {
				continue
			}
			if len(page.Items) == in.View.Limit {
				return nil
			}
			page.Items = append(page.Items, value.item)
			page.After = &PersonalOrderCursor{fence: f, order: in.Order, key: value.key}
		}
		page.Exhausted = true
		page.After = nil
		return ctx.Err()
	}})
}

func rbComparePersonalOrder(a, b rbPersonalOrderKey, order PersonalOrder, descending bool) int {
	cmpInt := func(a, b int64) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
	if a.valued != b.valued {
		if a.valued == descending {
			return -1
		}
		return 1
	}
	if !a.valued {
		// The public missing-value run is title ASC/id ASC in BOTH directions.
		if c := strings.Compare(rbPersonalASCIIFold(a.title), rbPersonalASCIIFold(b.title)); c != 0 {
			return c
		}
		return cmpInt(a.id, b.id)
	}
	c := cmpInt(a.number, b.number)
	if order == PersonalPlayedOrder {
		c = strings.Compare(a.text, b.text)
	}
	if c == 0 {
		c = cmpInt(a.id, b.id)
	}
	if descending {
		return -c
	}
	return c
}
func rbPersonalASCIIFold(s string) string {
	// SQLite NOCASE folds ASCII only; Unicode replacement would change ties.
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			if out == nil {
				out = []byte(s)
			}
			out[i] = s[i] + ('a' - 'A')
		}
	}
	if out == nil {
		return s
	}
	return string(out)
}
