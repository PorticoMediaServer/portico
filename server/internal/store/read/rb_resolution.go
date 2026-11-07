package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// ResolutionInput preserves the public missing-as-zero order without a SQL
// expression sort. NULL and explicit zero are merged by entity ID in Go.
type ResolutionInput struct {
	View  BrowseInput
	After *ResolutionCursor
}
type ResolutionCursor struct {
	fence   browseFence
	key, id int64
}
type ResolutionPage struct {
	Items                             []BrowseItem
	Next                              *ResolutionCursor
	Exhausted                         bool
	RawRows, RawPositions, Statements int
}

// ResolutionWindow is a distinct recipe: up to 128 fetched raw rows for two
// disjoint default streams, 64 consumed positions and 40 output values. The
// caller must fund that fetched bound, including both rejected and lookahead
// rows. Restricted containers still use the default fail-closed policy.
func (r *BrowseReader) ResolutionWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ResolutionInput) (ResolutionPage, BrowseReadResult, error) {
	if r == nil {
		return ResolutionPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (ResolutionPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.resolutionWindow(ctx, request, budget, output, in)
	})
}
func (r *BrowseReader) resolutionWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ResolutionInput) (page ResolutionPage, result BrowseReadResult, err error) {
	if r == nil || in.View.After != nil || request.Budget.RawRows < 2*MaxRaw+5 {
		return page, result, ErrInput
	}
	switch in.View.Kind {
	case 1, 2, 3, 4, 10:
	default:
		return page, result, ErrUnqualified
	}
	page, result, err = publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[ResolutionPage]{func(ctx context.Context, query browseQuery, f browseFence, draft *ResolutionPage) error {
		if in.After != nil && in.After.fence != f {
			return ErrStale
		}
		var e error
		*draft, e = readRBResolution(ctx, query, in, f)
		return e
	}})
	if err == nil {
		page.Statements = result.LookupStatements + result.PublicationStatements
	}
	return
}

func readRBResolution(ctx context.Context, query browseQuery, in ResolutionInput, f browseFence) (ResolutionPage, error) {
	dir := "asc."
	if in.View.Descending {
		dir = "desc."
	}
	prefix := "rb.resolution." + dir
	var streams [2][][]any
	name := "first"
	args := []any{in.View.Library, in.View.Kind}
	if in.After != nil {
		name = "tail"
		args = append(args, in.After.key, in.After.id)
	}
	rows, err := query(prefix+"value."+name, append(args, int64(MaxRaw))...)
	if err != nil {
		return ResolutionPage{}, err
	}
	if in.After != nil && len(rows) < MaxRaw {
		more, e := query(prefix+"value.next", in.View.Library, in.View.Kind, in.After.key, int64(MaxRaw-len(rows)))
		if e != nil {
			return ResolutionPage{}, e
		}
		rows = append(rows, more...)
	}
	streams[0] = rows
	// Missing values have public key zero. Once the cursor has passed zero,
	// that entire stream is behind it. At zero, both streams use the SAME ID
	// bound, preserving interleaved NULL/explicit-zero ties without duplicates.
	nullPending := in.After == nil || (!in.View.Descending && in.After.key <= 0) || (in.View.Descending && in.After.key >= 0)
	if nullPending {
		name = "first"
		args = []any{in.View.Library, in.View.Kind}
		if in.After != nil && in.After.key == 0 {
			name = "tail"
			args = append(args, in.After.id)
		}
		streams[1], err = query(prefix+"null."+name, append(args, int64(MaxRaw))...)
		if err != nil {
			return ResolutionPage{}, err
		}
	}
	key := func(row []any) int64 {
		if row[1] == nil {
			return 0
		}
		return row[1].(int64)
	}
	page := ResolutionPage{RawRows: len(streams[0]) + len(streams[1])}
	a, b := 0, 0
	for page.RawPositions < MaxRaw && (a < len(streams[0]) || b < len(streams[1])) {
		if e := ctx.Err(); e != nil {
			return ResolutionPage{}, e
		}
		left := b == len(streams[1])
		if a < len(streams[0]) && b < len(streams[1]) {
			x, y := streams[0][a], streams[1][b]
			kx, ky := key(x), key(y)
			ix, iy := x[0].(int64), y[0].(int64)
			left = (!in.View.Descending && (kx < ky || (kx == ky && ix < iy))) || (in.View.Descending && (kx > ky || (kx == ky && ix > iy)))
		}
		var row []any
		if left {
			row = streams[0][a]
			a++
		} else {
			row = streams[1][b]
			b++
		}
		page.RawPositions++
		page.Next = &ResolutionCursor{fence: f, key: key(row), id: row[0].(int64)}
		if !admittedItem(row, f) {
			continue
		}
		item, e := itemValue(row)
		if e != nil {
			return ResolutionPage{}, e
		}
		page.Items = append(page.Items, item)
		if len(page.Items) == in.View.Limit {
			break
		}
	}
	page.Exhausted = len(streams[0]) < MaxRaw && len(streams[1]) < MaxRaw && a == len(streams[0]) && b == len(streams[1])
	if page.Exhausted {
		page.Next = nil
	}
	return page, nil
}
