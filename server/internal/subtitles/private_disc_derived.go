package subtitles

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strconv"
)

// DerivePrivateDiscResourcesTx copies only the original personal measured
// resources into a sealed pending CHILD of the same session/source/facts.
// Cached artifact references are copied, never bytes or another owner's rows.
// The caller holds the opaque derived Store claim and both accepting proofs.
func DerivePrivateDiscResourcesTx(ctx context.Context, tx *sql.Tx, p identity.Principal, parent, child, selected string, check func(context.Context, *sql.Tx) error) (string, error) {
	if tx == nil || check == nil || !validID(parent) || !validID(child) || parent == child {
		return "", ErrInput
	}
	if err := dbwork.RequireGatedWriteTx(tx); err != nil {
		return "", err
	}
	if err := check(ctx, tx); err != nil {
		return "", err
	}
	var session, asset, item string
	err := tx.QueryRowContext(ctx, `SELECT c.session_id,cs.asset_id,pid(item.public_id) FROM playback_disc_snapshots p JOIN playback_disc_snapshots c ON c.session_id=p.session_id AND c.generation=p.generation+1 JOIN playback_disc_lifecycle pl ON pl.snapshot_id=p.id JOIN playback_disc_lifecycle cl ON cl.snapshot_id=c.id JOIN playback_disc_snapshot_source ps ON ps.snapshot_id=p.id JOIN playback_disc_snapshot_source cs ON cs.snapshot_id=c.id JOIN playback_disc_snapshot_disc pd ON pd.snapshot_id=p.id JOIN playback_disc_snapshot_disc cd ON cd.snapshot_id=c.id JOIN playback_sessions actual ON actual.id=c.session_id AND actual.generation=c.generation JOIN catalog_entities item ON item.id=actual.item_id WHERE p.id=? AND c.id=? AND pl.state='active' AND cl.state='pending' AND cl.sealed=1 AND ps.asset_id=cs.asset_id AND ps.inventory_object_id=cs.inventory_object_id AND ps.inventory_revision=cs.inventory_revision AND pd.facts_revision=cd.facts_revision AND pd.selected_title_key=cd.selected_title_key`, parent, child).Scan(&session, &asset, &item)
	if err != nil {
		return "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.current_revision,map.stream_index FROM playback_disc_subtitle_resources map JOIN subtitle_resources r ON r.id=map.resource_id WHERE map.snapshot_id=? AND r.owner=? AND r.scope='personal' AND r.deleted=0 AND r.source_id=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) ORDER BY map.stream_index LIMIT 129`, parent, actor(p), asset, item)
	if err != nil {
		return "", err
	}
	type original struct {
		id       string
		revision int64
		stream   int
	}
	var originals []original
	for rows.Next() {
		var o original
		if err = rows.Scan(&o.id, &o.revision, &o.stream); err != nil {
			break
		}
		originals = append(originals, o)
		if len(originals) > MaxTracks {
			err = ErrCapacity
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return "", err
	}
	mapped := ""
	for _, o := range originals {
		id := stable("private-disc-subtitle-v1:" + child + ":" + strconv.Itoa(o.stream))
		if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,deleted,discovered,discovery_id,discovery_revision) SELECT ?,item_id,source_id,scope,owner,current_revision,0,0,'',1 FROM subtitle_resources WHERE id=?`, id, o.id); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,provider,attribution,rights,original_digest,offset_us,source_size,source_modified_ns,source_facts_revision,created_at) SELECT ?,revision,digest,size,format,language,title,origin,provider,attribution,rights,original_digest,offset_us,source_size,source_modified_ns,source_facts_revision,created_at FROM subtitle_revisions WHERE resource_id=? AND revision=?`, id, o.id, o.revision); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_render_revisions(resource_id,revision,renderer,is_default,is_forced,source_evidence,hearing_impaired) SELECT ?,revision,renderer,is_default,is_forced,source_evidence,hearing_impaired FROM subtitle_render_revisions WHERE resource_id=? AND revision=?`, id, o.id, o.revision); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_original_revisions(resource_id,revision,digest,size,skipped_fonts,forced_lines,total_lines) SELECT ?,revision,digest,size,skipped_fonts,forced_lines,total_lines FROM subtitle_original_revisions WHERE resource_id=? AND revision=?`, id, o.id, o.revision); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_original_files(resource_id,revision,part,name,format,encoding,digest,size) SELECT ?,revision,part,name,format,encoding,digest,size FROM subtitle_original_files WHERE resource_id=? AND revision=?`, id, o.id, o.revision); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO playback_disc_subtitle_resources(snapshot_id,resource_id,stream_index) VALUES(?,?,?)`, child, id, o.stream); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO playback_subtitle_pins(session_id,resource_id,resource_revision) VALUES(?,?,?)`, session, id, o.revision); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO playback_manifest_subtitles SELECT session_id,?,resource_revision FROM playback_manifest_subtitles WHERE session_id=? AND resource_id=? AND resource_revision=?`, id, session, o.id, o.revision); err != nil {
			return "", err
		}
		if o.id == selected {
			mapped = id
		}
	}
	if selected != "" && mapped == "" {
		return "", ErrConflict
	}
	return mapped, check(ctx, tx)
}
