package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RSRelatedDefinitions is inert. All candidate LIMITs apply to source rows,
// before eligibility/reduction. Nothing installs these in a product registry.
// Source walks return numeric facts and schema-bounded 16-byte identities.
// Wide identity/card points are separately byte-preflighted.
func RSRelatedDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args []any, p stmt.Plan) {
		empty := append([]any(nil), args...)
		empty[0] = int64(999999)
		out = append(out, stmt.Definition{ID: statementID("rs.related." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_related.go:relatedQuantum"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	point := stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "k", "h"}, Indexes: []string{"personal_current_live"}}
	add("entity", `SELECT e.id,e.public_id,e.library_id,e.kind,e.retired,e.revision,e.admission_own_age,k.container,e.year,e.community_rating_key,e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,e.credit_image_id,e.credit_owner_revision,h.sealed,h.revision,e.parent_id,e.show_id,e.album_id,e.book_id,e.artist_id,p.revision,p.not_interested,p.rating,p.watched,p.favorite,p.watchlisted,p.play_count,e.artwork_revision,e.admission_revision,e.aggregate_revision
FROM catalog_entities AS e CROSS JOIN catalog_kinds AS k ON k.id=e.kind
LEFT JOIN catalog_credit_images AS h ON h.id=e.credit_image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision
LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id AND p.item_public_id=e.public_id
WHERE e.id=?`, []stmt.Param{s("profile"), i("entity")}, []any{"p", int64(1)}, point)
	// Entity has profile first; empty cohort must miss the point, not just state.
	out[len(out)-1].Cohorts[1].Bindings = []any{"p", int64(999999)}
	add("terms", `SELECT m.term_id,t.vocab FROM catalog_entity_terms AS m CROSS JOIN catalog_terms AS t ON t.id=m.term_id WHERE m.entity_id=? AND m.term_id>? ORDER BY m.term_id LIMIT ?`, []stmt.Param{i("entity"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(64)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"m", "t"}})
	add("term.members", `SELECT entity_id,0,0 FROM catalog_entity_terms INDEXED BY catalog_term_entities WHERE term_id=? AND entity_id>? ORDER BY entity_id LIMIT ?`, []stmt.Param{i("term"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_term_entities"}})
	add("credits", `SELECT ord,person_id,department_id,role_id,length(provider_person_id) FROM catalog_credits INDEXED BY catalog_credits_entity WHERE entity_id=? AND image_id=? AND ord>? ORDER BY ord LIMIT ?`, []stmt.Param{i("entity"), i("image"), i("after"), i("limit")}, []any{int64(1), int64(1), int64(-1), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_credits_entity"}})
	for _, r := range []struct{ name, predicate string }{{"person", "a.person_id=b.person_id"}, {"provider", "a.provider<>'' AND a.provider_person_id<>'' AND a.provider=b.provider AND a.provider_person_id=b.provider_person_id"}, {"name", "a.credited_name<>'' AND a.credited_name=b.credited_name"}} {
		add("credit.equal."+r.name, `SELECT a.ord FROM catalog_credits AS a CROSS JOIN catalog_credits AS b ON b.image_id=? AND b.ord=? WHERE a.image_id=? AND a.ord=? AND `+r.predicate, []stmt.Param{i("other_image"), i("other"), i("image"), i("ordinal")}, []any{int64(1), int64(0), int64(1), int64(0)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "b"}})
		out[len(out)-1].Cohorts[1].Bindings = []any{int64(1), int64(0), int64(999999), int64(0)}
		if r.name == "provider" {
			out[len(out)-1].Cohorts[0].Bindings = []any{int64(1), int64(1), int64(1), int64(1)}
		}
	}
	for _, route := range []struct{ name, index, predicate, extra string }{
		{"person", "catalog_credits_person", "c.person_id=a.person_id", ""},
		{"provider", "catalog_credits_provider_person", "c.provider=a.provider AND c.provider_person_id=a.provider_person_id", " AND c.provider_person_id<>''"},
		{"name", "catalog_credits_name", "c.credited_name=a.credited_name", " AND a.credited_name<>''"},
	} {
		base := `SELECT c.entity_id,c.ord,c.image_id FROM catalog_credits AS a CROSS JOIN catalog_credits AS c INDEXED BY ` + route.index + ` ON ` + route.predicate + ` WHERE a.image_id=? AND a.ord=?` + route.extra
		if route.name == "provider" {
			base = `SELECT c.entity_id,c.ord,c.image_id FROM catalog_credits AS c INDEXED BY catalog_credits_provider_person WHERE c.provider=? AND c.provider_person_id=? AND provider_person_id<>''`
		}
		for _, seek := range []struct {
			name, where, order string
			params             []stmt.Param
			args               []any
		}{
			{"image_tail", " AND c.entity_id=? AND c.ord=? AND c.image_id>?", "c.image_id", []stmt.Param{i("entity_after"), i("ordinal_after"), i("image_after")}, []any{int64(1), int64(0), int64(0)}},
			{"ordinal_tail", " AND c.entity_id=? AND c.ord>?", "c.ord,c.image_id", []stmt.Param{i("entity_after"), i("ordinal_after")}, []any{int64(1), int64(-1)}},
			{"next", " AND c.entity_id>?", "c.entity_id,c.ord,c.image_id", []stmt.Param{i("entity_after")}, []any{int64(0)}},
		} {
			prefixParams := []stmt.Param{i("image"), i("ordinal")}
			if route.name == "provider" {
				prefixParams = []stmt.Param{s("provider"), s("person")}
			}
			params := append(prefixParams, seek.params...)
			params = append(params, i("limit"))
			ord := int64(0)
			if route.name == "provider" {
				ord = 1
			}
			if route.name == "name" {
				ord = 2
			}
			prefixArgs := []any{int64(1), ord}
			if route.name == "provider" {
				prefixArgs = []any{"nfo", "provider-person"}
			}
			args := append(prefixArgs, seek.args...)
			args = append(args, int64(64))
			if seek.name == "image_tail" {
				args[3] = ord
			}
			p := stmt.Plan{Hot: true, PrimaryKeys: []string{"a"}, Indexes: []string{route.index}}
			if route.name == "provider" {
				p.PrimaryKeys = nil
			}
			add("credit."+route.name+"."+seek.name, base+seek.where+" ORDER BY "+seek.order+" LIMIT ?", params, args, p)
			if route.name == "provider" {
				out[len(out)-1].Cohorts[1].Bindings[0] = "missing"
			}
		}
	}
	add("collections", `SELECT collection_id FROM catalog_collection_members INDEXED BY catalog_collection_members_item WHERE item_id=? AND collection_id>? ORDER BY collection_id LIMIT ?`, []stmt.Param{i("entity"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_item"}})
	add("collection.members", `SELECT item_id,0,0 FROM catalog_collection_members WHERE collection_id=? AND item_id>? ORDER BY item_id LIMIT ?`, []stmt.Param{i("collection"), i("after"), i("limit")}, []any{int64(2000), int64(0), int64(64)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_collection_members"}})
	// A local identity is optional. Equality is evaluated natively; no unbounded
	// identity string is copied into the reducer or cursor.
	add("local.members", `SELECT e.id,0,0 FROM catalog_entities AS a CROSS JOIN catalog_entities AS e INDEXED BY catalog_entities_local ON e.kind=a.kind AND e.local_key=a.local_key WHERE a.id=? AND a.local_key<>'' AND e.id>? ORDER BY e.id LIMIT ?`, []stmt.Param{i("entity"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(64)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"a"}, Indexes: []string{"catalog_entities_local"}})
	for _, r := range []struct{ name, column, index string }{{"parent", "parent_id", "catalog_entities_parent"}, {"show", "show_id", "catalog_entities_show_fk"}, {"artist", "artist_id", "catalog_entities_artist"}} {
		add(r.name+".members", `SELECT id,0,0 FROM catalog_entities INDEXED BY `+r.index+` WHERE `+r.column+` IS NOT NULL AND `+r.column+`=? AND id>? ORDER BY id LIMIT ?`, []stmt.Param{i("owner"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{r.index}})
	}
	// Exact identity comparison is a point on both entities; external-ID rows
	// are individually walked by the provider equivalence statement below.
	add("local.equal", `SELECT a.id FROM catalog_entities AS a NOT INDEXED CROSS JOIN catalog_entities AS b NOT INDEXED ON b.id=? WHERE a.id=? AND a.local_key<>'' AND a.kind=b.kind AND a.local_key=b.local_key`, []stmt.Param{i("other"), i("entity")}, []any{int64(1), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "b"}})
	// Raw external identities are compared inside indexed SQL. A full window
	// without equality refuses rather than guessing absent identity.
	// Provider IDs are not required by any local generator.
	add("provider.equal", `SELECT b.entity_id FROM metadata_entity_external_ids AS a LEFT JOIN metadata_entity_external_ids AS b INDEXED BY sqlite_autoindex_metadata_entity_external_ids_1 ON b.entity_id=? AND b.provider=a.provider AND b.subject_kind=a.subject_kind AND b.provider_id=a.provider_id WHERE a.entity_id=? ORDER BY a.provider,a.subject_kind,a.provider_id LIMIT 64`, []stmt.Param{i("other"), i("entity")}, []any{int64(1), int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_entity_external_ids_1"}, PrimaryKeys: []string{"a"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "metadata_entity_external_ids", Name: "sqlite_autoindex_metadata_entity_external_ids_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "entity_id", Collation: "BINARY"}, {Name: "provider", Collation: "BINARY"}, {Name: "subject_kind", Collation: "BINARY"}, {Name: "provider_id", Collation: "BINARY"}}}}})
	out[len(out)-1].Cohorts[1].Bindings = []any{int64(1), int64(999999)}
	// Availability consumes raw link positions before current asset filtering.
	add("assets", `SELECT l.asset_id,l.available,a.available FROM catalog_asset_links AS l CROSS JOIN catalog_assets AS a ON a.id=l.asset_id AND a.library_id=l.library_id WHERE l.entity_id=? AND l.asset_id>? ORDER BY l.asset_id LIMIT ?`, []stmt.Param{i("entity"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(64)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"l", "a"}})
	add("children", `SELECT id,0,0 FROM catalog_entities INDEXED BY catalog_entities_parent WHERE parent_id IS NOT NULL AND parent_id=? AND id>? ORDER BY id LIMIT ?`, []stmt.Param{i("parent"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_parent"}})
	for _, r := range []struct {
		name, column string
		kind         int64
	}{{"album", "album_id", 7}, {"book", "book_id", 9}} {
		add("children."+r.name, `SELECT id,`+r.column+`,0 FROM catalog_entities INDEXED BY catalog_entities_library WHERE library_id=? AND kind=? AND id>? ORDER BY id LIMIT ?`, []stmt.Param{i("library"), i("kind"), i("after"), i("limit")}, []any{int64(1), r.kind, int64(0), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}})
	}
	add("title.less", `SELECT a.id FROM catalog_entities AS a NOT INDEXED CROSS JOIN catalog_entities AS b NOT INDEXED ON b.id=? WHERE a.id=? AND a.title COLLATE NOCASE < b.title COLLATE NOCASE`, []stmt.Param{i("other"), i("entity")}, []any{int64(2), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "b"}})
	return append(out, rsRelatedByteDefinitions()...)
}
