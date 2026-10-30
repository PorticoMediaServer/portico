package dvr

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// Zero retention days/episode limit means indefinite, which is more protective
// than any finite policy. Keep exempts every automatic decision, not manual delete.
func protective(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return max(a, b)
}

type StoragePolicy struct {
	Revision      int64 `json:"revision"`
	RetentionDays int   `json:"retentionDays"`
	EpisodeLimit  int   `json:"episodeLimit"`
	FloorBytes    int64 `json:"floorBytes"`
	CapBytes      int64 `json:"capBytes"`
}
type DeletePreview struct {
	RecordingID   string `json:"recordingId"`
	Revision      int64  `json:"revision"`
	Bytes         int64  `json:"bytes"`
	Keep          bool   `json:"keep"`
	ActiveReaders bool   `json:"activeReaders"`
	Result        string `json:"result"`
}

func readersTx(ctx context.Context, tx *sql.Tx, item string, now time.Time) (bool, error) {
	if item == "" {
		return false, nil
	}
	var busy bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND state NOT IN('stopped','ended','failed') AND expires_at>?)`, item, now.UTC().Format(time.RFC3339)).Scan(&busy)
	return busy, e
}
func (s *Store) PreviewDelete(ctx context.Context, a livechannels.Authority, o livechannels.Owner, id string) (DeletePreview, error) {
	var out DeletePreview
	e := s.snapshot(ctx, a, o, func(tx *sql.Tx, _ func(string, string) bool) error {
		r, e := loadTx(ctx, tx, o, id)
		if e != nil {
			return e
		}
		busy, e := readersTx(ctx, tx, r.ItemID, s.now())
		if e != nil {
			return e
		}
		message := "Delete this recording permanently. New playback will be unavailable immediately; existing readers finish before bytes are removed."
		if capturing(r.State) {
			message = "Stop this recording and delete its captured bytes after the capture and all readers retire."
		}
		out = DeletePreview{id, r.Revision, r.Bytes, r.Keep, busy || capturing(r.State), message}
		return nil
	})
	return out, e
}
func (s *Store) Delete(ctx context.Context, a livechannels.Authority, o livechannels.Owner, id string, in Mutation) (Recording, error) {
	return s.mutate(ctx, a, o, id, "delete", in, in, func(ctx context.Context, tx *sql.Tx, r Recording) (Recording, error) {
		if r.State == "deleted" || r.State == "pending-delete" {
			return r, nil
		}
		if e := s.pendingDeleteTx(ctx, tx, o, r, "owner-delete"); e != nil {
			return r, e
		}
		return loadTx(ctx, tx, o, id)
	})
}
func (s *Store) pendingDeleteTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, r Recording, reason string) error {
	if tx == nil || !o.Valid() {
		return ErrDenied
	}
	if s.driver == nil || !s.driver.RetirementAvailable() {
		return ErrDeletionUnavailable
	}
	_, e := tx.ExecContext(ctx, `UPDATE dvr_recordings SET state='pending-delete',reason=?,cancel_generation=cancel_generation+1,revision=revision+1,updated_ms=? WHERE id=?`, reason, s.now().UnixMilli(), r.ID)
	if e != nil {
		return e
	}
	// Immediately remove source availability and fence future ordinary playback.
	// Existing readers retain their actual descriptors until physical retirement.
	if r.ItemID != "" {
		entity, e := entityid.Resolve(ctx, tx, r.ItemID)
		if e != nil && !errors.Is(e, entityid.ErrNotFound) {
			return e
		}
		if e == nil {
			if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO dvr_catalog_retirements VALUES(?,?)`, entity, s.now().UnixMilli()); e != nil {
				return e
			}
			// Recording assets are occurrence-private. Fence asset-level source
			// admission too; existing physical descriptors are not forcibly released.
			var token string
			if scan := tx.QueryRowContext(ctx, `SELECT asset_id FROM dvr_catalog_provenance WHERE item_id=?`, entity).Scan(&token); scan != nil && !errors.Is(scan, sql.ErrNoRows) {
				return scan
			}
			if token != "" {
				asset, cause := compactcatalog.AssetByTokenTx(ctx, tx, token)
				if cause != nil {
					return cause
				}
				if asset != 0 {
					if e = compactcatalog.SetAssetAvailableTx(ctx, tx, asset, false); e != nil {
						return e
					}
				}
			}
		}
	}
	if e = unreserveTx(ctx, tx, r.ID); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO dvr_delete_work VALUES(?,?,?,?, 'waiting',0,'') ON CONFLICT(recording_id) DO NOTHING`, r.ID, r.Revision+1, s.now().UnixMilli(), s.now().Add(2*time.Minute).UnixMilli())
	return e
}

// retentionCandidates evaluates the whole eligible collection in SQL and only
// materializes a bounded batch. Keep/indefinite policies never become emergency
// disk victims. Deferred unknown authority cannot starve later eligible owners.
const retentionCandidates = `WITH protected AS (
 SELECT r.*, CASE WHEN ?=0 OR json_extract(options_json,'$.retentionDays')=0 THEN 0 ELSE max(?,json_extract(options_json,'$.retentionDays')) END AS days,
 CASE WHEN ?=0 OR json_extract(options_json,'$.episodeLimit')=0 THEN 0 ELSE max(?,json_extract(options_json,'$.episodeLimit')) END AS episodes,
 ROW_NUMBER() OVER(PARTITION BY owner_key,source_id,json_extract(programme_json,'$.seriesId') ORDER BY finished_ms DESC,id DESC) AS episode_rank
 FROM dvr_recordings r WHERE r.state IN('completed','incomplete-playable') AND r.keep=0
), eligible AS (
 SELECT r.*,CASE
 WHEN days>0 AND finished_ms<?-days*86400000 AND json_extract(options_json,'$.retentionDays')>=? THEN 0
 WHEN episodes>0 AND COALESCE(json_extract(programme_json,'$.seriesId'),'')!='' AND episode_rank>episodes THEN 1
 WHEN EXISTS(SELECT 1 FROM progress_activity a WHERE a.profile_id=r.profile_id AND a.item_id=r.item_id AND a.state='ended') THEN 2 ELSE 3 END AS deletion_order
 FROM protected r WHERE (days>0 AND finished_ms<?-days*86400000 OR episodes>0 AND COALESCE(json_extract(programme_json,'$.seriesId'),'')!='' AND episode_rank>episodes)
 AND NOT EXISTS(SELECT 1 FROM dvr_retention_retry retry WHERE retry.recording_id=r.id AND retry.not_before_ms>?)
) SELECT id,authority,account_id,profile_id FROM eligible ORDER BY deletion_order,finished_ms,id LIMIT 32`

func (s *Store) RetentionOne(ctx context.Context) error {
	if s.driver == nil || !s.driver.RetirementAvailable() {
		return nil
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var days, limit int
	if e = tx.QueryRowContext(ctx, `SELECT retention_days,episode_limit FROM dvr_storage_policy WHERE singleton=1`).Scan(&days, &limit); e != nil {
		return e
	}
	if days == 0 && limit == 0 {
		return nil
	}
	now := s.now().UnixMilli()
	rows, e := tx.QueryContext(ctx, retentionCandidates, days, days, limit, limit, now, days, now, now)
	if e != nil {
		return e
	}
	type candidate struct {
		id    string
		owner livechannels.Owner
	}
	entries := []candidate{}
	for rows.Next() {
		var v candidate
		if e = rows.Scan(&v.id, &v.owner.Authority, &v.owner.AccountID, &v.owner.ProfileID); e != nil {
			rows.Close()
			return e
		}
		entries = append(entries, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range entries {
		r, e := loadTx(ctx, tx, v.owner, v.id)
		if e != nil {
			return e
		}
		if !v.owner.Valid() || s.durable(ctx, tx, v.owner, r.Occurrence.SourceID, r.Occurrence.ChannelID) != nil {
			if _, e = tx.ExecContext(ctx, `INSERT INTO dvr_retention_retry VALUES(?,?) ON CONFLICT(recording_id) DO UPDATE SET not_before_ms=excluded.not_before_ms`, v.id, s.now().Add(5*time.Minute).UnixMilli()); e != nil {
				return e
			}
			continue
		}
		if e = s.pendingDeleteTx(ctx, tx, v.owner, r, "retention-policy"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM dvr_retention_retry WHERE recording_id=?`, v.id); e != nil {
			return e
		}
		if e = touchTx(ctx, tx, v.owner); e != nil {
			return e
		}
	}
	return gated.Commit()
}

// DeleteOne never accepts a filesystem path from the API or database. The driver
// derives a private owner root and accepts only a validated artifact key/digest.
// Metadata is retained as a deletion tombstone to prevent duplicate publication.
func (s *Store) DeleteOne(ctx context.Context) error {
	if s.driver == nil {
		return nil
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	var id, key, digest, allocation string
	var bytes, revision int64
	var o livechannels.Owner
	e = tx.QueryRowContext(ctx, `SELECT r.id,r.authority,r.account_id,r.profile_id,r.artifact_id,r.artifact_digest,r.bytes,r.revision,r.allocation_id FROM dvr_delete_work w JOIN dvr_recordings r ON r.id=w.recording_id WHERE r.state='pending-delete' AND w.not_before_ms<=? ORDER BY w.not_before_ms,r.id LIMIT 1`, s.now().UnixMilli()).Scan(&id, &o.Authority, &o.AccountID, &o.ProfileID, &key, &digest, &bytes, &revision, &allocation)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	r, e := loadTx(ctx, tx, o, id)
	if e != nil {
		return e
	}
	busy, e := readersTx(ctx, tx, r.ItemID, s.now())
	if e != nil {
		return e
	}
	if allocation != "" {
		var active bool
		e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM live_allocations WHERE id=? AND state!='released')`, allocation).Scan(&active)
		if e != nil {
			return e
		}
		busy = busy || active
	}
	s.mu.Lock()
	_, capturingHere := s.active[id]
	s.mu.Unlock()
	sharedPending, err := s.pendingSharedTx(ctx, tx, id)
	if err != nil {
		return err
	}
	busy = busy || capturingHere || sharedPending
	if busy {
		_, e = tx.ExecContext(ctx, `UPDATE dvr_delete_work SET reason='active-reader-grace',not_before_ms=? WHERE recording_id=?`, s.now().Add(2*time.Minute).UnixMilli(), id)
		if e != nil {
			return e
		}
		return gated2.Commit()
	}
	_, e = tx.ExecContext(ctx, `UPDATE dvr_delete_work SET phase='deleting',not_before_ms=?,attempt=attempt+1 WHERE recording_id=?`, s.now().Add(time.Minute).UnixMilli(), id)
	if e != nil {
		return e
	}
	if e = gated2.Commit(); e != nil {
		return e
	}
	// IO is outside SQLite. A pending-delete item cannot acquire a new grant.
	e = s.retireRecordingLayout(ctx, id, o, key, mediaartifact.Object{Digest: digest, Size: bytes})
	if e == nil {
		e = s.driver.Remove(ctx, o, key, mediaartifact.Object{Digest: digest, Size: bytes})
	}
	if e != nil {
		// A delayed/killed helper, transferred descriptor or inherited decoder
		// lock is physical evidence. Retry only after another full grace window.
		retry, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		reason := "artifact-remove-unavailable"
		if errors.Is(e, mediaartifact.ErrLeased) {
			reason = "physical-reader-active"
		}
		_, _ = dbwork.ExecWrite(retry, s.db, dbwork.ClassFrom(retry, dbwork.ClassInteractive), `UPDATE dvr_delete_work SET phase='waiting',reason=?,not_before_ms=? WHERE recording_id=?`, reason, s.now().Add(2*time.Minute).UnixMilli(), id)
		return e
	}
	var gated3 *dbwork.Write
	gated3, e = dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx = gated3.Tx()
	defer gated3.Rollback()
	var current int64
	var state string
	e = tx.QueryRowContext(ctx, `SELECT revision,state FROM dvr_recordings WHERE id=?`, id).Scan(&current, &state)
	if e != nil {
		return e
	}
	if state != "pending-delete" || current != revision {
		return ErrConflict
	}
	// Recording catalogue facts and the occurrence-private asset are removed
	// through the compact catalogue API after the physical bytes are gone.
	if r.ItemID != "" {
		entity, e := entityid.Resolve(ctx, tx, r.ItemID)
		if e != nil && !errors.Is(e, entityid.ErrNotFound) {
			return e
		}
		if e == nil {
			var token string
			scan := tx.QueryRowContext(ctx, `SELECT asset_id FROM dvr_catalog_provenance WHERE item_id=?`, entity).Scan(&token)
			if scan != nil && !errors.Is(scan, sql.ErrNoRows) {
				return scan
			}
			asset := int64(0)
			if token != "" {
				asset, e = compactcatalog.AssetByTokenTx(ctx, tx, token)
				if e != nil {
					return e
				}
			}
			if e = compactcatalog.DeleteEntityTx(ctx, tx, entity); e != nil {
				return e
			}
			if asset != 0 {
				if e = compactcatalog.DeleteAssetTx(ctx, tx, asset); e != nil {
					return e
				}
			}
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET state='deleted',reason='',bytes=0,revision=revision+1,updated_ms=? WHERE id=?`, s.now().UnixMilli(), id)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM dvr_delete_work WHERE recording_id=?`, id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM live_source_dependencies WHERE kind='recording' AND id=?`, id); e != nil {
		return e
	}
	if e = touchTx(ctx, tx, o); e != nil {
		return e
	}
	return gated3.Commit()
}
func (s *Store) GetStoragePolicy(ctx context.Context, a livechannels.Authority) (StoragePolicy, error) {
	var p StoragePolicy
	gated4, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return p, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	if a == nil {
		return p, ErrDenied
	}
	f, _, e := a(ctx, tx, true)
	if e != nil || f == "" {
		return p, ErrDenied
	}
	e = tx.QueryRowContext(ctx, `SELECT revision,retention_days,episode_limit,floor_bytes,cap_bytes FROM dvr_storage_policy WHERE singleton=1`).Scan(&p.Revision, &p.RetentionDays, &p.EpisodeLimit, &p.FloorBytes, &p.CapBytes)
	return p, e
}

type StoragePolicyInput struct {
	Mutation
	Policy StoragePolicy `json:"policy"`
}

func (s *Store) SetStoragePolicy(ctx context.Context, a livechannels.Authority, o livechannels.Owner, in StoragePolicyInput) (StoragePolicy, error) {
	var out StoragePolicy
	p := in.Policy
	if !hexID.MatchString(in.RequestID) || in.ExpectedRevision < 1 || p.RetentionDays < 0 || p.RetentionDays > 3650 || p.EpisodeLimit < 0 || p.EpisodeLimit > 100000 || p.FloorBytes < 0 || p.CapBytes < 0 || p.FloorBytes > 1<<53-1 || p.CapBytes > 1<<53-1 {
		return out, ErrInvalid
	}
	if s == nil || s.db == nil {
		return out, ErrUnavailable
	}
	if (p.RetentionDays != 0 || p.EpisodeLimit != 0 || p.FloorBytes != 0 || p.CapBytes != 0) && (s.storage == nil || s.driver == nil || !s.driver.RetirementAvailable()) {
		return out, ErrStoragePolicyUnavailable
	}
	e := s.transaction(ctx, a, o, func(tx *sql.Tx, _ func(string, string) bool) error {
		f, _, e := a(ctx, tx, true)
		if e != nil || f == "" {
			return ErrDenied
		}
		replay, e := receiptTx(ctx, tx, o, in.RequestID, in, &out)
		if e != nil || replay {
			return e
		}
		res, e := tx.ExecContext(ctx, `UPDATE dvr_storage_policy SET revision=revision+1,retention_days=?,episode_limit=?,floor_bytes=?,cap_bytes=? WHERE singleton=1 AND revision=?`, p.RetentionDays, p.EpisodeLimit, p.FloorBytes, p.CapBytes, in.ExpectedRevision)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		out = p
		out.Revision = in.ExpectedRevision + 1
		return saveReceiptTx(ctx, tx, o, in.RequestID, in, out, s.now())
	})
	if e == nil {
		s.cacheStoragePolicy(out)
	}
	return out, e
}
