package disc

import (
	"context"
	"database/sql"
)

// CheckPlaybackSelectionTx checks only the private title/source binding. The
// caller must also retain its live actor, session, generation and grant guards.
func CheckPlaybackSelectionTx(ctx context.Context, tx *sql.Tx, id string) error {
	var bad bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_pins p WHERE p.session_id=? AND NOT EXISTS(
 SELECT 1 FROM inventory_disc_facts d JOIN inventory_objects o ON o.id=d.object_id AND o.asset_id=d.asset_id
 JOIN library_sources s ON s.id=o.source_id JOIN playback_sessions ps ON ps.asset_id=d.asset_id AND ps.id=p.session_id
 JOIN catalog_assets a ON a.token=d.asset_id
 JOIN asset_stream_facts st ON st.asset_id=d.asset_id AND st.size=a.size AND st.modified_ns=a.modified_ns
 JOIN asset_chapter_facts ch ON ch.asset_id=d.asset_id AND ch.size=a.size AND ch.modified_ns=a.modified_ns
 WHERE d.object_id=p.object_id AND d.fingerprint=p.fingerprint AND d.source_revision=p.source_revision
 AND EXISTS(SELECT 1 FROM inventory_disc_title_media m WHERE m.object_id=p.object_id AND m.title_key=p.title_key AND m.source_revision=p.source_revision AND m.fingerprint=p.fingerprint AND m.revision=p.title_facts_revision)
 AND ((p.stream_revision>0 AND d.media_title_key=p.title_key AND d.media_stream_revision=p.stream_revision AND d.media_chapter_revision=p.chapter_revision AND st.revision=p.stream_revision AND ch.revision=p.chapter_revision)
 OR (p.stream_revision=0 AND p.chapter_revision=0))
 AND d.encryption='none' AND o.revision=d.source_revision AND o.retired=0 AND o.state='available' AND s.enabled=1 AND s.kind='local'
 AND d.source_generation=s.generation AND d.root_incarnation=s.incarnation AND o.root_incarnation=s.incarnation))`, id).Scan(&bad)
	if err != nil {
		return err
	}
	if bad {
		return ErrProtocol
	}
	return nil
}
