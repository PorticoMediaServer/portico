package playbackdisc

import (
	"context"
	"database/sql"
	"time"
)

func validPreparation(v Preparation) bool {
	b := v.Binding
	return text(v.ID, 256, false) && text(v.Key.DeviceID, 256, false) && digest(v.Key.StartKeyDigest) &&
		validActor(b.Actor) && b.Actor.DeviceID == v.Key.DeviceID && digest(b.Actor.SessionHash) &&
		digest(b.StartBodyDigest) && text(b.ItemID, 256, false) && text(b.AssetID, 256, false) && b.PartIndex >= 0 &&
		validSource(b.Source) && b.Source.AssetID == b.AssetID && validOperation(b.Queue) &&
		b.Queue.StartKeyDigest == v.Key.StartKeyDigest && b.Queue.StartBodyDigest == b.StartBodyDigest &&
		text(b.ReplacesLegacySessionID, 256, true) && ((b.ReplacesLegacySessionID == "" && b.ReplacesGeneration == 0) || (b.ReplacesLegacySessionID != "" && b.ReplacesGeneration > 0)) &&
		v.CreatedMs > 0 && b.DeadlineMs > v.CreatedMs && b.DeadlineMs-v.CreatedMs <= MaxPreparationLifetime.Milliseconds() &&
		b.DeadlineMs <= b.Actor.OriginalHorizonMs && b.DeadlineMs <= b.OriginalTokenExpiresMs
}

func (s *Store) checkPreparationTx(ctx context.Context, tx *sql.Tx, v Preparation, check PreparationCheck) error {
	if s == nil || ctx == nil || tx == nil || check == nil || s.serverID != v.Binding.Actor.ServerID || !validPreparation(v) {
		return ErrInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.Binding.DeadlineMs <= time.Now().UnixMilli() || v.State == "failed" || v.State == "retired" || v.State == "accepted" {
		return ErrCurrent
	}
	if err := check(ctx, tx, v); err != nil {
		return err
	}
	if v.Binding.DeadlineMs <= time.Now().UnixMilli() {
		return ErrCurrent
	}
	return ctx.Err()
}

// AdmitPreparationTx reserves only an original operation, never a session or
// bytes. The caller resolves its actual selected physical source before this
// short writer and starts I/O only after commit. A duplicate key must be read
// through FindPreparationTx; it cannot adopt a newly resolved selection.
func (s *Store) AdmitPreparationTx(ctx context.Context, tx *sql.Tx, id string, key PreparationKey, binding PreparationBinding, limits PreparationLimits, check PreparationCheck) (Preparation, error) {
	if err := writeTx(ctx, tx); err != nil {
		return Preparation{}, err
	}
	if limits.Retained < 1 || limits.Retained > 65536 || limits.Active < 1 || limits.Active > 256 || limits.Active > limits.Retained || limits.PerDevice < 1 || limits.PerDevice > limits.Active {
		return Preparation{}, ErrInput
	}
	v := Preparation{ID: id, Key: key, Binding: binding, State: "pending", Claim: PreparationClaim{OperationID: id, Revision: 1}, CreatedMs: time.Now().UnixMilli()}
	if err := s.checkPreparationTx(ctx, tx, v, check); err != nil {
		return Preparation{}, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_start_preparations WHERE id=? OR (device_id=? AND start_key_digest=?))`, id, key.DeviceID, key.StartKeyDigest).Scan(&exists); err != nil {
		return Preparation{}, err
	}
	if exists {
		return Preparation{}, ErrConflict
	}
	var retained, active, device int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_start_preparations`).Scan(&retained); err != nil {
		return Preparation{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(p.device_id=?),0) FROM playback_start_preparations p JOIN playback_start_preparation_lifecycle l ON l.operation_id=p.id WHERE l.state IN('pending','preparing','prepared')`, key.DeviceID).Scan(&active, &device); err != nil {
		return Preparation{}, err
	}
	if retained >= limits.Retained || active >= limits.Active || device >= limits.PerDevice {
		return Preparation{}, ErrCapacity
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO playback_start_preparations(id,device_id,start_key_digest,start_body_digest,item_id,asset_id,part_index,replaces_session_id,replaces_generation,token_expires_ms,created_ms,deadline_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, key.DeviceID, key.StartKeyDigest, binding.StartBodyDigest, binding.ItemID, binding.AssetID, binding.PartIndex, binding.ReplacesLegacySessionID, binding.ReplacesGeneration, binding.OriginalTokenExpiresMs, v.CreatedMs, binding.DeadlineMs)
	if err != nil {
		return Preparation{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO playback_start_preparation_lifecycle(operation_id,state,revision,execution_id,sealed) VALUES(?,'pending',1,'',0)`, id); err != nil {
		return Preparation{}, err
	}
	if err = writePreparationBinding(ctx, tx, id, binding); err != nil {
		return Preparation{}, err
	}
	if err = s.checkPreparationTx(ctx, tx, v, check); err != nil {
		return Preparation{}, err
	}
	if err = affectedOne(tx.ExecContext(ctx, `UPDATE playback_start_preparation_lifecycle SET sealed=1 WHERE operation_id=? AND state='pending' AND revision=1 AND sealed=0`, id)); err != nil {
		return Preparation{}, err
	}
	return v, nil
}

// FindPreparationTx requires the exact original credential identity and body.
// A returned pending/prepared row still needs its original process proof. The
// accepted-session path is separate because preparation expiry never renews it.
func (s *Store) FindPreparationTx(ctx context.Context, tx *sql.Tx, key PreparationKey, actor Actor, bodyDigest string, check PreparationCheck) (Preparation, error) {
	if s == nil || ctx == nil || tx == nil || check == nil || !text(key.DeviceID, 256, false) || !digest(key.StartKeyDigest) || !validActor(actor) || !digest(bodyDigest) {
		return Preparation{}, ErrInput
	}
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM playback_start_preparations WHERE device_id=? AND start_key_digest=?`, key.DeviceID, key.StartKeyDigest).Scan(&id); err != nil {
		return Preparation{}, err
	}
	v, err := readPreparation(ctx, tx, id)
	if err != nil {
		return Preparation{}, err
	}
	if v.Binding.Actor != actor {
		return Preparation{}, ErrCurrent
	}
	if err = s.checkPreparationTx(ctx, tx, v, check); err != nil {
		return Preparation{}, err
	}
	if v.Binding.StartBodyDigest != bodyDigest {
		return Preparation{}, ErrConflict
	}
	return v, nil
}

func readPreparation(ctx context.Context, tx *sql.Tx, id string) (v Preparation, err error) {
	v.ID, v.Claim.OperationID = id, id
	b := &v.Binding
	err = tx.QueryRowContext(ctx, `SELECT p.device_id,p.start_key_digest,p.start_body_digest,p.item_id,p.asset_id,p.part_index,p.replaces_session_id,p.replaces_generation,p.token_expires_ms,p.created_ms,p.deadline_ms,l.state,l.revision,l.execution_id FROM playback_start_preparations p JOIN playback_start_preparation_lifecycle l ON l.operation_id=p.id WHERE p.id=? AND l.sealed=1`, id).Scan(&v.Key.DeviceID, &v.Key.StartKeyDigest, &b.StartBodyDigest, &b.ItemID, &b.AssetID, &b.PartIndex, &b.ReplacesLegacySessionID, &b.ReplacesGeneration, &b.OriginalTokenExpiresMs, &v.CreatedMs, &b.DeadlineMs, &v.State, &v.Claim.Revision, &v.Claim.ExecutionID)
	if err != nil {
		return
	}
	a := &b.Actor
	err = tx.QueryRowContext(ctx, `SELECT server_id,authority,account_id,profile_id,device_id,family_id,grant_id,session_hash,epoch,token_generation,original_horizon_ms FROM playback_start_preparation_actor WHERE operation_id=?`, id).Scan(&a.ServerID, &a.Authority, &a.AccountID, &a.ProfileID, &a.DeviceID, &a.FamilyID, &a.Grant, &a.SessionHash, &a.Epoch, &a.TokenGeneration, &a.OriginalHorizonMs)
	if err != nil {
		return
	}
	source := &b.Source
	err = tx.QueryRowContext(ctx, `SELECT library_id,library_source_id,asset_id,inventory_object_id,inventory_revision,root_identity,incarnation,remote_source_id,effective_configuration,read_policy_witness,read_authority_witness,library_generation,raw_configuration_generation,profile_revision,size,modified_ns FROM playback_start_preparation_source WHERE operation_id=?`, id).Scan(&source.LibraryID, &source.LibrarySourceID, &source.AssetID, &source.InventoryObjectID, &source.InventoryRevision, &source.RootIdentity, &source.Incarnation, &source.RemoteSourceID, &source.EffectiveConfiguration, &source.ReadPolicyWitness, &source.ReadAuthorityWitness, &source.LibraryGeneration, &source.RawConfigurationGeneration, &source.ProfileRevision, &source.Size, &source.ModifiedNS)
	if err != nil {
		return
	}
	q := &b.Queue
	err = tx.QueryRowContext(ctx, `SELECT legacy_request_id,start_key_digest,start_body_digest,queue_id,queue_entry_id,queue_owner_kind,queue_owner_id,queue_revision,queue_custody_generation FROM playback_start_preparation_queue WHERE operation_id=?`, id).Scan(&q.LegacyRequestID, &q.StartKeyDigest, &q.StartBodyDigest, &q.QueueID, &q.QueueEntryID, &q.QueueOwnerKind, &q.QueueOwnerID, &q.QueueRevision, &q.QueueCustodyGeneration)
	if err != nil {
		return
	}
	if v.State == "accepted" {
		err = tx.QueryRowContext(ctx, `SELECT session_id,legacy_session_id,legacy_generation,snapshot_id FROM playback_start_preparation_results WHERE operation_id=?`, id).Scan(&v.Accepted.SessionID, &v.Accepted.Legacy.SessionID, &v.Accepted.Legacy.Generation, &v.Accepted.SnapshotID)
	}
	return
}

func writePreparationBinding(ctx context.Context, tx *sql.Tx, id string, b PreparationBinding) error {
	a := b.Actor
	if _, err := tx.ExecContext(ctx, `INSERT INTO playback_start_preparation_actor(operation_id,server_id,authority,account_id,profile_id,device_id,family_id,grant_id,session_hash,epoch,token_generation,original_horizon_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, a.ServerID, a.Authority, a.AccountID, a.ProfileID, a.DeviceID, a.FamilyID, a.Grant, a.SessionHash, a.Epoch, a.TokenGeneration, a.OriginalHorizonMs); err != nil {
		return err
	}
	s := b.Source
	if _, err := tx.ExecContext(ctx, `INSERT INTO playback_start_preparation_source(operation_id,library_id,library_source_id,asset_id,inventory_object_id,inventory_revision,root_identity,incarnation,remote_source_id,effective_configuration,read_policy_witness,read_authority_witness,library_generation,raw_configuration_generation,profile_revision,size,modified_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, s.LibraryID, s.LibrarySourceID, s.AssetID, s.InventoryObjectID, s.InventoryRevision, s.RootIdentity, s.Incarnation, s.RemoteSourceID, s.EffectiveConfiguration, s.ReadPolicyWitness, s.ReadAuthorityWitness, s.LibraryGeneration, s.RawConfigurationGeneration, s.ProfileRevision, s.Size, s.ModifiedNS); err != nil {
		return err
	}
	q := b.Queue
	_, err := tx.ExecContext(ctx, `INSERT INTO playback_start_preparation_queue(operation_id,legacy_request_id,start_key_digest,start_body_digest,queue_id,queue_entry_id,queue_owner_kind,queue_owner_id,queue_revision,queue_custody_generation) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, q.LegacyRequestID, q.StartKeyDigest, q.StartBodyDigest, q.QueueID, q.QueueEntryID, q.QueueOwnerKind, q.QueueOwnerID, q.QueueRevision, q.QueueCustodyGeneration)
	return err
}

func affectedOne(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrCurrent
	}
	return nil
}
