package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Extra preparation resolves one member row, its owner, its links and its
// episode asset boundary. library_sources holds no extra facts: item_extras
// stores the TEXT library key, while every link row stores the numeric catalogue
// library, so both are compared before a member is published.
const iaExtraEntitySQL = `SELECT id,sort_key,public_id,title,retired,revision,admission_own_age,kind,metadata_incarnation,source_revision FROM catalog_entities WHERE id=? AND library_id=?`
const iaExtraEntityOctetsSQL = `SELECT octet_length(sort_key),octet_length(public_id),octet_length(title),octet_length(metadata_incarnation) FROM catalog_entities WHERE id=? AND library_id=?`

// item_extras.item_id is the INTEGER PRIMARY KEY, so this exact point is a
// native rowid seek. Owner and path membership stay in their declared indexes.
const iaExtraSQL = `SELECT library_id,parent_path,owner_id,kind,title,asset_id,relative_path FROM item_extras WHERE item_id=?`
const iaExtraOctetsSQL = `SELECT octet_length(library_id),octet_length(parent_path),octet_length(kind),octet_length(title),octet_length(asset_id),octet_length(relative_path) FROM item_extras WHERE item_id=?`

// The link payload is exactly the merged IA audio link shape. One asset may
// carry separate editions, parts and CUE ordinals; none is collapsed.
const iaExtraLinkSQL = `SELECT asset_id,part_index,edition,available,cue_ordinal,start_seconds,end_seconds,playback_origin_incarnation,playback_origin_revision,info_pending_change FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?`

// An asset can be a member of more than one entity (deduplication). This bounded
// reverse window proves the extra's own membership from the asset side and
// refuses a row whose link belongs to another library.
const iaExtraMembershipSQL = `SELECT entity_id,part_index,library_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=? ORDER BY entity_id LIMIT ?`

// episode_asset_boundaries is keyed on (item_id,asset_id); asset_id is the asset
// TOKEN. Boundary evidence states whether this exact asset is a whole source or
// an unknown multi-episode boundary, and never invents a third state.
const iaExtraBoundarySQL = `SELECT item_id,asset_id,status FROM episode_asset_boundaries WHERE item_id=? AND asset_id=?`
const iaExtraBoundaryOctetsSQL = `SELECT octet_length(asset_id),octet_length(status) FROM episode_asset_boundaries WHERE item_id=? AND asset_id=?`

// The owner window is the exact ordered item_extras_owner prefix; the path
// window is the exact ordered item_extras_parent point. Both limit RAW
// positions before live rejection and order by the trailing item id.
const iaExtraOwnerSQL = `SELECT item_id FROM item_extras INDEXED BY item_extras_owner WHERE owner_id=? AND kind=? AND item_id>? ORDER BY item_id LIMIT ?`
const iaExtraPathSQL = `SELECT item_id FROM item_extras INDEXED BY item_extras_parent WHERE library_id=? AND parent_path=? AND kind=? AND title=? AND item_id>? ORDER BY item_id LIMIT ?`

// Links are ordered by the canonical link order key. One member owns at most 16
// link rows; the lookahead row proves an over-cap cohort instead of truncating it.
const iaExtraLinksSQL = `SELECT asset_id,part_index FROM catalog_asset_links INDEXED BY catalog_asset_links_order WHERE entity_id=? ORDER BY part_index,asset_id LIMIT 17`

// The token is unique per library, so this point proves that the member's stored
// token still names an asset in the admitted library before any link is trusted.
const iaExtraAssetSQL = `SELECT id FROM catalog_assets WHERE token=? AND library_id=?`

// IAExtraHarnessDefinitions is inert harness composition, not registration or
// authorization. Capture these exact definitions on the actual reader first.
func IAExtraHarnessDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, present, empty []any, p stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("foreground.ia.extra." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read/ia_extra", CallSites: []string{"store/read/ia_extra.go:IAExtraMembership"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	// The link payload shares the merged IA link octet preflight; only its own
	// payload ID is new, so the shared ID is never registered twice.
	out = append(out, iaLinkOctetsDefinition([]any{int64(3003), int64(30), int64(1)}))
	add("entity_octets", iaExtraEntityOctetsSQL, []stmt.Param{i("entity"), i("library")}, []any{int64(3001), int64(1)}, []any{int64(9999999), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}})
	add("entity", iaExtraEntitySQL, []stmt.Param{i("entity"), i("library")}, []any{int64(3001), int64(1)}, []any{int64(9999999), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}})
	add("extra_octets", iaExtraOctetsSQL, []stmt.Param{i("item")}, []any{int64(3003)}, []any{int64(9999999)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"item_extras"}, Seeks: []string{"rowid=?"}})
	add("extra", iaExtraSQL, []stmt.Param{i("item")}, []any{int64(3003)}, []any{int64(9999999)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"item_extras"}, Seeks: []string{"rowid=?"}})
	add("owner", iaExtraOwnerSQL, []stmt.Param{i("owner"), s("kind"), i("after"), i("limit")}, []any{int64(3001), "trailer", int64(0), int64(MaxRaw)}, []any{int64(9999999), "trailer", int64(0), int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{"item_extras_owner"}, Seeks: []string{"owner_id=? AND kind=? AND item_id>?"}})
	add("path", iaExtraPathSQL, []stmt.Param{s("library"), s("parent"), s("kind"), s("title"), i("after"), i("limit")}, []any{"movies", "Movies", "trailer", "Trailer", int64(0), int64(MaxRaw)}, []any{"movies", "Missing", "trailer", "Trailer", int64(0), int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{"item_extras_parent"}, Seeks: []string{"library_id=? AND parent_path=? AND kind=? AND title=? AND item_id>?"}})
	add("links", iaExtraLinksSQL, []stmt.Param{i("entity")}, []any{int64(3003)}, []any{int64(9999999)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_order"}, Seeks: []string{"entity_id=?"}})
	add("link", iaExtraLinkSQL, []stmt.Param{i("entity"), i("asset"), i("library")}, []any{int64(3003), int64(30), int64(1)}, []any{int64(9999999), int64(30), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}})
	add("asset", iaExtraAssetSQL, []stmt.Param{s("token"), i("library")}, []any{"extra30", int64(1)}, []any{"missing", int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_assets_1"}, Seeks: []string{"token=?"}})
	add("membership", iaExtraMembershipSQL, []stmt.Param{i("asset"), i("limit")}, []any{int64(30), int64(MaxRaw + 1)}, []any{int64(9999999), int64(MaxRaw + 1)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_asset"}, Seeks: []string{"asset_id=?"}})
	add("boundary", iaExtraBoundarySQL, []stmt.Param{i("item"), s("asset")}, []any{int64(3003), "extra30"}, []any{int64(9999999), "extra30"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_episode_asset_boundaries_1"}, Seeks: []string{"item_id=? AND asset_id=?"}})
	add("boundary_octets", iaExtraBoundaryOctetsSQL, []stmt.Param{i("item"), s("asset")}, []any{int64(3003), "extra30"}, []any{int64(9999999), "extra30"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_episode_asset_boundaries_1"}, Seeks: []string{"item_id=? AND asset_id=?"}})
	return out
}
