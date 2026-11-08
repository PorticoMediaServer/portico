package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The TV preparation family is four closed reads over accepted native rows:
// a show with its ordered seasons and directly attached episodes, one season
// with its ordered episodes, one episode with its exact show/season hierarchy,
// and the episode/asset link membership in both directions. Every shape seeks
// a native keyset; none scans or sorts. These are harness definitions only.
const (
	iaTVEntitySQL       = `SELECT id,public_id,title,kind,parent_id,show_id,season_id,season_number,episode_number,retired,admission_own_age,revision,tv_incarnation,tv_source_revision,tv_hierarchy_revision,tv_selection_revision,tv_descriptive_revision FROM catalog_entities WHERE id=? AND library_id=?`
	iaTVEntityOctetsSQL = `SELECT octet_length(public_id),octet_length(title),octet_length(tv_incarnation) FROM catalog_entities WHERE id=? AND library_id=?`

	// A show's seasons are the UNIQUE(show_id,season_number) key. Normalized
	// season numbers are the chronology; id is the total tie-break.
	iaTVSeasonsFirstSQL = `SELECT id,library_id,kind,retired,admission_own_age,revision,season_number,episode_number,season_id,parent_id FROM catalog_entities INDEXED BY catalog_entities_season_number WHERE kind=3 AND show_id=? ORDER BY season_number,id LIMIT ?`
	iaTVSeasonsTailSQL  = `SELECT id,library_id,kind,retired,admission_own_age,revision,season_number,episode_number,season_id,parent_id FROM catalog_entities INDEXED BY catalog_entities_season_number WHERE kind=3 AND show_id=? AND season_number=? AND id>? ORDER BY id LIMIT ?`
	iaTVSeasonsNextSQL  = `SELECT id,library_id,kind,retired,admission_own_age,revision,season_number,episode_number,season_id,parent_id FROM catalog_entities INDEXED BY catalog_entities_season_number WHERE kind=3 AND show_id=? AND season_number>? ORDER BY season_number,id LIMIT ?`

	// Episodes attached directly to a show have no season. episode_number is
	// nullable, so the continuation uses the merged two-stream nullable keyset.
	iaTVShowEpisodesBase  = `SELECT id,library_id,kind,retired,admission_own_age,revision,season_number,episode_number,season_id,parent_id FROM catalog_entities INDEXED BY catalog_entities_show WHERE show_id IS NOT NULL AND show_id=? AND season_id IS NULL`
	iaTVShowEpisodesFirst = iaTVShowEpisodesBase + ` ORDER BY episode_number,id LIMIT ?`
	iaTVShowEpisodesTail  = iaTVShowEpisodesBase + ` AND episode_number=? AND id>? ORDER BY id LIMIT ?`
	iaTVShowEpisodesNext  = iaTVShowEpisodesBase + ` AND episode_number>? ORDER BY episode_number,id LIMIT ?`

	iaTVSeasonEpisodesBase  = `SELECT id,library_id,kind,retired,admission_own_age,revision,season_number,episode_number,season_id,parent_id FROM catalog_entities INDEXED BY catalog_entities_season WHERE season_id IS NOT NULL AND season_id=?`
	iaTVSeasonEpisodesFirst = iaTVSeasonEpisodesBase + ` ORDER BY episode_number,id LIMIT ?`
	iaTVSeasonEpisodesTail  = iaTVSeasonEpisodesBase + ` AND episode_number=? AND id>? ORDER BY id LIMIT ?`
	iaTVSeasonEpisodesNext  = iaTVSeasonEpisodesBase + ` AND episode_number>? ORDER BY episode_number,id LIMIT ?`

	// Link order and its asset reverse are the two canonical link keys. The
	// bounded list returns identifiers only; each exact link is then charged
	// through its own byte point.
	iaTVLinksSQL      = `SELECT asset_id FROM catalog_asset_links INDEXED BY catalog_asset_links_order WHERE entity_id=? AND library_id=? ORDER BY part_index,asset_id LIMIT 17`
	iaTVMembersSQL    = `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=? AND library_id=? ORDER BY entity_id LIMIT 9`
	iaTVLinkSQL       = `SELECT entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition,playback_origin_incarnation,playback_origin_revision FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?`
	iaTVLinkOctetsSQL = `SELECT octet_length(edition),octet_length(playback_origin_incarnation) FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?`

	iaTVAssetSQL       = `SELECT id,available,playback_origin_revision,prepared_source_revision,playback_origin_incarnation FROM catalog_assets WHERE id=? AND library_id=?`
	iaTVAssetOctetsSQL = `SELECT octet_length(playback_origin_incarnation) FROM catalog_assets WHERE id=? AND library_id=?`
)

// IATVHarnessDefinitions is inert harness composition, not registration or
// authorization. Capture these exact definitions on the actual reader first.
func IATVHarnessDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args []any, p stmt.Plan) {
		empty := append([]any(nil), args...)
		empty[0] = int64(9999999)
		out = append(out, stmt.Definition{ID: readStatementID("foreground.ia.tv." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read/ia_tv", CallSites: []string{"store/read/ia_tv.go"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	entityPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}}
	entityParams := []stmt.Param{i("item"), i("library")}
	entityArgs := []any{int64(2201), int64(1)}
	add("show_octets", iaTVEntityOctetsSQL, entityParams, entityArgs, entityPoint)
	add("show", iaTVEntitySQL, entityParams, entityArgs, entityPoint)
	add("season_octets", iaTVEntityOctetsSQL, entityParams, entityArgs, entityPoint)
	add("season", iaTVEntitySQL, entityParams, entityArgs, entityPoint)
	add("episode_octets", iaTVEntityOctetsSQL, entityParams, entityArgs, entityPoint)
	add("episode", iaTVEntitySQL, entityParams, entityArgs, entityPoint)

	show := []any{int64(2000)}
	add("show_seasons_first", iaTVSeasonsFirstSQL, []stmt.Param{i("show"), i("limit")}, append(show, int64(MaxRaw)), stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_season_number"}, Seeks: []string{"show_id=?"}})
	add("show_seasons_tail", iaTVSeasonsTailSQL, []stmt.Param{i("show"), i("season"), i("after"), i("limit")}, append(show, int64(1), int64(0), int64(MaxRaw)), stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_season_number"}, Seeks: []string{"season_number=? AND rowid>?"}})
	add("show_seasons_next", iaTVSeasonsNextSQL, []stmt.Param{i("show"), i("season"), i("limit")}, append(show, int64(0), int64(MaxRaw)), stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_season_number"}, Seeks: []string{"season_number>?"}})

	add("show_episodes_first", iaTVShowEpisodesFirst, []stmt.Param{i("show"), i("limit")}, append(show, int64(MaxRaw)), stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_show"}, Seeks: []string{"show_id=? AND season_id=?"}})
	add("show_episodes_tail", iaTVShowEpisodesTail, []stmt.Param{i("show"), i("episode"), i("after"), i("limit")}, append(show, int64(1), int64(0), int64(MaxRaw)), stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_show"}, Seeks: []string{"episode_number=? AND id>?"}})
	add("show_episodes_next", iaTVShowEpisodesNext, []stmt.Param{i("show"), i("episode"), i("limit")}, append(show, int64(1), int64(MaxRaw)), stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_show"}, Seeks: []string{"episode_number>?"}})

	season := []any{int64(2100)}
	add("season_episodes_first", iaTVSeasonEpisodesFirst, []stmt.Param{i("season"), i("limit")}, append(season, int64(MaxRaw)), stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_season"}, Seeks: []string{"season_id=?"}})
	add("season_episodes_tail", iaTVSeasonEpisodesTail, []stmt.Param{i("season"), i("episode"), i("after"), i("limit")}, append(season, int64(1), int64(0), int64(MaxRaw)), stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_season"}, Seeks: []string{"episode_number=? AND id>?"}})
	add("season_episodes_next", iaTVSeasonEpisodesNext, []stmt.Param{i("season"), i("episode"), i("limit")}, append(season, int64(1), int64(MaxRaw)), stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_season"}, Seeks: []string{"episode_number>?"}})

	add("links", iaTVLinksSQL, []stmt.Param{i("entity"), i("library")}, []any{int64(2201), int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_order"}, Seeks: []string{"entity_id=?"}})
	add("members", iaTVMembersSQL, []stmt.Param{i("asset"), i("library")}, []any{int64(2000), int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_asset"}, Seeks: []string{"asset_id=?"}})
	linkPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}}
	linkParams := []stmt.Param{i("item"), i("asset"), i("library")}
	linkArgs := []any{int64(2201), int64(2000), int64(1)}
	add("link_octets", iaTVLinkOctetsSQL, linkParams, linkArgs, linkPoint)
	add("link", iaTVLinkSQL, linkParams, linkArgs, linkPoint)

	assetPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}}
	assetParams := []stmt.Param{i("asset"), i("library")}
	assetArgs := []any{int64(2000), int64(1)}
	add("asset_octets", iaTVAssetOctetsSQL, assetParams, assetArgs, assetPoint)
	add("asset", iaTVAssetSQL, assetParams, assetArgs, assetPoint)
	return out
}
