package read

import (
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This is the inert inventory for the private permitted-child aggregate recipe.
// Every identifier is a compiled constant; a request can only choose one of the
// five container sources, never SQL, columns, operators or an index.
const (
	// rbContainerMetricsRaw bounds one raw child window exactly like the common
	// browse window. Retired/foreign/unsupported rows count against it.
	rbContainerMetricsRaw = MaxRaw

	rbContainerMetricsPayloadColumns = `e.id,e.library_id,e.kind,e.retired,e.revision,e.admission_own_age,e.duration_key,e.community_rating_key,e.critic_rating_key,e.file_size_key,e.resolution_key,e.latest_aired_key,e.episode_count,p.rating,p.play_count,p.last_played_at`
	// Only these two variable octet groups are ever copied from a child row.
	rbContainerMetricsOctetColumns = `e.id,octet_length(e.latest_aired_key),COALESCE(octet_length(p.last_played_at),0)`
)

// rbContainerMetricsChildKey is one compiled member-order key. The key is part
// of the source index order, never a request expression.
type rbContainerMetricsChildKey struct {
	sql   string
	text  bool
	alias string
}

// rbContainerMetricsChildSpec is one finite container child source.
type rbContainerMetricsChildSpec struct {
	name      string
	kind      int64
	from      string
	where     string
	index     string
	seek      string
	order     string
	keys      []rbContainerMetricsChildKey
	kinds     []int64
	textFirst bool
	params    []stmt.Param
	binds     []any
}

func rbContainerMetricsChildSpecs() []rbContainerMetricsChildSpec {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	entity := "catalog_entities AS e INDEXED BY "
	return []rbContainerMetricsChildSpec{
		{
			name: "show", kind: 2,
			from:  entity + "catalog_entities_show_fk",
			where: "show_id IS NOT NULL AND e.show_id=? AND e.library_id=?",
			index: "catalog_entities_show_fk", seek: "show_id=?", order: "k0 ASC",
			keys: []rbContainerMetricsChildKey{{sql: "e.id", alias: "k0"}}, kinds: []int64{4},
			params: []stmt.Param{i("show"), i("library")}, binds: []any{int64(2000), int64(1)},
		},
		{
			name: "season", kind: 3,
			from:  entity + "catalog_entities_parent",
			where: "parent_id IS NOT NULL AND e.parent_id=? AND e.library_id=?",
			index: "catalog_entities_parent", seek: "parent_id=?", order: "k0 ASC",
			keys: []rbContainerMetricsChildKey{{sql: "e.id", alias: "k0"}}, kinds: []int64{4},
			params: []stmt.Param{i("parent"), i("library")}, binds: []any{int64(2000), int64(1)},
		},
		{
			name: "album", kind: 6,
			from:  entity + "catalog_entities_album",
			where: "album_id IS NOT NULL AND e.album_id=? AND e.library_id=?",
			index: "catalog_entities_album", seek: "album_id=?",
			order: "k0 ASC,k1 ASC,k2 ASC",
			keys: []rbContainerMetricsChildKey{
				{sql: "COALESCE(e.disc_number,1)", alias: "k0"},
				{sql: "COALESCE(e.track_number,0)", alias: "k1"},
				{sql: "e.id", alias: "k2"},
			},
			kinds:  []int64{7},
			params: []stmt.Param{i("album"), i("library")}, binds: []any{int64(2000), int64(1)},
		},
		{
			name: "book", kind: 8,
			from:  entity + "catalog_entities_book",
			where: "book_id IS NOT NULL AND e.book_id=? AND e.library_id=?",
			index: "catalog_entities_book", seek: "book_id=?",
			order: "k0 ASC,k1 ASC,k2 ASC",
			keys: []rbContainerMetricsChildKey{
				{sql: "COALESCE(e.disc_number,1)", alias: "k0"},
				{sql: "COALESCE(e.part_number,0)", alias: "k1"},
				{sql: "e.id", alias: "k2"},
			},
			kinds:  []int64{9},
			params: []stmt.Param{i("book"), i("library")}, binds: []any{int64(2000), int64(1)},
		},
		{
			name: "collection", kind: 10,
			from:  "catalog_collection_members AS m INDEXED BY catalog_collection_members_page CROSS JOIN catalog_entities AS e ON e.id=m.item_id",
			where: "m.collection_id=? AND e.library_id=?",
			index: "catalog_collection_members_page", seek: "collection_id=?", order: "k0 ASC,k1 ASC",
			keys: []rbContainerMetricsChildKey{{sql: "m.order_key", text: true, alias: "k0"}, {sql: "m.item_id", alias: "k1"}},
			// Authored collection members may only be leaf media. A nested
			// container is refused by the reducer, never silently flattened.
			kinds:     []int64{1, 4, 7, 9, 20, 21},
			params:    []stmt.Param{i("collection"), i("library")},
			binds:     []any{int64(2000), int64(1)},
			textFirst: true,
		},
	}
}

func rbContainerMetricsChildSpecFor(kind int64) (rbContainerMetricsChildSpec, bool) {
	for _, spec := range rbContainerMetricsChildSpecs() {
		if spec.kind == kind {
			return spec, true
		}
	}
	return rbContainerMetricsChildSpec{}, false
}

func rbContainerMetricsChildKind(spec rbContainerMetricsChildSpec, kind int64) bool {
	for _, k := range spec.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// rbContainerMetricsKeyColumns projects every member-order key under its compiled
// alias so ORDER BY can use the index's own expressions without a temp B-tree.
func rbContainerMetricsKeyColumns(spec rbContainerMetricsChildSpec) string {
	var out string
	for i, key := range spec.keys {
		if i > 0 {
			out += ","
		}
		out += key.sql + " AS " + key.alias
	}
	return out
}

// rbContainerMetricsSelect builds one closed statement. preds are compiled key
// predicates; requests never contribute one.
func rbContainerMetricsSelect(spec rbContainerMetricsChildSpec, projection string, preds []string, order string) string {
	where := spec.where
	for _, pred := range preds {
		where += " AND " + pred
	}
	return "SELECT " + projection + "," + rbContainerMetricsKeyColumns(spec) +
		" FROM " + spec.from +
		" LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id" +
		" WHERE " + where + " ORDER BY " + order + " LIMIT ?"
}

// rbContainerMetricsLegOrder omits the keys the leg's equality prefix already
// fixes, so the forced index supplies the order without a temporary B-tree.
func rbContainerMetricsLegOrder(spec rbContainerMetricsChildSpec, fixed int) string {
	var out string
	for i := fixed; i < len(spec.keys); i++ {
		if out != "" {
			out += ","
		}
		out += "k" + strconv.Itoa(i) + " ASC"
	}
	return out
}

// RBContainerMetricsDefinitions is an inert, independently receipted inventory.
func RBContainerMetricsDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbContainerMetricsDefinitions()...)
}

// rbContainerMetricsDefinitions is this domain's own inventory; the exported
// form also carries the shared fence harness.
func rbContainerMetricsDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, present, empty []any, p stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("rb.container_metrics." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_container_metrics.go:RBContainerMetricsPreparation.window"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	// Container source proof. The hierarchy point is one complete entity PK row;
	// the collection variant also proves no automatic/inferred membership exists,
	// because an authored walk cannot see those members.
	add("point", `SELECT id,library_id,kind,retired,public_id,metadata_incarnation,revision,source_revision,identity_revision,metadata_revision,tv_hierarchy_revision,music_membership_revision,collection_revision,aggregate_revision FROM catalog_entities WHERE id=? AND library_id=? AND kind=?`, []stmt.Param{i("subject"), i("library"), i("kind")}, []any{int64(2001), int64(1), int64(2)}, []any{int64(999999), int64(1), int64(2)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}})
	add("point.collection", `SELECT e.id,e.library_id,e.kind,e.retired,e.public_id,e.metadata_incarnation,e.revision,e.source_revision,e.identity_revision,e.metadata_revision,e.tv_hierarchy_revision,e.music_membership_revision,e.collection_revision,e.aggregate_revision FROM catalog_entities AS e LEFT JOIN catalog_automatic_collections AS a INDEXED BY catalog_automatic_collection_entity ON a.entity_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? LIMIT 2`, []stmt.Param{i("subject"), i("library"), i("kind")}, []any{int64(2001), int64(1), int64(10)}, []any{int64(999999), int64(1), int64(10)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Indexes: []string{"catalog_automatic_collection_entity"}, Seeks: []string{"entity_id=?"}})

	for _, spec := range rbContainerMetricsChildSpecs() {
		plan := stmt.Plan{Hot: true, Indexes: []string{spec.index, "personal_current_live"}, Seeks: []string{spec.seek, "profile_id=? AND item_id=?"}}
		if spec.name == "collection" {
			plan.PrimaryKeys = []string{"e"}
		}
		addChild := func(leg string, preds []string, keyParams []stmt.Param, keyBinds []any) {
			// The personal LEFT JOIN's profile binds first, before spec.where.
			params := append([]stmt.Param{s("profile")}, spec.params...)
			params = append(params, keyParams...)
			params = append(params, i("limit"))
			legPlan := func(name string) stmt.Plan {
				p := plan
				p.Waivers = append(append([]stmt.Waiver(nil), plan.Waivers...), stmt.Waiver{
					StatementID: readStatementID("rb.container_metrics.children." + spec.name + "." + name),
					Stage:       "eqp", Form: "BLOOM FILTER",
					Match:  "BLOOM FILTER ON p (profile_id=? AND item_id=?)",
					Reason: "bundled planner join bloom filter; the current-personal index seek is still pinned",
				})
				if spec.name == "album" || spec.name == "book" {
					// These authored-order indexes are expression indexes, so the
					// bounded keyset EQP names <expr> instead of a column. Waive that
					// exact reviewed detail; the index and seek stay pinned.
					fk := "album_id=?"
					if spec.name == "book" {
						fk = "book_id=?"
					}
					parts := []string{fk}
					for k := range keyParams {
						column, op := "<expr>", "=?"
						if k == 2 {
							column = "id"
						}
						if k == len(keyParams)-1 {
							op = ">?"
						}
						parts = append(parts, column+op)
					}
					p.Waivers = append(p.Waivers, stmt.Waiver{
						StatementID: readStatementID("rb.container_metrics.children." + spec.name + "." + name),
						Stage:       "eqp", Form: "UNRECOGNIZED EQP",
						Match:  "SEARCH e USING INDEX " + spec.index + " (" + strings.Join(parts, " AND ") + ")",
						Reason: "reviewed expression-index keyset for the authored album/book child order",
					})
				}
				return p
			}
			present := append([]any{"p"}, spec.binds...)
			present = append(present, keyBinds...)
			present = append(present, int64(rbContainerMetricsRaw+1))
			empty := append([]any(nil), present...)
			empty[1] = int64(-1)
			name := "children." + spec.name + "." + leg
			order := spec.order
			if leg != "first" {
				order = rbContainerMetricsLegOrder(spec, len(keyParams)-1)
			}
			add(name, rbContainerMetricsSelect(spec, rbContainerMetricsPayloadColumns, preds, order), params, present, empty, legPlan(leg))
			add(name+".bytes", rbContainerMetricsSelect(spec, rbContainerMetricsOctetColumns, preds, order), params, present, empty, legPlan(leg+".bytes"))
		}
		if spec.textFirst {
			addChild("first", nil, nil, nil)
		}
		for level := 0; level < len(spec.keys); level++ {
			var preds []string
			var keyParams []stmt.Param
			var keyBinds []any
			for k := 0; k <= level; k++ {
				op := "="
				if k == level {
					op = ">"
				}
				preds = append(preds, spec.keys[k].sql+op+"?")
				name := "key" + strconv.Itoa(k)
				if spec.keys[k].text {
					keyParams = append(keyParams, stmt.Param{Name: name, Type: stmt.Text})
					keyBinds = append(keyBinds, "a")
				} else {
					keyParams = append(keyParams, i(name))
					keyBinds = append(keyBinds, int64(0))
				}
			}
			addChild("tail"+strconv.Itoa(level), preds, keyParams, keyBinds)
		}
	}
	return out
}
