package catalog

import "github.com/PorticoMediaServer/portico/server/internal/readingcap"

// These are fixed server-owned aliases and the scope's serialized captured
// format set, never request SQL. Both candidate membership and primary choice
// use the identical admitted formats, matching readingBrowseScope.eligibleSQL.
func readingEligiblePublicationSQL(entity, library, kind, formats string) string {
	return `EXISTS(SELECT 1 FROM catalog_entities reading_title
 JOIN reading_publications reading_publication ON reading_publication.item_id=reading_title.id
 JOIN catalog_asset_links reading_link ON reading_link.entity_id=reading_title.id AND reading_link.asset_id=reading_publication.asset_id
 WHERE reading_title.id=` + entity + ` AND reading_title.library_id=` + library + `
 AND reading_title.retired=0 AND ((` + kind + `='book' AND reading_title.kind=20) OR (` + kind + `='comic' AND reading_title.kind=21))
 AND reading_publication.format IN(SELECT value FROM json_each(` + formats + `)) AND ` + readingcap.LocalComicArchiveSQL("reading_publication") + `
 AND reading_publication.asset_id=(SELECT candidate.asset_id FROM reading_publications candidate WHERE candidate.item_id=reading_title.id
 AND candidate.format IN(SELECT value FROM json_each(` + formats + `)) AND ` + readingcap.LocalComicArchiveSQL("candidate") + `
 ORDER BY EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=candidate.item_id
 AND available.asset_id=(SELECT token FROM catalog_assets WHERE id=candidate.asset_id) AND available.available=1) DESC,candidate.asset_id LIMIT 1))`
}
