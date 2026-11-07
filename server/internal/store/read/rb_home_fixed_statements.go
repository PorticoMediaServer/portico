package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

func rbHomeFixedDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(n, sql string, p []stmt.Param, b []any, proof stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("rb.home.fixed." + n), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_home_fixed.go"}, SQL: sql, Params: p, Cohorts: []stmt.Cohort{{Name: "present", Bindings: b}}, Plan: proof})
	}
	add("members", `SELECT id FROM catalog_entities INDEXED BY catalog_entities_library_id WHERE library_id=? AND id>? ORDER BY id LIMIT ?`, []stmt.Param{i("library"), i("after"), i("limit")}, []any{int64(1), int64(0), int64(33)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library_id"}, Seeks: []string{"library_id=? AND id>?"}})
	// Schema checks fix catalogue timestamp width; numbering is measured only.
	// Personal text has its own native byte point before hydration.
	add("entity", `SELECT id,public_id,kind,retired,revision,admission_own_age,parent_id,show_id,season_id,album_id,book_id,season_number,episode_number,disc_number,track_number,part_number,
 added_at,last_content_added_at,length(numbering),0,metadata_revision,source_revision,identity_revision
 FROM catalog_entities WHERE id=? AND library_id=?`, []stmt.Param{i("item"), i("library")}, []any{int64(1), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}})
	add("personal.bytes", `SELECT octet_length(activity_at),octet_length(container_watermark),octet_length(container_cleared_through),octet_length(dismissed_activity_at),octet_length(resume_playback_id),octet_length(dismissed_playback_id) FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=?`, []stmt.Param{s("profile"), i("item")}, []any{"p", int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}, Seeks: []string{"profile_id=? AND item_id=?"}})
	add("personal", `SELECT revision,item_public_id,resume_position,resume_unit,resume_completed,watched,play_count,favorite,watchlisted,not_interested,rating,
 activity_at,length(NULLIF(activity_state,'ended')),activity_visible,
 resume_playback_id,resume_item_public_id,
 container_watermark,container_cleared_through,
 length(activity_at),container_revision,
 dismissed_child_public_id,dismissed_activity_at,dismissed_playback_id,length(activity_state)
 FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=?`, []stmt.Param{s("profile"), i("item")}, []any{"p", int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}, Seeks: []string{"profile_id=? AND item_id=?"}})
	// Native accepted provider-to-edition grouping is not inferred from an entity
	// identity. Presence is an explicit preparation dependency until composed.
	add("external", `SELECT 1 FROM metadata_entity_external_ids WHERE entity_id=? AND provider IN('tmdb','tvdb','anilist') LIMIT 1`, []stmt.Param{i("item")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_entity_external_ids"}, Seeks: []string{"entity_id=?"}})
	add("owner.policy", `SELECT revision FROM admin_documents INDEXED BY sqlite_autoindex_admin_documents_1 WHERE scope=?`, []stmt.Param{s("scope")}, []any{"library:movies"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_admin_documents_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "admin_documents", Name: "sqlite_autoindex_admin_documents_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "scope", Collation: "BINARY"}}}}, Seeks: []string{"scope=?"}})
	return out
}

// Closed actual-byte preflights share precisely the helper's keyed FROM/WHERE
// and raw LIMIT. They materialize lengths only, before any variable payload.
func rbHomeFixedBindingByteDefinitions() []stmt.Definition {
	defs := rbHomeBindingDefinitions()
	fields := []string{"octet_length(cl.library_id)", "octet_length(a.token)", "octet_length(NULLIF(s.library_id,?)),octet_length(o.id),octet_length(o.revision),octet_length(o.source_id),octet_length(s.incarnation),octet_length(s.root),octet_length(s.configured_root)"}
	for n := range defs {
		d := &defs[n]
		sql := d.SQL
		at := strings.Index(sql, "\n FROM ")
		if at < 0 {
			panic("binding source shape changed")
		}
		d.SQL = "SELECT " + fields[n] + sql[at:]
		if n == 0 {
			d.SQL = strings.Replace(d.SQL, "catalog_entities AS e", "catalog_entities AS e INDEXED BY catalog_entities_library_id", 1)
		}
		if n == 0 {
			d.Plan.PrimaryKeys = []string{"k", "cl"}
			d.Plan.Indexes = append(d.Plan.Indexes, "catalog_entities_library_id")
			d.Plan.Seeks = []string{"library_id=? AND id=?"}
		}
		d.ID = readStatementID("rb.home.fixed.binding." + []string{"item", "links", "objects"}[n] + ".bytes")
		d.CallSites = []string{"store/read/rb_home_fixed.go"}
		if n == 2 {
			d.Cohorts[0].Bindings[1] = "fixed-1"
		}
	}
	return defs
}
