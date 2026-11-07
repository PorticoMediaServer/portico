package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Stream the existing exact title order; apply the closed scalar AST only AFTER
// raw retrieval and current age/retirement admission. Filtering never hides raw
// progress or turns a cap into EOF. The eighth value adds only the native year.
func readSavedViewScalarPage(ctx context.Context, q browseQuery, in BrowseInput, f browseFence, filter rbScalarFilter) (BrowsePage, error) {
	if ctx == nil || in.Limit < 1 || in.Limit > MaxPage {
		return BrowsePage{}, ErrInput
	}
	name := "rb.saved.view.scalar.asc."
	if in.Descending {
		name = "rb.saved.view.scalar.desc."
	}
	var raw [][]any
	var e error
	started := time.Now()
	if in.After == nil {
		raw, e = q(name+"first", in.Library, in.Kind, int64(MaxRaw))
	} else {
		raw, e = q(name+"tail", in.Library, in.Kind, in.After.key, in.After.id, int64(MaxRaw))
		if e == nil && len(raw) < MaxRaw {
			var next [][]any
			next, e = q(name+"next", in.Library, in.Kind, in.After.key, int64(MaxRaw-len(raw)))
			raw = append(raw, next...)
		}
	}
	if e != nil {
		return BrowsePage{}, e
	}
	if len(raw) > MaxRaw {
		return BrowsePage{}, ErrUnqualified
	}
	p := BrowsePage{RawRows: len(raw)}
	for _, row := range raw {
		if e = ctx.Err(); e != nil {
			return BrowsePage{}, e
		}
		if len(row) != 8 {
			return BrowsePage{}, ErrUnqualified
		}
		p.RawPositions++
		p.Next = &BrowseCursor{fence: f, key: row[1].(string), id: row[0].(int64)}
		if !admittedItem(row, f) {
			continue
		}
		facts := rbFilterFacts{title: row[3].(string), library: f.library, kind: f.kind, year: row[7].(int64)}
		if !filter.matches(facts) {
			continue
		}
		item, e := itemValue(row)
		if e != nil {
			return BrowsePage{}, e
		}
		p.Items = append(p.Items, item)
		if len(p.Items) == in.Limit {
			break
		}
	}
	p.Exhausted = p.RawPositions == len(raw) && len(raw) < MaxRaw
	if p.Exhausted {
		p.Next = nil
	}
	p.LookupElapsed = time.Since(started)
	return p, nil
}

// Adds one fixed-size scalar to the existing title source. No dynamic SQL,
// provider calls, alternate AST dialect, or import of legacy DB services.
func rbSavedViewScalarDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	for _, desc := range []bool{false, true} {
		direction, op, order := "asc.", ">", "ASC"
		if desc {
			direction, op, order = "desc.", "<", "DESC"
		}
		base := "SELECT id,sort_key,public_id,title,retired,revision,admission_own_age,year FROM catalog_entities INDEXED BY catalog_entities_title WHERE library_id=? AND kind=?"
		end := " ORDER BY sort_key COLLATE NOCASE " + order + ",id " + order + " LIMIT ?"
		add := func(name, sql, seek string, p []stmt.Param, args []any) {
			out = append(out, stmt.Definition{ID: readStatementID("rb.saved.view.scalar." + direction + name), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_saved_view_scalar.go:readSavedViewScalarPage"}, SQL: sql, Params: p, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_title"}, Seeks: []string{seek}}})
		}
		add("first", base+end, "library_id=? AND kind=?", []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), int64(MaxRaw)})
		add("tail", base+" AND sort_key COLLATE NOCASE=? AND id"+op+"?"+end, "sort_key=? AND id"+op+"?", []stmt.Param{i("library"), i("kind"), s("key"), i("after"), i("limit")}, []any{int64(1), int64(1), "a", int64(1), int64(MaxRaw)})
		add("next", base+" AND sort_key COLLATE NOCASE"+op+"?"+end, "sort_key"+op+"?", []stmt.Param{i("library"), i("kind"), s("key"), i("limit")}, []any{int64(1), int64(1), "a", int64(MaxRaw)})
	}
	return out
}
