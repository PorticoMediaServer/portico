package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func mgGuideID(name string) stmt.ID { return stmt.ID("read.mg.guide." + name) }

// These are inert guide source primitives, not a registered/public reader or an
// authorization verdict. The accepting reader must compose current principal,
// source/rating policy and final delivery fences through GV. No locator bytes
// or SQL handle escape. Wide values require their exact octet preflight in the
// SAME snapshot before hydration; native row limits remain independently due.
func mgGuideDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	key := func(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
		p := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, c := range columns {
			p.Columns = append(p.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return p
	}
	channelPK := "sqlite_autoindex_live_channel_versions_1"
	channelOrder := "sqlite_autoindex_live_channel_versions_2"
	programmePK := "sqlite_autoindex_live_programmes_1"
	defs := []stmt.Definition{
		{ID: mgGuideID("source"), SQL: `SELECT s.revision,s.state,s.active_generation,i.settings_viewer_access,i.has_live_source_removals
FROM live_sources AS s INDEXED BY sqlite_autoindex_live_sources_1
CROSS JOIN live_source_identities AS i INDEXED BY sqlite_autoindex_live_source_identities_1 ON i.id=s.id
WHERE s.id=?`, Params: []stmt.Param{text("source")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1", "sqlite_autoindex_live_source_identities_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{
				key("live_sources", "sqlite_autoindex_live_sources_1", "pk", "id"), key("live_source_identities", "sqlite_autoindex_live_source_identities_1", "pk", "id")}},
			Cohorts: []stmt.Cohort{{Name: "published", Bindings: []any{"mg-source"}}, {Name: "missing", Bindings: []any{"mg-missing"}}}},
		{ID: mgGuideID("channels"), SQL: `SELECT position,channel_id
FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_2
WHERE generation_id=? AND position>? ORDER BY position LIMIT ?`, Params: []stmt.Param{text("generation"), integer("after_position"), integer("limit")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{channelOrder}, Seeks: []string{"generation_id=? AND position>?"}, AutoIndexes: []stmt.AutoIndexBinding{key("live_channel_versions", channelOrder, "u", "generation_id", "position")}},
			Cohorts: []stmt.Cohort{{Name: "first", Bindings: []any{"mg-old", int64(-1), int64(3)}}, {Name: "continuation", Bindings: []any{"mg-old", int64(1), int64(2)}}, {Name: "empty", Bindings: []any{"mg-new", int64(20), int64(3)}}}},
		{ID: mgGuideID("channel"), SQL: `SELECT channel_id,name,number,group_name,position,has_live_channel_logos,COALESCE(logo_url,'')
FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_1 WHERE generation_id=? AND channel_id=?`, Params: []stmt.Param{text("generation"), text("channel")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{channelPK}, Seeks: []string{"generation_id=? AND channel_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{key("live_channel_versions", channelPK, "pk", "generation_id", "channel_id")}},
			Cohorts: []stmt.Cohort{{Name: "old", Bindings: []any{"mg-old", "mg-channel"}}, {Name: "new", Bindings: []any{"mg-new", "mg-channel"}}, {Name: "missing", Bindings: []any{"mg-old", "mg-missing"}}}},
		// A complete per-channel interval, not a programme continuation. Fetch
		// max+1 and report capacity if it overflows; do not emit a truncated
		// interval or use this non-unique time ordering as a cursor. Bounded Go
		// sorting may order complete copied output by start/end/ID afterward.
		{ID: mgGuideID("programmes"), SQL: `SELECT id,start_utc,end_utc
FROM live_programmes INDEXED BY live_programmes_window
WHERE generation_id=? AND channel_id=? AND start_utc<? AND end_utc>? ORDER BY start_utc,end_utc LIMIT ?`, Params: []stmt.Param{text("generation"), text("channel"), text("end"), text("start"), integer("limit")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"live_programmes_window"}, Seeks: []string{"generation_id=? AND channel_id=? AND start_utc<?"}},
			Cohorts: []stmt.Cohort{{Name: "overlap_and_ties", Bindings: []any{"mg-old", "mg-channel", "2026-11-07T06:00:00Z", "2026-11-07T00:00:00Z", int64(5)}}, {Name: "empty", Bindings: []any{"mg-old", "mg-channel", "2026-11-08T06:00:00Z", "2026-11-08T00:00:00Z", int64(5)}}}},
		{ID: mgGuideID("programme"), SQL: `SELECT id,channel_id,title,start_utc,end_utc,has_live_programme_metadata,COALESCE(metadata_series_id,''),COALESCE(metadata_episode_id,''),COALESCE(metadata_new_evidence,''),COALESCE(metadata_description,''),COALESCE(metadata_facts,''),has_live_programme_icons,COALESCE(icon_url,'')
FROM live_programmes INDEXED BY sqlite_autoindex_live_programmes_1 WHERE generation_id=? AND id=?`, Params: []stmt.Param{text("generation"), text("programme")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{programmePK}, Seeks: []string{"generation_id=? AND id=?"}, AutoIndexes: []stmt.AutoIndexBinding{key("live_programmes", programmePK, "pk", "generation_id", "id")}},
			Cohorts: []stmt.Cohort{{Name: "old", Bindings: []any{"mg-old", "mg-a"}}, {Name: "new", Bindings: []any{"mg-new", "mg-a"}}, {Name: "missing", Bindings: []any{"mg-old", "mg-missing"}}}},
	}
	defs = append(defs,
		stmt.Definition{ID: mgGuideID("channel_octets"), SQL: "SELECT octet_length(channel_id),octet_length(name),octet_length(number),octet_length(group_name),octet_length(logo_url) FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_1 WHERE generation_id=? AND channel_id=?",
			Params:  []stmt.Param{text("generation"), text("channel")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{channelPK}, Seeks: []string{"generation_id=? AND channel_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{key("live_channel_versions", channelPK, "pk", "generation_id", "channel_id")}},
			Cohorts: []stmt.Cohort{{Name: "old", Bindings: []any{"mg-old", "mg-channel"}}, {Name: "null_logo", Bindings: []any{"mg-old", "mg-channel-1"}}, {Name: "missing", Bindings: []any{"mg-old", "mg-missing"}}}},
		stmt.Definition{ID: mgGuideID("programme_octets"), SQL: "SELECT octet_length(id),octet_length(channel_id),octet_length(title),octet_length(start_utc),octet_length(end_utc),octet_length(metadata_series_id),octet_length(metadata_episode_id),octet_length(metadata_new_evidence),octet_length(metadata_description),octet_length(metadata_facts),octet_length(icon_url) FROM live_programmes INDEXED BY sqlite_autoindex_live_programmes_1 WHERE generation_id=? AND id=?",
			Params:  []stmt.Param{text("generation"), text("programme")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{programmePK}, Seeks: []string{"generation_id=? AND id=?"}, AutoIndexes: []stmt.AutoIndexBinding{key("live_programmes", programmePK, "pk", "generation_id", "id")}},
			Cohorts: []stmt.Cohort{{Name: "old", Bindings: []any{"mg-old", "mg-a"}}, {Name: "null_metadata", Bindings: []any{"mg-new", "mg-a"}}, {Name: "missing", Bindings: []any{"mg-old", "mg-missing"}}}},
	)

	for i := range defs {
		defs[i].Owner = "store/read MG guide source primitives"
		defs[i].Kind = stmt.Read
		defs[i].CallSites = []string{"store/read/mg_statements_test.go:native guide source controls"}
	}
	return defs
}
