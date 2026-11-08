package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The composed source binds every current head that the common detail DTO is
// built from: entity revisions, the sealed credit image, and the immediate
// show/season/album/book owners used by artwork inheritance. It adds one
// composed typed point; the individual entity, asset, publication and
// credit-image reads remain owned by their existing statement families.
const rsDetailComposedSourceSQL = `SELECT e.id,e.kind,e.retired,e.admission_own_age,e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,e.artwork_incarnation,e.artwork_revision,e.admission_revision,e.show_id,e.season_id,e.album_id,e.book_id,
sh.id,sh.kind,sh.retired,sh.admission_own_age,sh.metadata_incarnation,sh.source_revision,sh.identity_revision,sh.metadata_revision,sh.artwork_incarnation,sh.artwork_revision,sh.admission_revision,sh.show_id,sh.season_id,sh.album_id,sh.book_id,
se.id,se.kind,se.retired,se.admission_own_age,se.metadata_incarnation,se.source_revision,se.identity_revision,se.metadata_revision,se.artwork_incarnation,se.artwork_revision,se.admission_revision,se.show_id,se.season_id,se.album_id,se.book_id,
al.id,al.kind,al.retired,al.admission_own_age,al.metadata_incarnation,al.source_revision,al.identity_revision,al.metadata_revision,al.artwork_incarnation,al.artwork_revision,al.admission_revision,al.show_id,al.season_id,al.album_id,al.book_id,
bk.id,bk.kind,bk.retired,bk.admission_own_age,bk.metadata_incarnation,bk.source_revision,bk.identity_revision,bk.metadata_revision,bk.artwork_incarnation,bk.artwork_revision,bk.admission_revision,bk.show_id,bk.season_id,bk.album_id,bk.book_id,
e.revision,e.scalar_source_revision,e.aggregate_revision,e.credit_owner_revision,h.sealed
FROM catalog_entities AS e NOT INDEXED
LEFT JOIN catalog_credit_images AS h INDEXED BY catalog_credit_images_entity ON h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision AND h.id=e.credit_image_id
LEFT JOIN catalog_entities AS sh NOT INDEXED ON sh.id=e.show_id AND sh.library_id=e.library_id AND sh.kind=? AND sh.retired=0
LEFT JOIN catalog_entities AS se NOT INDEXED ON se.id=e.season_id AND se.library_id=e.library_id AND se.kind=? AND se.retired=0 AND se.show_id=e.show_id
LEFT JOIN catalog_entities AS al NOT INDEXED ON al.id=e.album_id AND al.library_id=e.library_id AND al.kind=? AND al.retired=0
LEFT JOIN catalog_entities AS bk NOT INDEXED ON bk.id=e.book_id AND bk.library_id=e.library_id AND bk.kind=? AND bk.retired=0
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

// The composed item point is the RD2 item shape plus the complete current
// profile projection. It selects only the exact entity and personal-current
// primary keys; no profile blob or cross-profile fallback is read.
const rsDetailComposedItemSQL = `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.overview,e.credit_image_id,
p.revision,p.resume_position,p.resume_unit,p.play_count,p.favorite,p.watchlisted,p.watched,p.resume_completed,p.not_interested,p.rating
FROM catalog_entities AS e
LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

const rsDetailComposedItemOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview)
FROM catalog_entities AS e
LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

func init() {
	registerRSBytePoint("rs.detail_composed.item", rsBytePoint{lengths: "rs.detail_composed.item_octets", columns: []int{1, 2, 3, 7}})
}

// RSDetailComposedDefinitions supplies the owned inert point SQL for one
// composed detail. Merged HarnessDefinitions, TitleFactsDefinitions,
// ArtworkDefinitions and ReadingFactsDefinitions are composed beside it and
// keep their own registrations.
func RSDetailComposedDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	sourceParams := []stmt.Param{i("show_kind"), i("season_kind"), i("album_kind"), i("book_kind"), i("item"), i("library"), i("kind")}
	sourcePresent := rsArtSourceArgs(1101, 1, 1)
	sourceEmpty := rsArtSourceArgs(999999, 1, 1)
	itemParams := []stmt.Param{s("profile"), i("item"), i("library"), i("kind")}
	itemPresent := []any{"p", int64(1101), int64(1), int64(1)}
	itemEmpty := []any{"p", int64(999999), int64(1), int64(1)}
	return []stmt.Definition{
		{ID: readStatementID("rs.detail_composed.source"), SQL: rsDetailComposedSourceSQL, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_detail_composed.go:RSDetailComposedReader"}, Params: sourceParams, Cohorts: []stmt.Cohort{{Name: "present", Bindings: sourcePresent}, {Name: "empty", Bindings: sourceEmpty}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "sh", "se", "al", "bk"}, Indexes: []string{"catalog_credit_images_entity"}}},
		{ID: readStatementID("rs.detail_composed.item"), SQL: rsDetailComposedItemSQL, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_detail_composed.go:RSDetailComposedReader"}, Params: itemParams, Cohorts: []stmt.Cohort{{Name: "present", Bindings: itemPresent}, {Name: "empty", Bindings: itemEmpty}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Indexes: []string{"personal_current_live"}}},
		{ID: readStatementID("rs.detail_composed.item_octets"), SQL: rsDetailComposedItemOctetsSQL, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_detail_composed.go:RSDetailComposedReader"}, Params: itemParams, Cohorts: []stmt.Cohort{{Name: "present", Bindings: itemPresent}, {Name: "empty", Bindings: itemEmpty}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
	}
}
