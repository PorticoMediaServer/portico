package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The first InventoryAccept effect recipe is NEW ordinary movie entities/assets
// only. These finite statements are composed by W; the domain handler will use
// the shared commandTurn and common receipt decision before fresh source CAS.
// Definitions alone are not an accepting command or an admission certificate.
const (
	scanInventoryIdentity stmt.ID = "scan.inventory_accept.identity"
	scanInventoryAsset    stmt.ID = "scan.inventory_accept.asset"
	scanInventorySubject  stmt.ID = "scan.inventory_accept.insert_subject"
	scanInventoryAlias    stmt.ID = "scan.inventory_accept.insert_identity"
	scanInventoryNewAsset stmt.ID = "scan.inventory_accept.insert_asset"
	scanInventoryEntity   stmt.ID = "scan.inventory_accept.insert_entity"
	scanInventoryLink     stmt.ID = "scan.inventory_accept.insert_link"
)

// These FIRST-slice bindings belong to the handler, never to caller-selected
// capabilities. A shared kind/search policy remains its owning lane's API.
const scanNewMovieKind int64 = 1

const scanInitialPlaybackOriginRevision int64 = 1

func scanInventoryDefinitions() []stmt.Definition {
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	real := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Real} }
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	blob := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Blob} }
	identity := make([]byte, 16)
	definitions := []stmt.Definition{
		{
			ID: scanInventoryIdentity, Kind: stmt.Read,
			SQL:     `SELECT subject_id FROM catalog_identities WHERE library_id=? AND source_key=?`,
			Params:  []stmt.Param{integer("library_id"), text("source_key")},
			Cohorts: []stmt.Cohort{{Name: "new_identity_miss", Bindings: []any{int64(1), "source-key"}}},
		},
		{
			ID: scanInventoryAsset, Kind: stmt.Read,
			SQL:     `SELECT id,token,info_epoch FROM catalog_assets WHERE library_id=? AND path=?`,
			Params:  []stmt.Param{integer("library_id"), text("path")},
			Cohorts: []stmt.Cohort{{Name: "library_scoped_asset_miss", Bindings: []any{int64(1), "/media/Arrival.mkv"}}},
		},
		{
			ID: scanInventorySubject, Kind: stmt.Write,
			SQL:     `INSERT INTO catalog_subjects(public_id,library_id,kind) VALUES(?,?,?) RETURNING id`,
			Params:  []stmt.Param{blob("public_id"), integer("library_id"), integer("kind")},
			Cohorts: []stmt.Cohort{{Name: "new_movie_subject", Bindings: []any{identity, int64(1), scanNewMovieKind}}},
		},
		{
			ID: scanInventoryAlias, Kind: stmt.Write,
			SQL:     `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(?,?,?)`,
			Params:  []stmt.Param{integer("library_id"), text("source_key"), integer("subject_id")},
			Cohorts: []stmt.Cohort{{Name: "same_library_source_identity", Bindings: []any{int64(1), "source-key", int64(1)}}},
		},
		{
			ID: scanInventoryNewAsset, Kind: stmt.Write,
			// Empty/zero technical fields mean no technical observation. Their
			// presence in this schema is NOT proof that Basic is ready. The
			// result epoch is read natively, never substituted with literal 1.
			SQL: `INSERT INTO catalog_assets(token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING id,token,info_epoch`,
			Params: []stmt.Param{text("asset_token"), integer("library_id"), text("path"), integer("size"), integer("modified_ns"),
				text("container"), text("video_codec"), text("audio_codec"), integer("width"), integer("height"), real("duration")},
			Cohorts: []stmt.Cohort{{Name: "new_movie_asset_info_epoch", Bindings: []any{"asset-token", int64(1), "/media/Arrival.mkv", int64(42), int64(1), "", "", "", int64(0), int64(0), float64(0)}}},
		},
		{
			ID: scanInventoryEntity, Kind: stmt.Write,
			// INT's FIRST source-to-publisher binding is the actual post-insert
			// metadata_incarnation + revision, not any asset/metadata counter.
			SQL: `INSERT INTO catalog_entities(
id,public_id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,
random_key,year,added_at,metadata_language,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
RETURNING id,metadata_incarnation,revision,playback_origin_incarnation,playback_origin_revision`,
			Params: []stmt.Param{integer("subject_id"), blob("public_id"), integer("library_id"), integer("kind"), text("title"), text("sort_title"), text("sort_key"), text("sort_head"),
				text("sort_key_keep_articles"), text("sort_head_keep_articles"), integer("random_key"), integer("year"), text("added_at"), text("metadata_language"),
				blob("metadata_incarnation"), blob("artwork_incarnation"), blob("playback_origin_incarnation"), integer("playback_origin_revision")},
			Cohorts: []stmt.Cohort{{Name: "filename_movie_new_source_witness", Bindings: []any{int64(1), identity, int64(1), scanNewMovieKind, "Arrival", "Arrival", "arrival", "a", "arrival", "a",
				int64(1), int64(2016), "2026-11-05T00:00:00.000000000Z", "en-US", identity, identity, identity, scanInitialPlaybackOriginRevision}}},
		},
		{
			ID: scanInventoryLink, Kind: stmt.Write,
			SQL:     `INSERT INTO catalog_asset_links(library_id,entity_id,asset_id,part_index) VALUES(?,?,?,?) RETURNING part_index`,
			Params:  []stmt.Param{integer("library_id"), integer("subject_id"), integer("asset_id"), integer("part_index")},
			Cohorts: []stmt.Cohort{{Name: "ordinary_same_library_link", Bindings: []any{int64(1), int64(1), int64(1), int64(0)}}},
		},
	}
	for i := range definitions {
		definitions[i].Owner = "store/writer scan owner adapter"
		definitions[i].CallSites = []string{"store/writer/scan_commands.go"}
		definitions[i].Plan = stmt.Plan{Hot: true}
		switch definitions[i].ID {
		case scanInventoryIdentity:
			definitions[i].Plan.PrimaryKeys = []string{"catalog_identities"}
			definitions[i].Plan.Seeks = []string{"library_id=? AND source_key=?"}
		case scanInventoryAsset:
			definitions[i].Plan.Indexes = []string{"sqlite_autoindex_catalog_assets_3"}
			definitions[i].Plan.Seeks = []string{"library_id=? AND path=?"}
		}
	}
	return definitions
}
