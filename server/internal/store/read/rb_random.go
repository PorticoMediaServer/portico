package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// RandomInput rotates the accepted FNV32 index at Seed. It never rehashes or
// sorts the library per request. The supported policy envelope is unchanged.
type RandomInput struct {
	View  BrowseInput
	Seed  uint32
	After *RandomCursor
}
type RandomCursor struct {
	fence   browseFence
	seed    uint32
	key, id int64
}
type RandomPage struct {
	Items                             []BrowseItem
	Next                              *RandomCursor
	Exhausted                         bool
	RawRows, RawPositions, Statements int
}

func (r *BrowseReader) RandomWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RandomInput) (RandomPage, BrowseReadResult, error) {
	if r == nil {
		return RandomPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RandomPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.randomWindow(ctx, request, budget, output, in)
	})
}
func (r *BrowseReader) randomWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RandomInput) (page RandomPage, result BrowseReadResult, err error) {
	if r == nil || in.View.After != nil {
		return page, result, ErrInput
	}
	page, result, err = publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[RandomPage]{func(ctx context.Context, query browseQuery, f browseFence, draft *RandomPage) error {
		if in.After != nil && (in.After.fence != f || in.After.seed != in.Seed) {
			return ErrStale
		}
		var e error
		*draft, e = readRandomPage(ctx, query, in, f)
		return e
	}})
	if err == nil {
		page.Statements = result.LookupStatements + result.PublicationStatements
	}
	return
}
func readRandomPage(ctx context.Context, query browseQuery, in RandomInput, f browseFence) (RandomPage, error) {
	prefix := "rb.random.asc."
	if in.View.Descending {
		prefix = "rb.random.desc."
	}
	seed := int64(in.Seed)
	type seek struct {
		name string
		args []any
	}
	seeks := []seek{{"first", []any{seed}}, {"wrapped", []any{seed}}}
	if p := in.After; p != nil {
		wrapped := p.key < seed
		if in.View.Descending {
			wrapped = p.key > seed
		}
		if wrapped {
			seeks = []seek{{"tail", []any{p.key, p.id}}, {"wrapped_next", []any{p.key, seed}}}
		} else {
			seeks = []seek{{"tail", []any{p.key, p.id}}, {"next", []any{p.key}}, {"wrapped", []any{seed}}}
		}
	}
	var raw [][]any
	for _, s := range seeks {
		args := []any{in.View.Library, in.View.Kind}
		args = append(args, s.args...)
		args = append(args, int64(MaxRaw-len(raw)))
		rows, err := query(prefix+s.name, args...)
		if err != nil {
			return RandomPage{}, err
		}
		raw = append(raw, rows...)
		if len(raw) == MaxRaw {
			break
		}
	}
	page := RandomPage{RawRows: len(raw)}
	for _, row := range raw {
		if err := ctx.Err(); err != nil {
			return RandomPage{}, err
		}
		page.Next = &RandomCursor{fence: f, seed: in.Seed, key: row[1].(int64), id: row[0].(int64)}
		page.RawPositions++
		if !admittedItem(row, f) {
			continue
		}
		item, err := itemValue(row)
		if err != nil {
			return RandomPage{}, err
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
