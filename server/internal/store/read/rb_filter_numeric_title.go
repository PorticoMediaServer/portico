package read

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// NumericTitleFilterInput is a bounded title-ordered source window, not an exact
// total or a general filter endpoint. The closed subset is year, decade, library
// and entityKind for the four video kinds. Unsupported predicates also refuse
// under NOT. Text, relationship, personal and relative-time filters remain open.
type NumericTitleFilterInput struct {
	View  BrowseInput
	Query json.RawMessage
	After *NumericTitleFilterCursor
}
type NumericTitleFilterCursor struct {
	fence browseFence
	query [32]byte
	key   string
	id    int64
}
type NumericTitleFilterPage struct {
	Items                 []BrowseItem
	After                 *NumericTitleFilterCursor
	RawRows, RawPositions int
	Exhausted             bool
}

func (r *BrowseReader) NumericTitleFilterWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in NumericTitleFilterInput) (NumericTitleFilterPage, BrowseReadResult, error) {
	if r == nil {
		return NumericTitleFilterPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (NumericTitleFilterPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.numericTitleFilterWindow(ctx, request, budget, out, in)
	})
}
func (r *BrowseReader) numericTitleFilterWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in NumericTitleFilterInput) (NumericTitleFilterPage, BrowseReadResult, error) {
	if in.View.After != nil || in.View.policy != (readPolicyBinding{}) || request.Budget.RawRows < MaxRaw+5 || len(in.Query) > 64<<10 || !utf8.Valid(in.Query) {
		return NumericTitleFilterPage{}, BrowseReadResult{}, ErrInput
	}
	if in.View.Kind < 1 || in.View.Kind > 4 {
		return NumericTitleFilterPage{}, BrowseReadResult{}, ErrUnqualified
	}
	// Reuse the bounded read session's counter/deadline/custody only; this path
	// reads the normal library recipe and never invokes saved-resource authority.
	s, err := newSavedViewSession(&SavedViewReader{snapshots: r.snapshots, pool: r.pool}, ctx, request, budget, out, in.View)
	if err != nil {
		return NumericTitleFilterPage{}, BrowseReadResult{}, err
	}
	// Input bytes determine the prospective bounded decoder/AST scratch. Source
	// copies debit this SAME original counter; it never resets between views.
	if err = s.reserve((1 << 20) + (32 << 10) + 16*int64(len(in.Query))); err != nil {
		return NumericTitleFilterPage{}, s.result(), err
	}
	body := bytes.Clone(in.Query)
	var first browseFence
	var page NumericTitleFilterPage
	var queryHash [32]byte
	ready := false
	err = s.window(false, 0, func(q browseQuery) error {
		var e error
		first, e = readBrowseFence(q, s.input(in.View))
		if e != nil {
			return e
		}
		filter, e := compileRBScalarFilter(body)
		if e != nil {
			return e
		}
		if !rbNumericTitleFilterSupported(filter.node) {
			return ErrUnqualified
		}
		canonical, e := json.Marshal(filter.node)
		if e != nil {
			return e
		}
		queryHash = sha256.Sum256(canonical)
		if in.After != nil && (in.After.fence != first || in.After.query != queryHash) {
			return ErrStale
		}
		ready = true
		page, e = readRBNumericTitlePage(ctx, q, in, first, filter, queryHash)
		return e
	})
	if ready {
		prior := err
		final := s.window(true, 0, func(q browseQuery) error {
			current, e := readBrowseFence(q, s.input(in.View))
			if e != nil {
				return e
			}
			if current != first {
				return ErrStale
			}
			return nil
		})
		err = s.finish(first, prior, final)
	}
	if err != nil {
		return NumericTitleFilterPage{}, s.result(), err
	}
	return page, s.result(), nil
}
func rbNumericTitleFilterSupported(n *rbFilterNode) bool {
	if n == nil {
		return true
	}
	if n.Field != "" {
		return n.Field == "year" || n.Field == "decade" || n.Field == "library" || n.Field == "entityKind"
	}
	for i := range n.All {
		if !rbNumericTitleFilterSupported(&n.All[i]) {
			return false
		}
	}
	for i := range n.Any {
		if !rbNumericTitleFilterSupported(&n.Any[i]) {
			return false
		}
	}
	return rbNumericTitleFilterSupported(n.Not)
}
func readRBNumericTitlePage(ctx context.Context, q browseQuery, in NumericTitleFilterInput, f browseFence, filter rbScalarFilter, queryHash [32]byte) (NumericTitleFilterPage, error) {
	name := "rb.filter.numeric.title.ignore.asc."
	if in.View.KeepArticles {
		name = "rb.filter.numeric.title.keep.asc."
	}
	if in.View.Descending {
		name = "rb.filter.numeric.title.ignore.desc."
		if in.View.KeepArticles {
			name = "rb.filter.numeric.title.keep.desc."
		}
	}
	var rows [][]any
	var err error
	if in.After == nil {
		rows, err = q(name+"first", in.View.Library, in.View.Kind, int64(MaxRaw))
	} else {
		rows, err = q(name+"tail", in.View.Library, in.View.Kind, in.After.key, in.After.id, int64(MaxRaw))
		if err == nil && len(rows) < MaxRaw {
			var next [][]any
			next, err = q(name+"next", in.View.Library, in.View.Kind, in.After.key, int64(MaxRaw-len(rows)))
			rows = append(rows, next...)
		}
	}
	if err != nil {
		return NumericTitleFilterPage{}, err
	}
	if len(rows) > MaxRaw {
		return NumericTitleFilterPage{}, ErrUnqualified
	}
	page := NumericTitleFilterPage{RawRows: len(rows)}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return NumericTitleFilterPage{}, err
		}
		if len(row) != 8 {
			return NumericTitleFilterPage{}, ErrUnqualified
		}
		page.RawPositions++
		page.After = &NumericTitleFilterCursor{f, queryHash, row[1].(string), row[0].(int64)}
		if !admittedItem(row, f) || !filter.matches(rbFilterFacts{title: row[3].(string), library: f.library, kind: f.kind, year: row[7].(int64)}) {
			continue
		}
		item, e := itemValue(row)
		if e != nil {
			return NumericTitleFilterPage{}, e
		}
		page.Items = append(page.Items, item)
		if len(page.Items) == in.View.Limit {
			break
		}
	}
	page.Exhausted = page.RawPositions == len(rows) && len(rows) < MaxRaw
	if page.Exhausted {
		page.After = nil
	}
	return page, nil
}
