package catalog

import "github.com/PorticoMediaServer/portico/server/internal/readingcap"

// The latest saved version must still be the exact admitted physical source.
// This is an indexed per-item existence check, not an AV resume conversion.
func savedReadingInProgressSQL(profile string) string {
	eligible := func(object, source string) string {
		return object + `.retired=0 AND ` + object + `.state='available' AND ` + object + `.unsupported_reason='' AND ` + object + `.root_incarnation=` + source + `.incarnation
 AND ` + object + `.size=asset.size AND ` + object + `.modified_ns=asset.modified_ns AND ` + source + `.enabled=1 AND ` + source + `.health NOT IN('offline','root_changed','removing')`
	}
	return `EXISTS(SELECT 1 FROM reading_progress rp
 JOIN reading_publications publication ON publication.item_id=rp.item_id AND publication.asset_id=rp.asset_id
 JOIN catalog_asset_links link ON link.entity_id=rp.item_id AND link.asset_id=rp.asset_id AND link.available=1
 JOIN catalog_assets asset ON asset.id=rp.asset_id AND asset.available=1
 JOIN inventory_item_availability available ON available.item_id=rp.item_id AND available.asset_id=asset.token AND available.available=1
 JOIN catalog_libraries cl ON cl.id=i.library_id JOIN libraries lib ON lib.id=cl.library_id
 JOIN inventory_objects observed INDEXED BY inventory_objects_asset ON observed.asset_id=asset.token
 JOIN library_sources source ON source.id=observed.source_id AND source.library_id=cl.library_id
 WHERE rp.owner_key=` + profile + ` AND rp.item_id=i.id AND rp.progression>0 AND rp.progression<1
 AND rp.format=publication.format
 AND ((i.kind=20 AND lib.kind='book' AND publication.format IN('epub','pdf')) OR (i.kind=21 AND lib.kind='comic' AND publication.format IN(` + readingcap.ComicFormatsSQL() + `))) AND ` + readingcap.LocalComicArchiveSQL("publication") + `
 AND ` + eligible("observed", "source") + `
 AND NOT EXISTS(SELECT 1 FROM reading_progress newer WHERE newer.owner_key=rp.owner_key AND newer.item_id=rp.item_id
 AND (newer.observed_at>rp.observed_at OR (newer.observed_at=rp.observed_at AND newer.asset_id>rp.asset_id)))
 AND NOT EXISTS(SELECT 1 FROM inventory_objects earlier INDEXED BY inventory_objects_asset
 JOIN library_sources earlier_source ON earlier_source.id=earlier.source_id AND earlier_source.library_id=cl.library_id
 WHERE earlier.asset_id=asset.token AND earlier.id<observed.id AND ` + eligible("earlier", "earlier_source") + `)
 AND rp.source_revision=portico_reading_revision(i.id,publication.asset_id,publication.format,asset.token,asset.size,asset.modified_ns,
 source.id,source.generation,source.incarnation,source.root_identity,observed.relative_path,observed.object_identity,observed.revision,observed.evidence_json))`
}
