package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Chronology distinguishes fixed Date added from Home's accepted new-content
// chronology. It does not project a Home work into a season/episode target.
type Chronology string

const (
	DateAdded    Chronology = "added"
	ContentAdded Chronology = "contentAdded"
)

type ChronologyInput struct {
	View  BrowseInput
	Clock Chronology
	After *ChronologyCursor
}
type ChronologyCursor struct {
	fence browseFence
	clock Chronology
	key   string
	null  bool
	id    int64
}
type ChronologyPage struct {
	Items                             []BrowseItem
	Next                              *ChronologyCursor
	Exhausted                         bool
	RawRows, RawPositions, Statements int
}

// ChronologyWindow streams exact source dates, including their undated default
// bucket, through ordinary foreground/output custody. It is harness-only.
func (r *BrowseReader) ChronologyWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ChronologyInput) (ChronologyPage, BrowseReadResult, error) {
	if r == nil {
		return ChronologyPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (ChronologyPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.chronologyWindow(ctx, request, budget, output, in)
	})
}
func (r *BrowseReader) chronologyWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ChronologyInput) (page ChronologyPage, result BrowseReadResult, err error) {
	if r == nil || in.View.After != nil || (in.Clock != DateAdded && in.Clock != ContentAdded) {
		return page, result, ErrInput
	}
	page, result, err = publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[ChronologyPage]{func(ctx context.Context, query browseQuery, f browseFence, draft *ChronologyPage) error {
		if in.After != nil && (in.After.fence != f || in.After.clock != in.Clock) {
			return ErrStale
		}
		var e error
		*draft, e = readChronologyPage(ctx, query, in, f)
		return e
	}})
	if err == nil {
		page.Statements = result.LookupStatements + result.PublicationStatements
	}
	return
}
func readChronologyPage(ctx context.Context, query browseQuery, in ChronologyInput, f browseFence) (ChronologyPage, error) {
	clock := in.Clock
	// The approved monotonic new-content fact belongs only to shows/seasons.
	// All other families retain their own accepted Date-added chronology.
	if in.View.Kind != 2 && in.View.Kind != 3 {
		clock = DateAdded
	}
	prefix := "rb.chronology." + string(clock) + ".asc."
	if in.View.Descending {
		prefix = "rb.chronology." + string(clock) + ".desc."
	}
	type seek struct {
		name string
		args []any
	}
	seeks := []seek{{"first", nil}}
	if in.After != nil {
		p := in.After
		if p.null {
			seeks = []seek{{"null_tail", []any{p.id}}}
			if !in.View.Descending {
				seeks = append(seeks, seek{"values", nil})
			}
		} else {
			seeks = []seek{{"tail", []any{p.key, p.id}}, {"next", []any{p.key}}}
			if in.View.Descending {
				seeks = append(seeks, seek{"nulls", nil})
			}
		}
	}
	var raw [][]any
	for _, s := range seeks {
		args := []any{in.View.Library, in.View.Kind}
		args = append(args, s.args...)
		args = append(args, int64(MaxRaw-len(raw)))
		rows, err := query(prefix+s.name, args...)
		if err != nil {
			return ChronologyPage{}, err
		}
		raw = append(raw, rows...)
		if len(raw) == MaxRaw {
			break
		}
	}
	page := ChronologyPage{RawRows: len(raw)}
	for _, row := range raw {
		if e := ctx.Err(); e != nil {
			return ChronologyPage{}, e
		}
		p := &ChronologyCursor{fence: f, clock: in.Clock, id: row[0].(int64), null: row[1] == nil}
		if !p.null {
			p.key = row[1].(string)
		}
		page.Next = p
		page.RawPositions++
		if !admittedItem(row, f) {
			continue
		}
		item, err := itemValue(row)
		if err != nil {
			return ChronologyPage{}, err
		}
		page.Items = append(page.Items, item)
		if len(page.Items) == in.View.Limit {
			break
		}
	}
	page.Exhausted = page.RawPositions == len(raw) && len(raw) < MaxRaw
	if page.Exhausted {
		page.Next = nil
	}
	return page, nil
}
