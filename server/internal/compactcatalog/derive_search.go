package compactcatalog

import (
	"context"
	"database/sql"
)

const searchLiveSQL = `e.retired=0 AND EXISTS(SELECT 1 FROM catalog_kinds k WHERE k.id=e.kind AND k.searchable=1)
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=e.id)
 AND (e.kind<>4 OR (e.parent_id IS NULL AND NOT EXISTS(SELECT 1 FROM catalog_episodes loose WHERE loose.entity_id=e.id)) OR EXISTS(SELECT 1 FROM catalog_episodes ep JOIN catalog_entities parent ON parent.id=ep.show_id AND parent.library_id=e.library_id AND parent.retired=0 WHERE ep.entity_id=e.id))
 AND (e.kind<>7 OR EXISTS(SELECT 1 FROM catalog_songs so JOIN catalog_entities parent ON parent.id=so.album_id AND parent.library_id=e.library_id AND parent.retired=0 WHERE so.entity_id=e.id))`

// Each context lookup is keyed by entity. Provider aliases are already accepted
// evidence; malformed provider JSON contributes no aliases. Context is bounded
// independently of the number of credits or aliases a provider supplies.
const searchContentSQL = `e.title||' '||e.sort_title||' '||CASE WHEN e.year>0 THEN e.year ELSE '' END||' '||
 COALESCE(d.original_title,'')||' '||COALESCE(sh.original_title,'')||' '||
 COALESCE((SELECT group_concat(name,' ') FROM (SELECT c.credited_name||' '||COALESCE(p.name,'') AS name FROM catalog_credits c LEFT JOIN catalog_people p ON p.id=c.person_id WHERE c.entity_id=e.id ORDER BY c.ord LIMIT 256)),'')||' '||
 COALESCE((SELECT group_concat(alias,' ') FROM (SELECT CAST(j.value AS TEXT) alias FROM screen_metadata_work w JOIN screen_metadata_publications p ON p.id=w.accepted_publication,json_each(CASE WHEN json_valid(p.payload) THEN p.payload ELSE '{}' END,'$.aliases') j WHERE w.target_id=e.id AND j.type='text' LIMIT 128)),'')||' '||
 COALESCE(b.author,'')||' '||COALESCE(b.narrator,'')||' '||COALESCE(b.series,'')||' '||
 COALESCE((SELECT group_concat(title,' ') FROM (SELECT a.title FROM catalog_song_artists sa JOIN catalog_entities a ON a.id=sa.artist_id AND a.library_id=e.library_id AND a.retired=0 WHERE sa.song_id=e.id LIMIT 64)),'')||' '||
 COALESCE((SELECT a.title||' '||ar.title FROM catalog_songs so JOIN catalog_entities a ON a.id=so.album_id JOIN catalog_albums al ON al.entity_id=a.id JOIN catalog_entities ar ON ar.id=al.artist_id WHERE so.entity_id=e.id AND a.library_id=e.library_id AND ar.library_id=e.library_id),'')||' '||
 COALESCE((SELECT ar.title FROM catalog_albums al JOIN catalog_entities ar ON ar.id=al.artist_id WHERE al.entity_id=e.id AND ar.library_id=e.library_id),'')||' '||
 COALESCE((SELECT sh.title FROM catalog_episodes ep JOIN catalog_entities sh ON sh.id=ep.show_id WHERE ep.entity_id=e.id AND sh.library_id=e.library_id),'')||' '||
 COALESCE((SELECT group_concat(title,' ') FROM (SELECT ce.title FROM catalog_collection_members cm JOIN catalog_entities ce ON ce.id=cm.collection_id AND ce.library_id=e.library_id AND ce.retired=0 WHERE cm.item_id=e.id LIMIT 64)),'')||' '||
 COALESCE((SELECT group_concat(alias,' ') FROM (SELECT CAST(j.value AS TEXT) alias FROM provider_evidence pe,json_each(CASE WHEN json_valid(pe.payload) THEN pe.payload ELSE '{}' END,'$.aliases') j WHERE pe.item_id=e.id AND j.type='text' LIMIT 128)),'')`

func deriveSearch(ctx context.Context, tx *sql.Tx, ids string) error {
	var slot, rebuilding, first int
	if err := tx.QueryRowContext(ctx, `SELECT active_slot,rebuilding,first_build FROM catalog_derivations WHERE domain=17`).Scan(&slot, &rebuilding, &first); err != nil && err != sql.ErrNoRows {
		return err
	}
	if rebuilding == 1 && first == 0 {
		slot = 1 - slot
	}
	for _, query := range []string{
		`DELETE FROM catalog_people_names WHERE rowid IN(SELECT person_id FROM catalog_credits WHERE entity_id IN(SELECT value FROM json_each(?)) AND person_id IS NOT NULL)`,
		`INSERT INTO catalog_people_names(rowid,name) SELECT p.id,portico_search_text(p.name) FROM catalog_people p WHERE p.id IN(SELECT person_id FROM catalog_credits WHERE entity_id IN(SELECT value FROM json_each(?)) AND person_id IS NOT NULL)`,
		// The index rows follow the documents through the tables' own triggers,
		// in whichever generation is being written.
		`DELETE FROM catalog_search_documents WHERE entity_id IN(SELECT value FROM json_each(?))`,
		`INSERT INTO catalog_search_documents(entity_id,title,title_fold,entity_pid,search,normalized,kind,library_id,entity_kind,container,rating_key,label_key)
 SELECT e.id,e.title,portico_search_fold(e.title),pid(e.public_id),portico_search_text(` + searchContentSQL + `),portico_search_fold(substr(` + searchContentSQL + `,1,65536)),'k'||e.kind||' l'||e.library_id,e.library_id,e.kind,NOT EXISTS(SELECT 1 FROM catalog_kinds k WHERE k.id=e.kind AND (k.playable=1 OR k.id IN(20,21))),` + browseAttributeKeySQL("contentRating") + `,` + browseAttributeKeySQL("label") + `
 FROM json_each(?) j JOIN catalog_entities e ON e.id=j.value
 LEFT JOIN catalog_item_details d ON d.entity_id=e.id LEFT JOIN catalog_shows sh ON sh.entity_id=e.id LEFT JOIN catalog_books b ON b.entity_id=e.id
 WHERE ` + searchLiveSQL,
	} {
		if _, err := tx.ExecContext(ctx, searchTables(query, slot), ids); err != nil {
			return err
		}
	}
	for _, query := range []string{
		`INSERT INTO catalog_search_prefix_terms(prefix) SELECT DISTINCT json_extract(p.value,'$.prefix') FROM json_each(?) j CROSS JOIN catalog_search_documents d ON d.entity_id=j.value CROSS JOIN json_each(portico_search_prefixes(d.search,d.title_fold)) p WHERE 1 ON CONFLICT(prefix) DO NOTHING`,
		`INSERT INTO catalog_search_prefix_rows(term_id,entity_id,kind,library_id,tier,container,rating_key,label_key)
 SELECT t.id,d.entity_id,d.entity_kind,d.library_id,json_extract(p.value,'$.tier'),d.container,d.rating_key,d.label_key
 FROM json_each(?) j CROSS JOIN catalog_search_documents d ON d.entity_id=j.value CROSS JOIN json_each(portico_search_prefixes(d.search,d.title_fold)) p
 CROSS JOIN catalog_search_prefix_terms t ON t.prefix=json_extract(p.value,'$.prefix')`,
	} {
		if _, err := tx.ExecContext(ctx, searchTables(query, slot), ids); err != nil {
			return err
		}
	}
	// A source hint may have reached readers before this queued batch ran.
	// Publishing the serving index must wake them again; the replacement slot
	// remains private until its generation switch publishes one invalidation.
	if rebuilding != 1 || first == 1 {
		_, err := tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1
 WHERE library_id IN(SELECT cl.library_id FROM json_each(?) j
 JOIN catalog_entities e ON e.id=j.value JOIN catalog_libraries cl ON cl.id=e.library_id)`, ids)
		return err
	}
	return nil
}

func init() {
	Register(DomainSearch, Derivation{
		Version: 10,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			ids := keyIDs(keys)
			return ids, deriveSearch(ctx, tx, mustJSON(ids))
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT id FROM catalog_entities WHERE id>? ORDER BY id LIMIT ?`, after, limit)
		},
	})
}
