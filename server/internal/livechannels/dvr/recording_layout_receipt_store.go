package dvr

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const recordingLayoutColumns = `l.owner_key,l.recording_id,l.artifact_id,l.layout_revision,l.root_path,l.root_identity,l.source_identity,l.target_identity,l.source_relative_path,l.target_relative_path,l.target_base_relative_path,l.collision_ordinal,l.object_digest,l.byte_size,l.phase`

func readRecordingLayoutReceiptTx(ctx context.Context, tx *sql.Tx, id string) (RecordingLayoutReceipt, bool, error) {
	var out RecordingLayoutReceipt
	err := tx.QueryRowContext(ctx, `SELECT `+recordingLayoutColumns+` FROM dvr_recording_layouts l WHERE l.recording_id=?`, id).Scan(&out.OwnerKey, &out.RecordingID, &out.ArtifactKey, &out.LayoutRevision, &out.RootPath, &out.RootIdentity, &out.SourceIdentity, &out.TargetIdentity, &out.Source, &out.Target, &out.BaseTarget, &out.Collision, &out.Object.Digest, &out.Object.Size, &out.Phase)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if !out.Valid() {
		return RecordingLayoutReceipt{}, false, ErrInvalid
	}
	return out, true, nil
}

// IsRecordingLayoutPath adds physical reader leases to exact linked aliases.
// Permission still comes from the normal actor/library/recording grants. Neither
// a pathname prefix nor a stale receipt restores any revoked recording grant.
func (s *Store) IsRecordingLayoutPath(ctx context.Context, path string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s == nil || s.db == nil {
		return false, ErrUnavailable
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false, nil
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return false, err
	}
	defer snapshot.Rollback()
	var r RecordingLayoutReceipt
	var bound bool
	err = snapshot.Tx().QueryRowContext(ctx, `SELECT `+recordingLayoutColumns+`, EXISTS(
 SELECT 1 FROM dvr_recordings r
 JOIN dvr_catalog_provenance p ON p.recording_id=r.id AND p.item_id=r.item_id AND p.artifact_digest=r.artifact_digest
  AND p.source_id=r.source_id AND p.channel_id=r.channel_id AND p.programme_id=r.programme_id AND p.guide_generation=r.guide_generation
  AND p.captured_start_ms=r.captured_start_ms AND p.captured_end_ms=r.captured_end_ms
 JOIN catalog_assets a ON a.token=p.asset_id AND a.path=l.root_path||'/'||l.source_relative_path AND a.size=l.byte_size
 JOIN catalog_asset_links al ON al.asset_id=a.id AND al.entity_id=p.item_id AND al.available=1
 JOIN catalog_entities e ON e.id=p.item_id
 JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0
 JOIN dvr_private_libraries dl ON dl.library_id=cl.library_id AND dl.authority=r.authority AND dl.account_id=r.account_id AND dl.profile_id=r.profile_id
 WHERE r.id=l.recording_id AND r.owner_key=l.owner_key AND r.artifact_id=l.artifact_id AND r.artifact_digest=l.object_digest AND r.bytes=l.byte_size
  AND r.state IN('completed','incomplete-playable','pending-delete'))
 FROM dvr_recording_layouts l WHERE l.phase='linked' AND l.root_path||'/'||l.target_relative_path=?`, path).Scan(&r.OwnerKey, &r.RecordingID, &r.ArtifactKey, &r.LayoutRevision, &r.RootPath, &r.RootIdentity, &r.SourceIdentity, &r.TargetIdentity, &r.Source, &r.Target, &r.BaseTarget, &r.Collision, &r.Object.Digest, &r.Object.Size, &r.Phase, &bound)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !r.Valid() {
		return false, ErrInvalid
	}
	if !bound {
		return false, ErrConflict
	}
	return true, ctx.Err()
}

func sameRecordingLayoutReceipt(a, b RecordingLayoutReceipt) bool {
	a.Phase, b.Phase = "prepared", "prepared"
	a.TargetIdentity, b.TargetIdentity = "", ""
	return a == b
}

// reserveRecordingLayoutTx is before target IO. A distinct occurrence cannot
// acquire another recording's reserved filename, even when payloads are shared.
func reserveRecordingLayoutTx(ctx context.Context, tx *sql.Tx, r RecordingLayoutReceipt) (bool, error) {
	if !r.Valid() || r.Phase != "prepared" || r.TargetIdentity != "" {
		return false, ErrInvalid
	}
	prior, exists, err := readRecordingLayoutReceiptTx(ctx, tx, r.RecordingID)
	if err != nil {
		return false, err
	}
	if exists {
		if !sameRecordingLayoutReceipt(prior, r) {
			return false, ErrConflict
		}
		return true, nil
	}
	var owner, key string
	var digest string
	var size int64
	err = tx.QueryRowContext(ctx, `SELECT owner_key,artifact_id,artifact_digest,bytes FROM dvr_recordings WHERE id=?`, r.RecordingID).Scan(&owner, &key, &digest, &size)
	if err != nil {
		return false, err
	}
	if owner != r.OwnerKey || key != r.ArtifactKey || digest != r.Object.Digest || size != r.Object.Size {
		return false, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO dvr_recording_layouts(recording_id,owner_key,artifact_id,layout_revision,root_path,root_identity,source_relative_path,source_identity,target_relative_path,target_identity,target_base_relative_path,collision_ordinal,object_digest,byte_size,phase) VALUES(?,?,?,?,?,?,?,?,?,'',?,?,?,?,'prepared')`, r.RecordingID, r.OwnerKey, r.ArtifactKey, r.LayoutRevision, r.RootPath, r.RootIdentity, r.Source, r.SourceIdentity, r.Target, r.BaseTarget, r.Collision, r.Object.Digest, r.Object.Size)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
