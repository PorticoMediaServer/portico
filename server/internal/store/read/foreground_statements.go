package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strconv"
)

// These inert definitions are available only to explicit harness composition.
func foregroundDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	key := func(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
		v := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, c := range columns {
			v.Columns = append(v.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return v
	}
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, p stmt.Plan) {
		empty := append([]any(nil), bindings...)
		if len(empty) > 0 {
			switch empty[0].(type) {
			case string:
				empty[0] = "missing"
			case int64:
				empty[0] = int64(999999)
			}
		}
		if name == "item" {
			empty = append([]any(nil), bindings...)
			empty[1] = int64(999999)
		}
		if name == "link_source" {
			empty = append([]any(nil), bindings...)
			empty[3] = int64(999999)
		}

		out = append(out, stmt.Definition{ID: readStatementID("foreground." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/foreground.go:ForegroundReader"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	entity := "e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age"
	state := "p.revision,p.resume_position,p.resume_unit,p.play_count,p.favorite,p.watchlisted,p.watched,p.resume_completed"
	add("item", "SELECT "+entity+",e.overview,e.credit_image_id,"+state+` FROM catalog_entities AS e
LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id
WHERE e.id=? AND e.library_id=? AND e.kind=?`, []stmt.Param{s("profile"), i("item"), i("library"), i("kind")}, []any{"p", int64(1), int64(1), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Indexes: []string{"personal_current_live"}})
	add("children", "SELECT "+entity+`,e.kind,k.container FROM catalog_entities AS e INDEXED BY catalog_entities_parent
CROSS JOIN catalog_kinds AS k ON k.id=e.kind WHERE parent_id IS NOT NULL AND e.parent_id=? AND e.id>? ORDER BY e.id LIMIT ?`, []stmt.Param{i("parent"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_parent"}, PrimaryKeys: []string{"k"}, Seeks: []string{"parent_id=? AND id>?"}})
	add("images", `SELECT role,choice,digest,provider,origin,revision FROM artwork_selections WHERE kind='entity' AND entity_id=? ORDER BY role,choice LIMIT ?`, []stmt.Param{i("item"), i("limit")}, []any{int64(1), int64(129)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_selections"}, Seeks: []string{"kind=? AND entity_id=?"}})

	classKey := key("catalog_credit_labels", "sqlite_autoindex_catalog_credit_labels_1", "u", "label")
	add("credits.class", `SELECT id FROM catalog_credit_labels INDEXED BY sqlite_autoindex_catalog_credit_labels_1 WHERE label=?`, []stmt.Param{s("label")}, []any{"Acting"}, stmt.Plan{Hot: true, Indexes: []string{classKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{classKey}})

	// department_id has a mandatory FK; the left point join preserves that label
	// lookup without a planner-added prefilter/Bloom pass over the label table.
	for _, class := range []struct {
		name, predicate string
		limit           int64
	}{
		{"cast", "c.department_id=?", 100},
		{"crew", "c.department_id<>?", 50},
	} {
		params := []stmt.Param{i("item"), i("image"), i("after"), i("department")}
		bindings := []any{int64(1), int64(1), int64(-1), int64(2)}
		add("credits."+class.name, `SELECT c.ord,c.person_id,c.credited_name,r.label,d.label FROM catalog_credits AS c INDEXED BY catalog_credits_entity
CROSS JOIN catalog_credit_labels AS r ON r.id=c.role_id
LEFT JOIN catalog_credit_labels AS d ON d.id=c.department_id
WHERE c.entity_id=? AND c.image_id=? AND c.ord>? AND `+class.predicate+` ORDER BY c.ord LIMIT `+strconv.FormatInt(class.limit, 10), params, bindings, stmt.Plan{Hot: true, Indexes: []string{"catalog_credits_entity"}, PrimaryKeys: []string{"r", "d"}, Seeks: []string{"entity_id=? AND image_id=? AND ord>?"}})

	}

	add("credits.crew.all", `SELECT c.ord,c.person_id,c.credited_name,r.label,d.label FROM catalog_credits AS c INDEXED BY catalog_credits_entity
CROSS JOIN catalog_credit_labels AS r ON r.id=c.role_id
CROSS JOIN catalog_credit_labels AS d ON d.id=c.department_id
WHERE c.entity_id=? AND c.image_id=? AND c.ord>? ORDER BY c.ord LIMIT 50`, []stmt.Param{i("item"), i("image"), i("after")}, []any{int64(1), int64(1), int64(-1)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_credits_entity"}, PrimaryKeys: []string{"r", "d"}, Seeks: []string{"entity_id=? AND image_id=? AND ord>?"}})

	add("versions", `SELECT l.asset_id,l.part_index,l.edition,l.available FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_order WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id LIMIT ?`, []stmt.Param{i("item"), i("limit")}, []any{int64(1), int64(17)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_order"}, Seeks: []string{"entity_id=?"}})
	sf := key("asset_stream_facts", "sqlite_autoindex_asset_stream_facts_1", "pk", "asset_id")
	cf := key("asset_chapter_facts", "sqlite_autoindex_asset_chapter_facts_1", "pk", "asset_id")
	add("file", `SELECT a.token,a.path,a.size,a.modified_ns,a.container,a.duration,l.start_seconds,l.end_seconds,a.playback_origin_incarnation,a.playback_origin_revision,l.playback_origin_incarnation,l.playback_origin_revision,a.prepared_source_revision
FROM catalog_assets AS a INDEXED BY sqlite_autoindex_catalog_assets_1
CROSS JOIN catalog_asset_links AS l INDEXED BY catalog_asset_links_available_seek ON l.asset_id=a.id AND l.library_id=a.library_id
CROSS JOIN asset_stream_facts AS s INDEXED BY sqlite_autoindex_asset_stream_facts_1 ON s.asset_id=a.token AND s.size=a.size AND s.modified_ns=a.modified_ns
CROSS JOIN asset_chapter_facts AS c INDEXED BY sqlite_autoindex_asset_chapter_facts_1 ON c.asset_id=a.token AND c.size=a.size AND c.modified_ns=a.modified_ns
WHERE a.token=? AND l.entity_id=? AND l.library_id=? AND l.available=1 AND a.available=1 AND a.info_publication_pending=0 AND l.info_pending_change IS NULL AND c.status='ready'`, []stmt.Param{s("token"), i("item"), i("library")}, []any{"asset", int64(1), int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_available_seek", "sqlite_autoindex_catalog_assets_1", sf.Name, cf.Name}, AutoIndexes: []stmt.AutoIndexBinding{key("catalog_assets", "sqlite_autoindex_catalog_assets_1", "u", "token"), sf, cf}})

	add("source", `SELECT e.revision,e.credit_image_id,h.sealed,e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,e.artwork_incarnation,e.artwork_revision,e.credit_owner_revision,e.scalar_source_revision,e.aggregate_revision,e.admission_revision,e.playback_origin_incarnation,e.playback_origin_revision,e.subtitle_collection_revision,e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age
FROM catalog_entities AS e
LEFT JOIN catalog_credit_images AS h INDEXED BY catalog_credit_images_entity ON h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision AND h.id=e.credit_image_id
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`, []stmt.Param{i("item"), i("library"), i("kind")}, []any{int64(1), int64(1), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Indexes: []string{"catalog_credit_images_entity"}})
	add("asset_source", `SELECT token,playback_origin_incarnation,playback_origin_revision,prepared_source_revision,available,size,modified_ns,info_epoch,info_invalidation_revision,info_publication_pending FROM catalog_assets WHERE id=? AND library_id=?`, []stmt.Param{i("asset"), i("library")}, []any{int64(1), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}})
	add("link_source", `SELECT l.playback_origin_incarnation,l.playback_origin_revision,l.available,l.info_pending_change,
s.revision,s.size,s.modified_ns,s.fingerprint,s.detail_version,c.revision,c.size,c.modified_ns,c.status,c.fingerprint
FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_asset
LEFT JOIN asset_stream_facts AS s INDEXED BY sqlite_autoindex_asset_stream_facts_1 ON s.asset_id=?
LEFT JOIN asset_chapter_facts AS c INDEXED BY sqlite_autoindex_asset_chapter_facts_1 ON c.asset_id=?
WHERE l.asset_id=? AND l.entity_id=?`, []stmt.Param{s("stream_token"), s("chapter_token"), i("asset"), i("item")}, []any{"asset", "asset", int64(1), int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_asset", sf.Name, cf.Name}, AutoIndexes: []stmt.AutoIndexBinding{sf, cf}})

	streams := key("asset_streams", "sqlite_autoindex_asset_streams_1", "pk", "asset_id", "stream_index")
	add("streams", `SELECT stream_index,type,codec,language,title,channels,is_default,is_forced FROM asset_streams INDEXED BY sqlite_autoindex_asset_streams_1 WHERE asset_id=? ORDER BY stream_index LIMIT ?`, []stmt.Param{s("asset"), i("limit")}, []any{"asset", int64(766)}, stmt.Plan{Hot: true, Indexes: []string{streams.Name}, AutoIndexes: []stmt.AutoIndexBinding{streams}, Seeks: []string{"asset_id=?"}})
	chapters := key("asset_chapters", "sqlite_autoindex_asset_chapters_1", "pk", "asset_id", "chapter_index")
	add("chapters", `SELECT chapter_index,title,start_seconds,end_seconds FROM asset_chapters INDEXED BY sqlite_autoindex_asset_chapters_1 WHERE asset_id=? ORDER BY chapter_index LIMIT ?`, []stmt.Param{s("asset"), i("limit")}, []any{"asset", int64(513)}, stmt.Plan{Hot: true, Indexes: []string{chapters.Name}, AutoIndexes: []stmt.AutoIndexBinding{chapters}, Seeks: []string{"asset_id=?"}})
	base := "SELECT p.activity_at,p.item_public_id," + state + "," + entity + `,e.library_id,e.kind FROM personal_items AS p INDEXED BY personal_current_activity
LEFT JOIN catalog_entities AS e ON e.id=p.item_id WHERE p.profile_id=? AND p.activity_visible=1 AND p.activity_at IS NOT NULL`
	end := " ORDER BY p.activity_at DESC,p.item_public_id ASC LIMIT ?"
	add("continue.first", base+end, []stmt.Param{s("profile"), i("limit")}, []any{"p", int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_activity"}, PrimaryKeys: []string{"e"}, Seeks: []string{"profile_id=? AND activity_visible=?"}})
	add("continue.tail", base+" AND p.activity_at=? AND p.item_public_id>?"+end, []stmt.Param{s("profile"), s("at"), b("public"), i("limit")}, []any{"p", "2026-11-05T12:00:00.000000000Z", make([]byte, 16), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_activity"}, PrimaryKeys: []string{"e"}, Seeks: []string{"activity_at=? AND item_public_id>?"}})
	add("continue.next", base+" AND p.activity_at<?"+end, []stmt.Param{s("profile"), s("at"), i("limit")}, []any{"p", "2026-11-05T12:00:00.000000000Z", int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_activity"}, PrimaryKeys: []string{"e"}, Seeks: []string{"activity_at<?"}})
	return out
}

// HarnessDefinitions supplies the exact compiled browse and foreground SQL to
// qualification composition. It opens no DB and authorizes no execution: the
// caller must Capture on its actual reader, then register that opaque receipt.
// The shipped server does not select this layer before explicit cutover.
func HarnessDefinitions() []stmt.Definition {
	return append(browseDefinitions(), foregroundDefinitions()...)
}
