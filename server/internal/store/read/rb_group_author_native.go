package read

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"sort"
	"time"
)

const rbAuthorPolicy readPolicyID = 32401

func init() {
	registerReadPolicy(readPolicyRecipe{id: rbAuthorPolicy, domain: "rb-author-complete", kinds: []int64{9}, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
		if f.maximumAge >= 0 || f.allowUnrated == 0 {
			return "", ErrUnqualified
		}
		rows, e := q("rb.group.subject", in.policy.subject)
		if e != nil {
			return "", e
		}
		if len(rows) != 1 || rows[0][1] != in.Library || rows[0][2] != int64(13) || rows[0][3] != int64(0) {
			return "", ErrDenied
		}
		return fmt.Sprint(rows[0]), nil
	}})
}

type RBAuthorScope struct {
	Items   []BrowseItem
	RawRows int
	fence   browseFence
}
type rbAuthorNativeDraft struct {
	books [][]any
	name  string
	rows  []rbAuthorOrderRow
	items map[int64]BrowseItem
	keys  []any
	done  bool
	raw   int
	fence browseFence
}

// RBAuthorExact returns a complete small direct-part scope, never lifted browse
// entities. Larger scopes refuse; root owns large preparation/cache and lifting.
func (r *BrowseReader) RBAuthorExact(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, view BrowseInput, author int64) (RBAuthorScope, BrowseReadResult, error) {
	if r == nil {
		return RBAuthorScope{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, req, budget, out, r.pool, view.Now, func(ctx context.Context, req governor.Request, budget time.Duration, at time.Time) (RBAuthorScope, BrowseReadResult, error) {
		view.Now = at
		return r.rbAuthorExact(ctx, req, budget, out, view, author)
	})
}
func (r *BrowseReader) rbAuthorExact(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, view BrowseInput, author int64) (RBAuthorScope, BrowseReadResult, error) {
	if author <= 0 || view.Kind != 9 || view.After != nil {
		return RBAuthorScope{}, BrowseReadResult{}, ErrInput
	}
	view.policy = readPolicyBinding{recipe: rbAuthorPolicy, subject: author}
	steps := []readStep[rbAuthorNativeDraft]{func(_ context.Context, q browseQuery, f browseFence, d *rbAuthorNativeDraft) error {
		rows, e := q("rb.group.subject", author)
		if e != nil {
			return e
		}
		d.name = rows[0][13].(string)
		d.fence = f
		d.items = map[int64]BrowseItem{}
		return nil
	}, func(_ context.Context, q browseQuery, f browseFence, d *rbAuthorNativeDraft) error {
		name := "rb.group.author.books.ignore"
		if view.KeepArticles {
			name = "rb.group.author.books.keep"
		}
		rows, e := q(name, view.Library, d.name, int64(17))
		if e != nil {
			return e
		}
		if len(rows) > 16 {
			return ErrNotReady
		}
		d.books = rows
		return nil
	}}
	// Each book has four raw quanta and an exhaustion quantum. Empty steps use
	// no SQL. At most16 source books and256 raw parts; rejected rows consume caps.
	for book := 0; book < 16; book++ {
		for window := 0; window < 5; window++ {
			book, window := book, window
			steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *rbAuthorNativeDraft) error {
				if book >= len(d.books) {
					return nil
				}
				b := d.books[book]
				if window == 0 {
					d.keys = nil
					d.done = false
				}
				if d.done {
					return nil
				}
				if !admittedItem(b, f) {
					d.done = true
					return nil
				}
				var rows [][]any
				var e error
				if d.keys == nil {
					rows, e = q("rb.group.book-parts.asc.first", b[0], int64(64))
				} else {
					for level := 2; level >= 0 && len(rows) < 64; level-- {
						args := []any{b[0]}
						args = append(args, d.keys[:level+1]...)
						args = append(args, int64(64-len(rows)))
						more, err := q(fmt.Sprintf("rb.group.book-parts.asc.seek%d", level), args...)
						if err != nil {
							return err
						}
						rows = append(rows, more...)
					}
				}
				if e != nil {
					return e
				}
				d.raw += len(rows)
				if d.raw > 256 || window == 4 && len(rows) > 0 {
					return ErrNotReady
				}
				d.done = len(rows) < 64
				for _, p := range rows {
					if e := ctx.Err(); e != nil {
						return e
					}
					d.keys = append([]any(nil), p[9:]...)
					if !admittedItem(p, f) || p[7] != view.Library || p[8] != int64(9) {
						continue
					}
					item, e := itemValue(p)
					if e != nil {
						return e
					}
					var pos *float64
					if b[8] != nil {
						v := b[8].(float64)
						pos = &v
					}
					d.rows = append(d.rows, rbAuthorOrderRow{Book: b[0].(int64), Part: item.ID, Series: b[7].(string), Position: pos, TitleKey: b[9].(string), Disc: p[9].(int64), Number: p[10].(int64)})
					d.items[item.ID] = item
				}
				return nil
			})
		}
	}
	d, res, e := publishRead(ctx, r.snapshots, req, budget, out, view, steps)
	if e != nil {
		return RBAuthorScope{}, res, e
	}
	sort.Slice(d.rows, func(i, j int) bool {
		a, b := d.rows[i], d.rows[j]
		if view.Descending {
			a, b = b, a
		}
		return rbAuthorOrderLess(a, b)
	})
	result := RBAuthorScope{RawRows: d.raw, fence: d.fence}
	for _, row := range d.rows {
		result.Items = append(result.Items, d.items[row.Part])
	}
	return result, res, nil
}

// RBAuthorDefinitions composes with RBGroupDefinitions; no shared registry edit.
func RBAuthorDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var defs []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, index string) {
		empty := append([]any(nil), bindings...)
		empty[0] = int64(-1)
		defs = append(defs, stmt.Definition{ID: statementID("rb.group.author." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_group_author_native.go:BrowseReader.RBAuthorExact"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: []string{index}}})
	}
	for _, keep := range []bool{false, true} {
		key, name := "sort_key", "ignore"
		if keep {
			key, name = "sort_key_keep_articles", "keep"
		}
		add("books."+name, `SELECT id,'',public_id,title,retired,revision,admission_own_age,series,CAST(NULLIF(series_position,'') AS REAL),`+key+` FROM catalog_entities INDEXED BY catalog_entities_book_author WHERE kind=8 AND library_id=? AND author COLLATE NOCASE=? ORDER BY id LIMIT ?`, []stmt.Param{i("library"), s("author"), i("limit")}, []any{int64(1), "Author", int64(17)}, "catalog_entities_book_author")
	}
	return defs
}
