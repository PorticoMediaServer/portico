package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

type ContinueInput struct {
	View  BrowseInput
	After *ContinueCursor
}
type ContinueCursor struct {
	fence  browseFence
	at     string
	public [16]byte
}
type ContinueItem struct {
	BrowseItem
	State UserState
}
type ContinuePage struct {
	Items                 []ContinueItem
	Next                  *ContinueCursor
	Exhausted             bool
	RawRows, RawPositions int
}

// ContinueWatching starts from this profile's sparse state, never the shared
// catalogue or immutable history. A window examines <=64 raw state positions;
// denied/removed/other-library rows still advance its continuation. This leaf
// recipe refuses containers; Home's container/on-deck projection is separate.
func (r *ForegroundReader) ContinueWatching(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ContinueInput) (ContinuePage, BrowseReadResult, error) {
	if r == nil || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return ContinuePage{}, BrowseReadResult{}, ErrInput
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[ContinuePage]{func(ctx context.Context, q browseQuery, f browseFence, d *ContinuePage) error {
		if f.container != 0 {
			return ErrUnqualified
		}
		if in.After != nil && in.After.fence != f {
			return ErrStale
		}
		var rows [][]any
		var e error
		if in.After == nil {
			rows, e = q("foreground.continue.first", f.profile, int64(MaxRaw))
		} else {
			rows, e = q("foreground.continue.tail", f.profile, in.After.at, in.After.public[:], int64(MaxRaw))
			if e == nil && len(rows) < MaxRaw {
				var next [][]any
				next, e = q("foreground.continue.next", f.profile, in.After.at, int64(MaxRaw-len(rows)))
				rows = append(rows, next...)
			}
		}
		if e != nil {
			return e
		}
		d.RawRows = len(rows)
		for _, v := range rows {
			if e := ctx.Err(); e != nil {
				return e
			}
			public, ok := v[1].([]byte)
			if !ok || len(public) != 16 {
				return ErrUnqualified
			}
			cursor := &ContinueCursor{fence: f, at: v[0].(string)}
			copy(cursor.public[:], public)
			d.Next = cursor
			d.RawPositions++
			// All eligibility is after the raw ordered seek/limit. The live join is
			// a point probe; no library/kind predicate can scan a sparse state tail.
			if v[10] == nil || v[17].(int64) != in.View.Library || v[18].(int64) != in.View.Kind {
				continue
			}
			if !admittedItem(v[10:17], f) || v[3].(int64) <= 0 || v[9].(int64) != 0 {
				continue
			}
			item, e := itemValue(v[10:17])
			if e != nil {
				return e
			}
			if item.PublicID != cursor.public {
				return ErrUnqualified
			}
			d.Items = append(d.Items, ContinueItem{BrowseItem: item, State: stateValue(v[2:10])})
			if len(d.Items) == in.View.Limit {
				break
			}
		}
		d.Exhausted = d.RawPositions == len(rows) && len(rows) < MaxRaw
		if d.Exhausted {
			d.Next = nil
		}
		return nil
	}})
}
