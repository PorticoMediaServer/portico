package read

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"sort"
	"strings"
	"time"
)

type RBRelationshipSort uint8

const (
	RBRelationshipAlbum RBRelationshipSort = iota + 1
	RBRelationshipArtist
)
const rbRelationshipPolicy readPolicyID = 32400

func init() {
	registerReadPolicy(readPolicyRecipe{id: rbRelationshipPolicy, domain: "rb-group-relationship", kinds: []int64{6, 7}, validate: func(_ browseQuery, in BrowseInput, f browseFence) (string, error) {
		if in.policy.subject != 0 {
			return "", ErrInput
		}
		if f.maximumAge >= 0 || f.allowUnrated == 0 {
			return "", ErrUnqualified
		}
		return "", nil
	}})
}

type RBRelationshipInput struct {
	View  BrowseInput
	Sort  RBRelationshipSort
	After *RBRelationshipCursor
}
type RBRelationshipCursor struct {
	fence browseFence
	sort  RBRelationshipSort
	key   string
	id    int64
}
type RBRelationshipPage struct {
	Items               []BrowseItem
	Next                *RBRelationshipCursor
	Exhausted           bool
	RawRows, Statements int
}
type rbRelationshipValue struct {
	item BrowseItem
	key  string
}
type rbRelationshipDraft struct {
	values    []rbRelationshipValue
	after     int64
	exhausted bool
	raw       int
	fence     browseFence
}

// RBRelationshipExact is a bounded complete-set fallback for small libraries.
// It returns nothing until source EXHAUSTION is proven; a larger library returns
// ErrNotReady for the root's leased exact manager. It never sorts a top-K sample
// and calls that a global order. No lease/cold-1M completion claim is made here.
func (r *BrowseReader) RBRelationshipExact(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RBRelationshipInput) (RBRelationshipPage, BrowseReadResult, error) {
	if r == nil {
		return RBRelationshipPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBRelationshipPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.rbRelationshipExact(ctx, request, budget, output, in)
	})
}
func (r *BrowseReader) rbRelationshipExact(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RBRelationshipInput) (page RBRelationshipPage, result BrowseReadResult, err error) {
	if in.View.After != nil || in.Sort < RBRelationshipAlbum || in.Sort > RBRelationshipArtist || in.View.Kind != 7 && (in.View.Kind != 6 || in.Sort != RBRelationshipArtist) {
		return page, result, ErrInput
	}
	if in.After != nil && in.After.sort != in.Sort {
		return page, result, ErrStale
	}
	in.View.policy = readPolicyBinding{recipe: rbRelationshipPolicy}
	name := "rb.group.relationship.ignore"
	if in.View.KeepArticles {
		name = "rb.group.relationship.keep"
	}
	// Four64-row quanta and one single-row exhaustion probe. All source rows,
	// including ineligible rows, consume the raw cap. Only complete sets sort.
	steps := make([]readStep[rbRelationshipDraft], 5)
	for window := range steps {
		window := window
		steps[window] = func(ctx context.Context, q browseQuery, f browseFence, d *rbRelationshipDraft) error {
			d.fence = f
			if in.After != nil && in.After.fence != f {
				return ErrStale
			}
			if d.exhausted {
				return nil
			}
			limit := int64(MaxRaw)
			if window == 4 {
				limit = 1
			}
			rows, e := q(name, in.View.Library, in.View.Kind, d.after, limit)
			if e != nil {
				return e
			}
			d.raw += len(rows)
			if window == 4 && len(rows) > 0 {
				return ErrNotReady
			}
			d.exhausted = len(rows) < int(limit)
			for _, row := range rows {
				if e := ctx.Err(); e != nil {
					return e
				}
				d.after = row[0].(int64)
				if !admittedItem(row, f) {
					continue
				}
				// Parent keys are values, never proof that a hidden parent or child is
				// eligible. Validate every required current ancestor before keeping a key.
				if in.View.Kind == 7 && (row[9] != int64(0) || row[10] != in.View.Library || row[11] != int64(6)) {
					continue
				}
				if in.Sort == RBRelationshipArtist && (row[12] != int64(0) || row[13] != in.View.Library || row[14] != int64(5)) {
					continue
				}
				item, e := itemValue(row)
				if e != nil {
					return e
				}
				keyIndex := 7
				if in.Sort == RBRelationshipArtist {
					keyIndex = 8
				}
				key, ok := row[keyIndex].(string)
				if !ok {
					return ErrUnqualified
				}
				d.values = append(d.values, rbRelationshipValue{item, rbRelationshipFold(key)})
			}
			return nil
		}
	}
	draft, result, err := publishRead(ctx, r.snapshots, request, budget, output, in.View, steps)
	if err != nil {
		return page, result, err
	}
	if !draft.exhausted {
		return page, result, ErrNotReady
	}
	sort.Slice(draft.values, func(i, j int) bool {
		a, b := draft.values[i], draft.values[j]
		less := a.key < b.key
		if a.key == b.key {
			less = a.item.ID < b.item.ID
		}
		if in.View.Descending {
			return !less && (a.key != b.key || a.item.ID != b.item.ID)
		}
		return less
	})
	page.RawRows = draft.raw
	page.Statements = result.LookupStatements + result.PublicationStatements
	for _, v := range draft.values {
		if in.After != nil {
			compare := strings.Compare(v.key, in.After.key)
			if compare == 0 {
				if v.item.ID < in.After.id {
					compare = -1
				} else if v.item.ID > in.After.id {
					compare = 1
				}
			}
			if !in.View.Descending && compare <= 0 || in.View.Descending && compare >= 0 {
				continue
			}
		}
		if len(page.Items) == in.View.Limit {
			page.Exhausted = false
			return page, result, nil
		}
		page.Items = append(page.Items, v.item)
		page.Next = &RBRelationshipCursor{fence: draft.fence, sort: in.Sort, key: v.key, id: v.item.ID}
	}
	page.Exhausted = true
	page.Next = nil
	return page, result, nil
}

// SQLite lower()/NOCASE folds only ASCII. Unicode strings.ToLower would change
// the public order for e.g. accented capitals and must not be substituted.
func rbRelationshipFold(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func rbRelationshipDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	for _, keep := range []bool{false, true} {
		column, name := "sort_key", "ignore"
		if keep {
			column, name = "sort_key_keep_articles", "keep"
		}
		sql := `SELECT e.id,'' AS unused,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,a.` + column + `,r.` + column + `,a.retired,a.library_id,a.kind,r.retired,r.library_id,r.kind FROM catalog_entities AS e INDEXED BY catalog_entities_library
LEFT JOIN catalog_entities AS a ON a.id=e.album_id
LEFT JOIN catalog_entities AS r ON r.id=CASE WHEN e.kind=7 THEN a.artist_id ELSE e.artist_id END
WHERE e.library_id=? AND e.kind=? AND e.id>? ORDER BY e.id LIMIT ?`
		out = append(out, stmt.Definition{ID: statementID("rb.group.relationship." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_group_relationship.go:BrowseReader.RBRelationshipExact"}, Kind: stmt.Read, Params: []stmt.Param{i("library"), i("kind"), i("after"), i("limit")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(7), int64(0), int64(MaxRaw)}}, {Name: "empty", Bindings: []any{int64(-1), int64(7), int64(0), int64(MaxRaw)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"library_id=? AND kind=? AND id>?"}}})
	}
	return out
}
