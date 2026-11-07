package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// TitleFactsDefinitions supplies inert point-read SQL for actual Reader Capture.
// HarnessDefinitions supplies the shared foreground source/authority statements.
func TitleFactsDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return []stmt.Definition{{ID: readStatementID("rs.detail.facts"), Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_detail.go:ForegroundReader"}, SQL: `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,
e.overview,e.year,e.original_title,e.tagline,e.edition,e.release_date,e.content_rating,e.studio,e.network,e.country,e.added_at,e.last_content_added_at,
e.hide_seasons,e.absolute_numbering,e.newest_first,e.show_settings_revision,e.show_ranges,
e.show_id,e.season_id,e.season_number,e.episode_number,e.numbering,e.air_date,e.local_identity_status,e.ordering_basis,
e.artist_id,e.record_label,e.release_country,e.edition_key,e.album_id,e.disc_number,e.track_number,e.author,e.narrator,e.series,e.series_position,e.book_id,e.part_number,e.collection_name_key,e.collection_revision
FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`, Params: []stmt.Param{i("item"), i("library"), i("kind")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}}}
}
