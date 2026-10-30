package artworkref

// AudioFallbackKeys registers file-owned embedded covers independently of the
// current sidecar selection. Both the collector and backup closure consume the
// same currentness rule. Raw local-artwork bytes are also carried by backup.
// A disabled/offline source retains its baseline; a retired object, rebound
// root or changed file cannot pin an obsolete fallback indefinitely.
const AudioFallbackKeys = `SELECT b.artwork_key FROM audio_file_metadata b INDEXED BY audio_file_metadata_artwork_key
 JOIN catalog_assets a ON a.token=b.asset_id AND a.size=b.size AND a.modified_ns=b.modified_ns
 WHERE b.artwork_key<>'' AND EXISTS(
 SELECT 1 FROM inventory_objects o INDEXED BY inventory_objects_asset JOIN library_sources s ON s.id=o.source_id
 WHERE o.asset_id=b.asset_id AND o.revision=b.source_revision AND o.size=b.size AND o.modified_ns=b.modified_ns AND o.root_incarnation=s.incarnation AND o.retired=0)`
