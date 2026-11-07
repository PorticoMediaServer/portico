package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// nativeSearchSourceDefinitions supplies ordinary MAIN metadata only. The parent
// composes these with FullSearchDefinitions and captures the combined recipe;
// this function grants no execution, FTS, bank, or publication authority.
//
// Projection order is the SELECT order below; parameter order is Params order.
// Every window is an ascending, exclusive keyset. Callers must validate limits
// in [1,64], drain all pages (including a final empty page), and refuse/pending
// rather than treat a work budget or a full page as cohort completeness.
//
// Person acceptance consumes CHANGE-013's dedicated immutable search birth.
// Neither token nor artwork_incarnation is that carrier. The new point remains
// unexecutable on a schema lacking that column; there is no fallback.
// There are no entity/person redirect tables in this schema; none are invented.
func nativeSearchSourceDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	key := func(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
		v := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, c := range columns {
			v.Columns = append(v.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return v
	}
	unique := func(k stmt.AutoIndexBinding) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{k.Name}, AutoIndexes: []stmt.AutoIndexBinding{k}}
	}
	point := func(alias string) stmt.Plan { return stmt.Plan{Hot: true, PrimaryKeys: []string{alias}} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, p stmt.Plan) {
		empty := append([]any(nil), bindings...)
		switch empty[0].(type) {
		case string:
			empty[0] = "missing"
		default:
			empty[0] = int64(999999)
		}
		out = append(out, stmt.Definition{ID: stmt.ID("read.full.native." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/native_full_search.go:NativeFullSearchReader.Read"}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	add("revision", `SELECT revision,active_generation_id,building_generation_id,input_sequence FROM search_revision AS r WHERE r.singleton=?`, []stmt.Param{i("singleton")}, []any{int64(1)}, point("r"))
	bankKey := key("search_generations", "sqlite_autoindex_search_generations_1", "u", "bank_key")
	// bank_key is exact BINARY equality; callers bind "v1". Actual engine
	// identity is the separate closed nativeEngineDefinition, not this row.
	add("bank", `SELECT b.id,b.bank_key,b.recipe_version,b.engine_version,b.unicode_fingerprint,b.state,b.pending_subjects FROM search_generations AS b INDEXED BY sqlite_autoindex_search_generations_1 WHERE b.bank_key=?`, []stmt.Param{t("bank_key")}, []any{"v1"}, unique(bankKey))

	entityKey := key("search_publish_jobs", "sqlite_autoindex_search_publish_jobs_1", "u", "entity_id", "search_generation_id")
	p := unique(entityKey)
	p.PrimaryKeys = []string{"p", "e"}
	// Accepted entity projection: job head (document, published/input generations,
	// source incarnation/revision, job revision), then publication display and
	// identity (title, fold, kind, library, id, generation). Both current source
	// and published input must still match; a newer pending input yields no row.
	add("entity.accepted", `SELECT j.published_document_id,j.published_generation,j.input_generation,j.source_incarnation,j.source_revision,j.job_revision,p.title,p.title_fold,p.kind,p.library_id,p.id,p.generation
FROM search_publish_jobs AS j INDEXED BY sqlite_autoindex_search_publish_jobs_1
CROSS JOIN search_publications AS p ON p.id=j.published_document_id
CROSS JOIN catalog_entities AS e ON e.id=j.entity_id
WHERE j.entity_id=? AND j.search_generation_id=?
AND p.entity_id=j.entity_id AND p.search_generation_id=j.search_generation_id
AND p.generation=j.published_generation AND p.generation=j.input_generation
AND p.source_incarnation=j.source_incarnation AND p.source_revision=j.source_revision
AND p.recipe_version=j.recipe_version
AND e.metadata_incarnation=p.source_incarnation AND e.revision=p.source_revision
AND e.kind=p.kind AND e.library_id=p.library_id AND e.retired=0`, []stmt.Param{i("entity"), i("bank")}, []any{int64(1), int64(1)}, p)
	personKey := key("search_publish_jobs", "sqlite_autoindex_search_publish_jobs_2", "u", "person_id", "search_generation_id")
	add("person.head.raw", `SELECT j.published_document_id,j.published_generation,j.input_generation,j.source_incarnation,j.source_revision,j.job_revision FROM search_publish_jobs AS j INDEXED BY sqlite_autoindex_search_publish_jobs_2 WHERE j.person_id=? AND j.search_generation_id=?`, []stmt.Param{i("person"), i("bank")}, []any{int64(1), int64(1)}, unique(personKey))
	add("person.source.raw", `SELECT p.id,p.name,p.sort_name,p.revision FROM catalog_people AS p WHERE p.id=?`, []stmt.Param{i("person")}, []any{int64(1)}, point("p"))
	personAccepted := unique(personKey)
	personAccepted.PrimaryKeys = []string{"p", "s"}
	add("person.accepted", `SELECT p.id,p.generation,p.title,p.title_fold,p.source_incarnation,p.source_revision,s.name
FROM search_publish_jobs AS j INDEXED BY sqlite_autoindex_search_publish_jobs_2
CROSS JOIN search_publications AS p ON p.id=j.published_document_id
CROSS JOIN catalog_people AS s ON s.id=j.person_id
WHERE j.person_id=? AND j.search_generation_id=?
AND p.person_id=j.person_id AND p.search_generation_id=j.search_generation_id
AND p.generation=j.published_generation AND p.generation=j.input_generation
AND p.source_incarnation=j.source_incarnation AND p.source_revision=j.source_revision
AND p.recipe_version=j.recipe_version
AND s.search_incarnation=p.source_incarnation AND s.revision=p.source_revision`, []stmt.Param{i("person"), i("bank")}, []any{int64(1), int64(1)}, personAccepted)
	add("publication", `SELECT p.id,p.entity_id,p.person_id,p.search_generation_id,p.title,p.title_fold,p.kind,p.library_id,p.generation,p.source_incarnation,p.source_revision,p.recipe_version FROM search_publications AS p WHERE p.id=?`, []stmt.Param{i("publication")}, []any{int64(1)}, point("p"))
	add("fragment", `SELECT f.publication_id,f.owner_entity,f.owner_person,f.field,f.ordinal,f.source_revision,f.generation,f.search_generation_id FROM search_fragments AS f WHERE f.id=?`, []stmt.Param{i("fragment")}, []any{int64(1)}, point("f"))
	field := `SELECT f.id,f.ordinal,f.prefix_tokens,f.lexical_tokens,f.normalized,f.source_revision FROM search_fragments AS f INDEXED BY search_fragment_publication WHERE f.publication_id=? AND f.field=?`
	fp := stmt.Plan{Hot: true, Indexes: []string{"search_fragment_publication"}, Seeks: []string{"publication_id=? AND field=?"}}
	add("field.first", field+` ORDER BY f.ordinal LIMIT ?`, []stmt.Param{i("publication"), i("field"), i("limit")}, []any{int64(1), int64(4), int64(64)}, fp)
	add("field.next", field+` AND f.ordinal>? ORDER BY f.ordinal LIMIT ?`, []stmt.Param{i("publication"), i("field"), i("after"), i("limit")}, []any{int64(1), int64(4), int64(-1), int64(64)}, fp)
	// Common seven columns match the existing browse entity decoder exactly.
	add("item", `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,e.library_id,e.metadata_incarnation,e.source_revision,e.credit_image_id,e.credit_owner_revision,e.album_id,e.show_id,e.artist_id,e.parent_id FROM catalog_entities AS e WHERE e.id=?`, []stmt.Param{i("entity")}, []any{int64(1)}, point("e"))

	// Accepted credit image gate binds the current metadata incarnation and
	// owner revision as well as the entity's current image pointer. Ordinal is
	// unique within an image; no secondary tie cursor is needed.
	credit := `SELECT c.ord,c.person_id,c.image_id,c.credited_name,c.owner_incarnation,c.owner_revision
FROM catalog_entities AS e
CROSS JOIN catalog_credit_images AS h ON h.id=e.credit_image_id
CROSS JOIN catalog_credits AS c INDEXED BY catalog_credits_entity ON c.entity_id=e.id AND c.image_id=h.id
WHERE e.id=? AND h.id=? AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision AND h.sealed=1`
	cp := stmt.Plan{Hot: true, Indexes: []string{"catalog_credits_entity"}, PrimaryKeys: []string{"e", "h"}, Seeks: []string{"entity_id=? AND image_id=?"}}
	add("credit.first", credit+` ORDER BY c.ord LIMIT ?`, []stmt.Param{i("entity"), i("image"), i("limit")}, []any{int64(2), int64(1), int64(64)}, cp)
	add("credit.next", credit+` AND c.ord>? ORDER BY c.ord LIMIT ?`, []stmt.Param{i("entity"), i("image"), i("after"), i("limit")}, []any{int64(2), int64(1), int64(-1), int64(64)}, cp)
	add("credit.point", credit+` AND c.ord=? AND c.person_id=?`, []stmt.Param{i("entity"), i("image"), i("ordinal"), i("person")}, []any{int64(2), int64(1), int64(0), int64(1)}, cp)
	// Inverses only. Existing FullSearchDefinitions owns forward/reverse owner
	// pages; do not substitute these relationships for automatic membership.
	sa := key("catalog_song_artists", "sqlite_autoindex_catalog_song_artists_1", "pk", "song_id", "artist_id")
	for _, route := range []struct {
		name, base, order string
		plan              stmt.Plan
	}{
		{"song_artist", `SELECT s.artist_id FROM catalog_song_artists AS s INDEXED BY sqlite_autoindex_catalog_song_artists_1 WHERE s.song_id=?`, "s.artist_id", unique(sa)},
		{"authored_collection", `SELECT c.collection_id FROM catalog_collection_members AS c INDEXED BY catalog_collection_members_item WHERE c.item_id=?`, "c.collection_id", stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_item"}}},
	} {
		add(route.name+".first", route.base+" ORDER BY "+route.order+" LIMIT ?", []stmt.Param{i("entity"), i("limit")}, []any{int64(1), int64(64)}, route.plan)
		add(route.name+".next", route.base+" AND "+route.order+">? ORDER BY "+route.order+" LIMIT ?", []stmt.Param{i("entity"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(64)}, route.plan)
	}
	add("kind", `SELECT id,name,container FROM catalog_kinds WHERE id=?`, []stmt.Param{i("kind")}, []any{int64(1)}, point("catalog_kinds"))
	add("seal", `SELECT algorithm,sealed,expected_fragments,verified_fragments,expected_digest,chain,expected_bytes,verified_bytes FROM search_image_seals WHERE publication_id=?`, []stmt.Param{i("publication")}, []any{int64(1)}, point("search_image_seals"))
	album := `SELECT id FROM catalog_entities INDEXED BY catalog_entities_album_search WHERE album_id IS NOT NULL AND album_id=?`
	albumPlan := stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_album_search"}}
	add("album.first", album+` ORDER BY id DESC LIMIT ?`, []stmt.Param{i("album"), i("limit")}, []any{int64(20), int64(64)}, albumPlan)
	add("album.next", album+` AND id<? ORDER BY id DESC LIMIT ?`, []stmt.Param{i("album"), i("after"), i("limit")}, []any{int64(20), int64(24), int64(64)}, albumPlan)
	return out
}
