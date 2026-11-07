package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"hash"
	"sort"
	"time"
)

type RBTermField uint8

const (
	RBTermDecade RBTermField = iota + 1
	RBTermGenre
)

// RBTermSmallInput requests holder groups only: movie/show decades or song
// genres. Member hydration preserves numeric source-ID order; it does not claim
// album/artist lifting or a public relationship groupOrder implementation.
// The complete fallback refuses more than 64 raw holders, 16 term sources per
// admitted holder, 256 combined raw rows, 512-byte group labels, or 256KiB of
// conservatively charged retained native values/maps/DTOs. Rejected holder rows
// and term rows consume the same raw budget. ErrNotReady means an incomplete
// tail/capacity refusal, with no partial groups or sampled exact count.
//
// Source contract: B-READS group naming; compactcatalog/facet_holder_stage.go
// owns nonempty COALESCE(source override,dictionary label), no whitespace trim,
// own movie/show year 1800..2199, no child aggregation. This harness reads those
// facts natively and fences the authoritative metadata heads plus actual library
// preference facts in a fresh publication snapshot. The transcript includes
// rejected raw rows and explicit strict-key exhaustion probes.
type RBTermSmallInput struct {
	View    BrowseInput
	Field   RBTermField
	Members bool
	Value   string
}
type RBTermGroup struct {
	ID, Value, Title string
	Count            int
	MemberIDs        []int64
	key              string
}

// A successful result is complete within the declared source bounds. It has no
// lifting, contextual filters, count/activity sorting, range or wire registration.
type RBTermSmallResult struct {
	Groups                   []RBTermGroup
	Members                  []BrowseItem
	RawFetched, RawPositions int
	Exhausted                bool
	fence                    browseFence
	source                   [32]byte
}
type rbTermSmallWork struct {
	entities   [][]any
	groups     map[string]map[int64]bool
	items      map[int64]BrowseItem
	raw, bytes int
	after      int64
	hash       hash.Hash
	fence      browseFence
}

func rbTermSmallPolicy(field RBTermField) readPolicyID { return 32550 + readPolicyID(field) }
func init() {
	for _, field := range []RBTermField{RBTermDecade, RBTermGenre} {
		field := field
		kinds := []int64{1, 2}
		if field == RBTermGenre {
			kinds = []int64{7}
		}
		registerReadPolicy(readPolicyRecipe{id: rbTermSmallPolicy(field), domain: fmt.Sprint("rb-term-small-", field), kinds: kinds, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
			if in.policy.subject != 0 {
				return "", ErrInput
			}
			if f.maximumAge >= 0 || f.allowUnrated == 0 {
				return "", ErrUnqualified
			}
			rows, e := q("rb.term.library", f.library)
			if e != nil {
				return "", e
			}
			if len(rows) != 1 {
				return "", ErrDenied
			}
			kind := rows[0][1].(string)
			if field == RBTermGenre && kind != "music" || field == RBTermDecade && (in.Kind == 1 && kind != "movie" || in.Kind == 2 && kind != "tv" && kind != "anime") {
				return "", ErrUnqualified
			}
			return fmt.Sprint(rows[0]), nil
		}})
	}
}
func (r *BrowseReader) RBTermSmall(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in RBTermSmallInput) (RBTermSmallResult, BrowseReadResult, error) {
	if r == nil {
		return RBTermSmallResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, req, budget, out, r.pool, in.View.Now, func(ctx context.Context, req governor.Request, budget time.Duration, at time.Time) (RBTermSmallResult, BrowseReadResult, error) {
		in.View.Now = at
		return r.rbTermSmall(ctx, req, budget, out, in)
	})
}
func (r *BrowseReader) rbTermSmall(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in RBTermSmallInput) (RBTermSmallResult, BrowseReadResult, error) {
	if in.Field != RBTermDecade && in.Field != RBTermGenre || in.View.After != nil || in.Members && in.Value == "" || in.Field == RBTermDecade && in.View.Kind != 1 && in.View.Kind != 2 || in.Field == RBTermGenre && in.View.Kind != 7 {
		return RBTermSmallResult{}, BrowseReadResult{}, ErrInput
	}
	in.View.policy = readPolicyBinding{recipe: rbTermSmallPolicy(in.Field)}
	steps := []readStep[rbTermSmallWork]{func(_ context.Context, q browseQuery, f browseFence, d *rbTermSmallWork) error {
		d.fence = f
		d.hash = sha256.New()
		d.groups = map[string]map[int64]bool{}
		d.items = map[int64]BrowseItem{}
		d.bytes = 4096 + 2*len(fmt.Sprint(f))
		rows, e := q("rb.term.entities", in.View.Library, in.View.Kind, int64(0), int64(MaxRaw))
		if e != nil {
			return e
		}
		if e = rbTermRecord(d, "entities", rows); e != nil {
			return e
		}
		d.entities = rows
		if len(rows) > 0 {
			d.after = rows[len(rows)-1][0].(int64)
		}
		return nil
	}, func(_ context.Context, q browseQuery, f browseFence, d *rbTermSmallWork) error {
		// An explicit strict-key single-row probe proves the entire holder stream
		// exhausted, even if the first raw batch was short or entirely rejected.
		rows, e := q("rb.term.entities", in.View.Library, in.View.Kind, d.after, int64(1))
		if e != nil {
			return e
		}
		if e = rbTermRecord(d, "entity-eof", rows); e != nil {
			return e
		}
		if len(rows) != 0 {
			return ErrNotReady
		}
		return nil
	}}
	for index := 0; index < MaxRaw; index++ {
		index := index
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *rbTermSmallWork) error {
			if index >= len(d.entities) {
				return nil
			}
			v := d.entities[index]
			if !admittedItem(v, f) || v[8] != nil {
				return nil
			}
			item, e := itemValue(v)
			if e != nil {
				return e
			}
			d.items[item.ID] = item
			d.bytes += 128 + 2*len(item.Title)
			if d.bytes > 256<<10 {
				return ErrNotReady
			}
			if in.Field == RBTermDecade {
				value, ok := rbDecadeValue(v[7].(int64))
				if ok {
					return rbTermAdd(d, value, item.ID)
				}
				return nil
			}
			rows, e := q("rb.term.sources", item.ID, int64(16))
			if e != nil {
				return e
			}
			if e = rbTermRecord(d, "terms", rows); e != nil {
				return e
			}
			if len(rows) > 0 {
				last := rows[len(rows)-1]
				tail, e := q("rb.term.source-tail", item.ID, last[0], last[1], int64(1))
				if e != nil {
					return e
				}
				if e = rbTermRecord(d, "term-tail-eof", tail); e != nil {
					return e
				}
				if len(tail) > 0 {
					return ErrNotReady
				}
				next, e := q("rb.term.source-next", item.ID, last[0], int64(1))
				if e != nil {
					return e
				}
				if e = rbTermRecord(d, "term-next-eof", next); e != nil {
					return e
				}
				if len(next) > 0 {
					return ErrNotReady
				}
			}
			for _, term := range rows {
				if e := ctx.Err(); e != nil {
					return e
				}
				if term[2] != int64(1) || term[3] == "" {
					continue
				}
				if e = rbTermAdd(d, term[3].(string), item.ID); e != nil {
					return e
				}
			}
			return nil
		})
	}
	d, res, e := publishRead(ctx, r.snapshots, req, budget, out, in.View, steps)
	if e != nil {
		return RBTermSmallResult{}, res, e
	}
	result := RBTermSmallResult{RawFetched: d.raw, RawPositions: d.raw, Exhausted: true, fence: d.fence}
	copy(result.source[:], d.hash.Sum(nil))
	for value, members := range d.groups {
		g := RBTermGroup{Value: value, Title: value, Count: len(members)}
		prefix := "genre:"
		if in.Field == RBTermDecade {
			prefix = "decade:"
			g.Title += "s"
		}
		g.ID = prefix + value
		g.key = sorttext.Key(value, "", "")
		d.bytes += 192 + 2*len(g.ID) + 2*len(g.Title) + 2*len(g.key)
		if d.bytes > 256<<10 {
			return RBTermSmallResult{}, res, ErrNotReady
		}
		for id := range members {
			g.MemberIDs = append(g.MemberIDs, id)
		}
		sort.Slice(g.MemberIDs, func(i, j int) bool { return g.MemberIDs[i] < g.MemberIDs[j] })
		result.Groups = append(result.Groups, g)
	}
	sort.Slice(result.Groups, func(i, j int) bool {
		a, b := result.Groups[i], result.Groups[j]
		if in.View.Descending {
			a, b = b, a
		}
		if a.key != b.key {
			return a.key < b.key
		}
		return a.Value < b.Value
	})
	if in.Members {
		for id := range d.groups[in.Value] {
			result.Members = append(result.Members, d.items[id])
		}
		sort.Slice(result.Members, func(i, j int) bool {
			a, b := result.Members[i].ID, result.Members[j].ID
			if in.View.Descending {
				return a > b
			}
			return a < b
		})
	}
	return result, res, nil
}
func rbTermAdd(d *rbTermSmallWork, value string, id int64) error {
	if len(value) > 512 {
		return ErrNotReady
	}
	group := d.groups[value]
	if group == nil {
		group = map[int64]bool{}
		d.groups[value] = group
		d.bytes += 128 + 2*len(value)
	}
	if !group[id] {
		group[id] = true
		d.bytes += 64
	}
	if d.bytes > 256<<10 {
		return ErrNotReady
	}
	return nil
}

// Account actual native value lengths, retained DTO overlap, fixed row/map
// overhead and the exact raw transcript, including rejected rows and EOF reads.
func rbTermRecord(d *rbTermSmallWork, tag string, rows [][]any) error {
	d.raw += len(rows)
	if d.raw > 256 {
		return ErrNotReady
	}
	for _, row := range rows {
		d.bytes += 64 + 32*len(row)
		for _, v := range row {
			switch v := v.(type) {
			case string:
				d.bytes += 2 * len(v)
			case []byte:
				d.bytes += 3 * len(v)
			}
		}
	}
	if d.bytes > 256<<10 {
		return ErrNotReady
	}
	b, e := json.Marshal([]any{tag, rows})
	if e != nil {
		return e
	}
	d.hash.Write(b)
	return nil
}
func RBTermSmallDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var defs []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings, empty []any, p stmt.Plan) {
		defs = append(defs, stmt.Definition{ID: statementID("rb.term." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_group_term_small.go:BrowseReader.RBTermSmall"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	add("library", `SELECT id,kind,preference_present,preference_revision,preference_local_tags,data_revision,data_order_revision,data_metadata_revision FROM libraries INDEXED BY sqlite_autoindex_libraries_1 WHERE id=?`, []stmt.Param{s("library")}, []any{"movies"}, []any{"absent"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}})
	add("entities", `SELECT e.id,'',e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.year,g.item_id FROM catalog_entities AS e INDEXED BY catalog_entities_library LEFT JOIN dvr_catalog_retirements AS g ON g.item_id=e.id WHERE e.library_id=? AND e.kind=? AND e.id>? ORDER BY e.id LIMIT ?`, []stmt.Param{i("library"), i("kind"), i("after"), i("limit")}, []any{int64(1), int64(1), int64(0), int64(64)}, []any{int64(-1), int64(1), int64(0), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}, PrimaryKeys: []string{"g"}})
	base := `SELECT s.provider,s.source_id,t.vocab,COALESCE(s.label_override,t.label),s.term_id FROM catalog_term_sources AS s CROSS JOIN catalog_terms AS t ON t.id=s.term_id WHERE s.entity_id=?`
	end := ` ORDER BY s.provider,s.source_id LIMIT ?`
	add("sources", base+end, []stmt.Param{i("entity"), i("limit")}, []any{int64(1), int64(16)}, []any{int64(-1), int64(16)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "t"}})
	add("source-tail", base+` AND s.provider=? AND s.source_id>?`+end, []stmt.Param{i("entity"), s("provider"), s("after"), i("limit")}, []any{int64(1), "owner", "a", int64(1)}, []any{int64(-1), "owner", "a", int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "t"}})
	add("source-next", base+` AND s.provider>?`+end, []stmt.Param{i("entity"), s("provider"), i("limit")}, []any{int64(1), "a", int64(1)}, []any{int64(-1), "a", int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "t"}})
	return defs
}
