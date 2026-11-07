package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type HomeLeafInput struct {
	View  BrowseInput
	Work  int64
	After *HomeLeafCursor
}
type HomeLeafCursor struct {
	fence    browseFence
	work, id int64
	source   []any
}
type HomeLeafCandidate struct {
	Item   BrowseItem
	Added  string
	Season int64
}
type HomeLeafPage struct {
	Candidates            []HomeLeafCandidate
	Next                  *HomeLeafCursor
	Exhausted             bool
	RawRows, RawPositions int
}

// HomeLeafWindow is the complete-reachability input to large-show preparation.
// It proves current own-leaf content permission, NOT asset availability or a
// ready recent target. Preparation must resolve candidate availability and
// finish the delivery/visible-clock reducer before publishing a row.
func (r *BrowseReader) HomeLeafWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeLeafInput) (HomeLeafPage, BrowseReadResult, error) {
	if r == nil {
		return HomeLeafPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (HomeLeafPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.homeLeafWindow(ctx, request, budget, output, in)
	})
}
func (r *BrowseReader) homeLeafWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeLeafInput) (HomeLeafPage, BrowseReadResult, error) {
	if in.Work <= 0 || !validHomeView(in.View) || in.View.Kind != 2 && in.View.Kind != 3 {
		return HomeLeafPage{}, BrowseReadResult{}, ErrInput
	}
	in.View = homeView(in.View)
	var source []any
	root := func(q browseQuery) ([]any, error) {
		rows, e := q("rb.home.item", in.Work, in.View.Library, in.View.Kind)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 || rows[0][4].(int64) != 0 {
			return nil, ErrDenied
		}
		envelope, e := homeLibraryRows(q, in.View.TokenHash, in.View.Library)
		if e != nil {
			return nil, e
		}
		source = append([]any(nil), rows[0]...)
		for _, row := range envelope {
			source = append(source, row...)
		}
		return source, nil
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[HomeLeafPage]{
		func(_ context.Context, _ browseQuery, f browseFence, _ *HomeLeafPage) error {
			if in.After != nil && (in.After.fence != f || in.After.work != in.Work || !equalSource(in.After.source, source)) {
				return ErrStale
			}
			return nil
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *HomeLeafPage) error {
			after := int64(0)
			if in.After != nil {
				after = in.After.id
			}
			name := "rb.home.source.show"
			if in.View.Kind == 3 {
				name = "rb.home.source.season"
			}
			rows, e := q(name, in.Work, after, int64(MaxRaw))
			if e != nil {
				return e
			}
			d.RawRows = len(rows)
			for _, row := range rows {
				if e := ctx.Err(); e != nil {
					return e
				}
				d.RawPositions++
				d.Next = &HomeLeafCursor{fence: f, work: in.Work, id: row[0].(int64), source: source}
				if row[7].(int64) != 4 || row[8].(int64) != in.View.Library || !admittedItem(row, f) {
					continue
				}
				item, e := itemValue(row)
				if e != nil {
					return e
				}
				c := HomeLeafCandidate{Item: item}
				if row[1] != nil {
					c.Added = row[1].(string)
				}
				if row[9] != nil {
					c.Season = row[9].(int64)
				}
				d.Candidates = append(d.Candidates, c)
				if len(d.Candidates) == in.View.Limit {
					break
				}
			}
			d.Exhausted = d.RawPositions == len(rows) && len(rows) < MaxRaw
			if d.Exhausted {
				d.Next = nil
			}
			return nil
		},
	}, root)
}

func rbHomeMemberDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	for _, v := range []struct{ name, column, index string }{{"show", "show_id", "catalog_entities_show_fk"}, {"season", "parent_id", "catalog_entities_parent"}} {
		out = append(out, stmt.Definition{ID: readStatementID("rb.home.source." + v.name), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_home_members.go:BrowseReader.HomeLeafWindow"}, SQL: "SELECT e.id,e.added_at,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,e.library_id,e.season_id FROM catalog_entities AS e INDEXED BY " + v.index + " WHERE " + v.column + " IS NOT NULL AND e." + v.column + "=? AND e.id>? ORDER BY e.id LIMIT ?", Params: []stmt.Param{i("parent"), i("after"), i("limit")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(2000), int64(0), int64(MaxRaw)}}, {Name: "empty", Bindings: []any{int64(999999), int64(0), int64(MaxRaw)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{v.index}, Seeks: []string{v.column + "=? AND id>?"}}})
	}
	return out
}
