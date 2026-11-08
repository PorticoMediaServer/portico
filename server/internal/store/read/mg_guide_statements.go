package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func mgGuideID(name string) stmt.ID { return stmt.ID(mgGuideQuery(name)) }

func mgGuideQuery(name string) string { return "read.mg.guide." + name }

// MG guide source primitives are inert compiled definitions. They become an
// executable reader only when MGGuideHarnessDefinitions is captured and
// composed with a bounded GV snapshot pool. No locator bytes or SQL handle
// escape; wide values require their exact octet preflight in the SAME snapshot
// before hydration. The library set is read only to prove the viewer may see
// every configured library, never to synthesize a viewer or a library.
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
	libraries := "sqlite_autoindex_catalog_libraries_1"
	defs := []stmt.Definition{
		// The configured library set is a bounded keyset, not a scan: every
		// non-retired catalogue library must be visible to the current viewer
		// before any guide row is read.
		{ID: mgGuideID("libraries"), SQL: `SELECT library_id,retired
FROM catalog_libraries INDEXED BY sqlite_autoindex_catalog_libraries_1
WHERE library_id>? ORDER BY library_id LIMIT ?`, Params: []stmt.Param{text("after"), integer("limit")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{libraries}, Seeks: []string{"library_id>?"}, AutoIndexes: []stmt.AutoIndexBinding{key("catalog_libraries", libraries, "u", "library_id")}},
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"", int64(mgGuideLibraryMax + 1)}}, {Name: "empty", Bindings: []any{"zzz", int64(mgGuideLibraryMax + 1)}}}},
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
		{ID: mgGuideID("channel"), SQL: `SELECT channel_id,name,number,group_name,position,has_live_channel_logos,logo_url
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
		{ID: mgGuideID("programme"), SQL: `SELECT id,channel_id,title,start_utc,end_utc,has_live_programme_metadata,metadata_series_id,metadata_episode_id,metadata_new_evidence,metadata_description,metadata_facts,has_live_programme_icons,icon_url
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
		defs[i].Owner = "store/read MG guide reader"
		defs[i].Kind = stmt.Read
		defs[i].CallSites = []string{"store/read/mg_guide.go:MGGuideReader.Channels"}
	}
	return defs
}

// MGGuideHarnessDefinitions is exactly the inert guide recipe plus the shared
// principal fence it composes. Capture and register these before invoking the
// reader; production wiring remains out of scope.
func MGGuideHarnessDefinitions() []stmt.Definition {
	return append(browseDefinitions(), mgGuideDefinitions()...)
}
