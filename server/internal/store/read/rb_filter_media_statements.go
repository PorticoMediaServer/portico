package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert asset and Reading fact lookups for the closed media filter vocabulary.
// The caller must Capture these exact definitions before use; nothing here is a
// public read port, a source opening or an authority capability.
//
// Every point is an equality seek on a primary key, a unique key or the named
// facet index. Variable text is always accompanied by an octet preflight leg so
// a caller reserves actual bytes before hydration.
const rbFilterMediaAttributionSQL = `SELECT e.kind,e.disc_number,e.has_reading_attribution_members,e.reading_author_value,e.reading_author_key,e.reading_author_source,e.reading_series_value,e.reading_series_key,e.reading_series_id,e.reading_series_source FROM catalog_entities AS e WHERE e.id=? AND e.library_id=?`
const rbFilterMediaAttributionOctetsSQL = `SELECT octet_length(e.reading_author_value),octet_length(e.reading_author_key),octet_length(e.reading_series_value),octet_length(e.reading_series_key),octet_length(e.reading_series_id) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=?`

// The author and series facet points prove the accepted attribution row named by
// its own current key still exists in this library and kind. They are not raw
// text tag lookups and are not inferred from another source.
const rbFilterMediaAuthorFacetSQL = `SELECT 1 FROM catalog_entities AS e INDEXED BY reading_entity_author_facet WHERE e.library_id=? AND e.kind=? AND e.reading_author_key=? AND e.reading_author_value=? AND has_reading_attribution_members=1 AND e.id=?`
const rbFilterMediaSeriesFacetSQL = `SELECT 1 FROM catalog_entities AS e INDEXED BY reading_entity_series_facet WHERE e.library_id=? AND e.kind=? AND e.reading_series_key=? AND e.reading_series_value=? AND has_reading_attribution_members=1 AND e.id=?`

// A version is one accepted asset link in the title's own order. The asset
// identity is joined by its (library_id,id) unique key; a missing asset row is
// returned as a NULL identity instead of being hidden from the version set.
const rbFilterMediaLinksSQL = `SELECT l.asset_id,l.part_index,a.token FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_order LEFT JOIN catalog_assets AS a ON a.id=l.asset_id AND a.library_id=l.library_id AND a.library_id=? WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id LIMIT ?`
const rbFilterMediaLinksOctetsSQL = `SELECT l.asset_id,octet_length(a.token) FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_order LEFT JOIN catalog_assets AS a ON a.id=l.asset_id AND a.library_id=l.library_id AND a.library_id=? WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id LIMIT ?`

const rbFilterMediaAssetSQL = `SELECT a.token,a.path,a.available,a.size,a.modified_ns,a.height,a.duration,a.info_publication_pending,a.library_id FROM catalog_assets AS a WHERE a.id=?`
const rbFilterMediaAssetOctetsSQL = `SELECT octet_length(a.token),octet_length(a.path) FROM catalog_assets AS a WHERE a.id=?`

// A local archive source is a separate accepted fact for cb7/cbr formats.
const rbFilterMediaAssetLocalSQL = `SELECT 1 FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY library_sources_library ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? AND o.retired=0 AND o.root_incarnation=s.incarnation AND o.size=? AND o.modified_ns=? AND s.kind='local' LIMIT 1`

const rbFilterMediaSourcesSQL = `SELECT id,root,configured_root,enabled,health,incarnation FROM library_sources INDEXED BY library_sources_library WHERE library_id=? ORDER BY id LIMIT ?`
const rbFilterMediaSourcesOctetsSQL = `SELECT octet_length(id),octet_length(root),octet_length(configured_root),octet_length(health),octet_length(incarnation) FROM library_sources INDEXED BY library_sources_library WHERE library_id=? ORDER BY id LIMIT ?`
const rbFilterMediaInventoryExistsSQL = `SELECT 1 FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY library_sources_library ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? LIMIT 1`
const rbFilterMediaInventoryReadySQL = `SELECT 1 FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY library_sources_library ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND s.enabled=1 AND s.health NOT IN('root_changed','removing') AND o.root_incarnation=s.incarnation LIMIT 1`
const rbFilterMediaRetiredSQL = `SELECT item_id FROM dvr_catalog_retirements WHERE item_id=?`

// Streams are accepted only with their own size/modified_ns identity fact for
// the same asset. The caller compares those values with the asset point.
const rbFilterMediaStreamsSQL = `SELECT s.stream_index,s.type,s.language,s.detail_json,f.revision,f.size,f.modified_ns FROM asset_streams AS s LEFT JOIN asset_stream_facts AS f INDEXED BY sqlite_autoindex_asset_stream_facts_1 ON f.asset_id=s.asset_id WHERE s.asset_id=? AND s.stream_index>? ORDER BY s.stream_index LIMIT ?`
const rbFilterMediaStreamsOctetsSQL = `SELECT s.stream_index,octet_length(s.language),octet_length(s.detail_json) FROM asset_streams AS s WHERE s.asset_id=? AND s.stream_index>? ORDER BY s.stream_index LIMIT ?`
const rbFilterMediaSubtitlesSQL = `SELECT t.id,t.language,f.revision,f.size,f.modified_ns FROM asset_subtitles AS t INDEXED BY asset_subtitles_asset LEFT JOIN asset_subtitle_facts AS f INDEXED BY sqlite_autoindex_asset_subtitle_facts_1 ON f.asset_id=t.asset_id WHERE t.asset_id=? AND t.id>? ORDER BY t.id LIMIT ?`
const rbFilterMediaSubtitlesOctetsSQL = `SELECT t.id,octet_length(t.language) FROM asset_subtitles AS t INDEXED BY asset_subtitles_asset WHERE t.asset_id=? AND t.id>? ORDER BY t.id LIMIT ?`

// Only an accepted Reading publication is a Reading version. Publication type
// is not a filter field, so its source row is not read here.
const rbFilterMediaPublicationSQL = `SELECT p.asset_id,p.format FROM reading_publications AS p WHERE p.item_id=? AND p.asset_id>? ORDER BY p.asset_id LIMIT ?`
const rbFilterMediaPublicationOctetsSQL = `SELECT p.asset_id,octet_length(p.format) FROM reading_publications AS p WHERE p.item_id=? AND p.asset_id>? ORDER BY p.asset_id LIMIT ?`

// RBMediaFilterDefinitions returns every inert statement the media filter facts
// kernel can use. Composition must Capture the actual Reader.
func RBMediaFilterDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	linkPlan := stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_order"}, PrimaryKeys: []string{"a"}, Seeks: []string{"entity_id=?", "rowid=?"}}
	assetPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"a"}, Seeks: []string{"rowid=?"}}
	inventoryPlan := stmt.Plan{Hot: true, Indexes: []string{"inventory_objects_reading_asset_object", "library_sources_library"}, Seeks: []string{"asset_id=?", "library_id=?"}}
	authorPlan := stmt.Plan{Hot: true, Indexes: []string{"reading_entity_author_facet"}, Seeks: []string{"library_id=? AND kind=? AND reading_author_key=? AND reading_author_value=? AND id=?"}}
	seriesPlan := stmt.Plan{Hot: true, Indexes: []string{"reading_entity_series_facet"}, Seeks: []string{"library_id=? AND kind=? AND reading_series_key=? AND reading_series_value=? AND id=?"}}
	pk := func(table, name string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "asset_id", Collation: "BINARY"}}}
	}
	streamFacts := pk("asset_stream_facts", "sqlite_autoindex_asset_stream_facts_1")
	subtitleFacts := pk("asset_subtitle_facts", "sqlite_autoindex_asset_subtitle_facts_1")
	streamPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_stream_facts_1"}, AutoIndexes: []stmt.AutoIndexBinding{streamFacts}, Seeks: []string{"asset_id=?"}}
	subtitlePlan := stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset", "sqlite_autoindex_asset_subtitle_facts_1"}, AutoIndexes: []stmt.AutoIndexBinding{subtitleFacts}, Seeks: []string{"asset_id=? AND id>?", "asset_id=?"}}
	publicationPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"p"}}
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, cohorts []stmt.Cohort, plan stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("rb.filter.media." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rb_filter_media_facts.go:readRBMediaFilterFacts"}, Params: params, Cohorts: cohorts, Plan: plan})
	}
	numberPoint := func(p stmt.Param) []stmt.Cohort {
		return []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1)}}}
	}
	textPoint := func() []stmt.Cohort {
		return []stmt.Cohort{{Name: "present", Bindings: []any{"asset", "movies"}}, {Name: "empty", Bindings: []any{"missing", "movies"}}}
	}
	add("attribution", rbFilterMediaAttributionSQL, []stmt.Param{i("item"), i("library")}, numberPoint(i("item")), stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}})
	add("attribution.bytes", rbFilterMediaAttributionOctetsSQL, []stmt.Param{i("item"), i("library")}, numberPoint(i("item")), stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}})
	add("attribution.author", rbFilterMediaAuthorFacetSQL, []stmt.Param{i("library"), i("kind"), s("key"), s("value"), i("item")}, []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), "a", "A", int64(1)}}, {Name: "empty", Bindings: []any{int64(1), int64(1), "a", "A", int64(999999)}}}, authorPlan)
	add("attribution.series", rbFilterMediaSeriesFacetSQL, []stmt.Param{i("library"), i("kind"), s("key"), s("value"), i("item")}, []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), "s", "S", int64(1)}}, {Name: "empty", Bindings: []any{int64(1), int64(1), "s", "S", int64(999999)}}}, seriesPlan)
	linkCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(rbMediaLinkCap + 1)}}, {Name: "empty", Bindings: []any{int64(1), int64(999999), int64(rbMediaLinkCap + 1)}}}
	add("links", rbFilterMediaLinksSQL, []stmt.Param{i("library"), i("item"), i("limit")}, linkCohorts, linkPlan)
	add("links.bytes", rbFilterMediaLinksOctetsSQL, []stmt.Param{i("library"), i("item"), i("limit")}, linkCohorts, linkPlan)
	add("asset", rbFilterMediaAssetSQL, []stmt.Param{i("asset")}, []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(999999)}}}, assetPlan)
	add("asset.bytes", rbFilterMediaAssetOctetsSQL, []stmt.Param{i("asset")}, []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(999999)}}}, assetPlan)
	add("asset.local", rbFilterMediaAssetLocalSQL, []stmt.Param{s("asset"), s("library"), i("size"), i("mtime")}, []stmt.Cohort{{Name: "present", Bindings: []any{"asset", "movies", int64(1000), int64(1)}}, {Name: "empty", Bindings: []any{"missing", "movies", int64(1000), int64(1)}}}, inventoryPlan)
	sourceCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"movies", int64(rbMediaSourceCap + 1)}}, {Name: "empty", Bindings: []any{"missing", int64(rbMediaSourceCap + 1)}}}
	sourcePlan := stmt.Plan{Hot: true, Indexes: []string{"library_sources_library"}, Seeks: []string{"library_id=?"}}
	add("sources", rbFilterMediaSourcesSQL, []stmt.Param{s("library"), i("limit")}, sourceCohorts, sourcePlan)
	add("sources.bytes", rbFilterMediaSourcesOctetsSQL, []stmt.Param{s("library"), i("limit")}, sourceCohorts, sourcePlan)
	add("inventory.exists", rbFilterMediaInventoryExistsSQL, []stmt.Param{s("asset"), s("library")}, textPoint(), inventoryPlan)
	add("inventory.ready", rbFilterMediaInventoryReadySQL, []stmt.Param{s("asset"), s("library")}, textPoint(), inventoryPlan)
	add("retired", rbFilterMediaRetiredSQL, []stmt.Param{i("item")}, []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(999999)}}}, stmt.Plan{Hot: true, PrimaryKeys: []string{"dvr_catalog_retirements"}})
	streamCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"asset", int64(-1), int64(rbMediaStreamCap + 1)}}, {Name: "empty", Bindings: []any{"missing", int64(-1), int64(rbMediaStreamCap + 1)}}}
	add("streams", rbFilterMediaStreamsSQL, []stmt.Param{s("asset"), i("after"), i("limit")}, streamCohorts, streamPlan)
	add("streams.bytes", rbFilterMediaStreamsOctetsSQL, []stmt.Param{s("asset"), i("after"), i("limit")}, streamCohorts, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index>?"}})
	subtitleCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"asset", "", int64(rbMediaSubtitleCap + 1)}}, {Name: "empty", Bindings: []any{"missing", "", int64(rbMediaSubtitleCap + 1)}}}
	add("streams.subtitles", rbFilterMediaSubtitlesSQL, []stmt.Param{s("asset"), s("after"), i("limit")}, subtitleCohorts, subtitlePlan)
	add("streams.subtitles.bytes", rbFilterMediaSubtitlesOctetsSQL, []stmt.Param{s("asset"), s("after"), i("limit")}, subtitleCohorts, stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id>?"}})
	publicationCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(0), int64(rbMediaPublicationCap + 1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(0), int64(rbMediaPublicationCap + 1)}}}
	add("publication", rbFilterMediaPublicationSQL, []stmt.Param{i("item"), i("after"), i("limit")}, publicationCohorts, publicationPlan)
	add("publication.bytes", rbFilterMediaPublicationOctetsSQL, []stmt.Param{i("item"), i("after"), i("limit")}, publicationCohorts, publicationPlan)
	return out
}
