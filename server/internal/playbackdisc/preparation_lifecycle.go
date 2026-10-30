package playbackdisc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"
)

func nextPreparationRevision(v Preparation) (int64, error) {
	if v.Claim.Revision < 1 || v.Claim.Revision == 1<<63-1 {
		return 0, ErrCurrent
	}
	return v.Claim.Revision + 1, nil
}

// ClaimPreparationTx issues exactly one process execution, after the pending
// operation itself committed. Start the worker only after THIS writer commits.
// A restarted process retires an old claim; it never adopts or reissues it.
func (s *Store) ClaimPreparationTx(ctx context.Context, tx *sql.Tx, pending PreparationClaim, executionID string, check PreparationCheck) (PreparationClaim, error) {
	if err := writeTx(ctx, tx); err != nil {
		return PreparationClaim{}, err
	}
	if !text(executionID, 256, false) || pending.ExecutionID != "" {
		return PreparationClaim{}, ErrInput
	}
	v, err := readPreparation(ctx, tx, pending.OperationID)
	if err != nil {
		return PreparationClaim{}, err
	}
	if v.State != "pending" || v.Claim != pending {
		return PreparationClaim{}, ErrCurrent
	}
	if err = s.checkPreparationTx(ctx, tx, v, check); err != nil {
		return PreparationClaim{}, err
	}
	revision, err := nextPreparationRevision(v)
	if err != nil {
		return PreparationClaim{}, err
	}
	if err = affectedOne(tx.ExecContext(ctx, `UPDATE playback_start_preparation_lifecycle SET state='preparing',revision=?,execution_id=? WHERE operation_id=? AND state='pending' AND revision=? AND execution_id='' AND sealed=1`, revision, executionID, v.ID, pending.Revision)); err != nil {
		return PreparationClaim{}, err
	}
	return PreparationClaim{OperationID: v.ID, ExecutionID: executionID, Revision: revision}, nil
}

// CheckPreparationClaimTx is the retained worker's read/measurement check.
// It accepts only that original live execution, never a pending row or another
// revision. It grants no session, bytes, new worker, or accepting publication.
func (s *Store) CheckPreparationClaimTx(ctx context.Context, tx *sql.Tx, claim PreparationClaim, check PreparationCheck) (Preparation, error) {
	if s == nil || ctx == nil || tx == nil || check == nil || !text(claim.OperationID, 256, false) || !text(claim.ExecutionID, 256, false) || claim.Revision < 1 {
		return Preparation{}, ErrInput
	}
	v, err := readPreparation(ctx, tx, claim.OperationID)
	if err != nil {
		return Preparation{}, err
	}
	if v.Claim != claim || v.State != "preparing" && v.State != "prepared" {
		return Preparation{}, ErrCurrent
	}
	if err = s.checkPreparationTx(ctx, tx, v, check); err != nil {
		return Preparation{}, err
	}
	return v, nil
}

// MarkPreparationPreparedTx records readiness of the existing private value.
// It stores no facts, session, grant or queue mutation. Keep the returned claim
// paired with that same value only after commit; the old revision is retired.
func (s *Store) MarkPreparationPreparedTx(ctx context.Context, tx *sql.Tx, claim PreparationClaim, check PreparationCheck) (PreparationClaim, error) {
	if err := writeTx(ctx, tx); err != nil {
		return PreparationClaim{}, err
	}
	v, err := s.CheckPreparationClaimTx(ctx, tx, claim, check)
	if err != nil {
		return PreparationClaim{}, err
	}
	if v.State != "preparing" {
		return PreparationClaim{}, ErrCurrent
	}
	revision, err := nextPreparationRevision(v)
	if err != nil {
		return PreparationClaim{}, err
	}
	if err = affectedOne(tx.ExecContext(ctx, `UPDATE playback_start_preparation_lifecycle SET state='prepared',revision=? WHERE operation_id=? AND state='preparing' AND revision=? AND execution_id=? AND sealed=1`, revision, v.ID, claim.Revision, claim.ExecutionID)); err != nil {
		return PreparationClaim{}, err
	}
	return PreparationClaim{OperationID: v.ID, ExecutionID: claim.ExecutionID, Revision: revision}, nil
}

// CheckPreparedClaimTx reproves the original ready claim inside a gated writer.
// It is not current-request admission: legacy/final publication must also pass
// CheckPreparedContinuationTx. Keep the exact sealed borrow through commit.
func (s *Store) CheckPreparedClaimTx(ctx context.Context, tx *sql.Tx, claim PreparationClaim, check PreparationCheck) (Preparation, error) {
	if err := writeTx(ctx, tx); err != nil {
		return Preparation{}, err
	}
	v, err := s.CheckPreparationClaimTx(ctx, tx, claim, check)
	if err != nil {
		return Preparation{}, err
	}
	if v.State != "prepared" {
		return Preparation{}, ErrCurrent
	}
	return v, nil
}

// CheckPreparedContinuationTx is required before the original continuation's
// legacy insert/binding and again before final activation/acceptance. The live
// continuation proof is separate from a worker's retained measurement proof.
func (s *Store) CheckPreparedContinuationTx(ctx context.Context, tx *sql.Tx, claim PreparationClaim, check PreparationCheck, continuation PreparationContinuationCheck) (Preparation, error) {
	if continuation == nil {
		return Preparation{}, ErrInput
	}
	v, err := s.CheckPreparedClaimTx(ctx, tx, claim, check)
	if err != nil {
		return Preparation{}, err
	}
	if err = continuation(ctx, tx, v); err != nil {
		return Preparation{}, err
	}
	if err = ctx.Err(); err != nil {
		return Preparation{}, err
	}
	if v.Binding.DeadlineMs <= time.Now().UnixMilli() {
		return Preparation{}, ErrCurrent
	}
	return v, nil
}

func (s *Store) acceptedPreparationResultTx(ctx context.Context, tx *sql.Tx, v Preparation, out PreparationAccepted) error {
	if !text(out.SessionID, 256, false) || !text(out.SnapshotID, 256, false) || !validKey(out.Legacy) {
		return ErrInput
	}
	var legacy Key
	var current bool
	if err := tx.QueryRowContext(ctx, `SELECT p.session_id,p.generation,l.state='active' AND l.sealed=1 AND l.expires_ms>? FROM playback_disc_snapshots p JOIN playback_disc_lifecycle l ON l.snapshot_id=p.id JOIN playback_sessions media ON media.id=p.session_id WHERE p.id=? AND media.generation=p.generation AND media.asset_id=? AND media.account_id=? AND media.profile_id=? AND media.request_id=?`, time.Now().UnixMilli(), out.SnapshotID, v.Binding.AssetID, v.Binding.Actor.AccountID, v.Binding.Actor.ProfileID, v.Binding.Queue.LegacyRequestID).Scan(&legacy.SessionID, &legacy.Generation, &current); err != nil {
		return err
	}
	if !current || legacy != out.Legacy {
		return ErrCurrent
	}
	a, err := readActor(ctx, tx, out.SnapshotID)
	if err != nil {
		return err
	}
	physical, err := readSource(ctx, tx, out.SnapshotID)
	if err != nil {
		return err
	}
	queue, err := readOperation(ctx, tx, out.SnapshotID)
	if err != nil {
		return err
	}
	if a != v.Binding.Actor || physical != v.Binding.Source || queue != v.Binding.Queue {
		return ErrCurrent
	}
	var rawKey, body, device, account, profile, authority, item, version, queueID, entryID string
	var actual Key
	var part int
	if err := tx.QueryRowContext(ctx, `SELECT p.start_key,p.start_digest,p.device_id,p.account_id,p.profile_id,p.authority,p.media_session_id,p.media_generation,p.part_index,pid(i.public_id),p.version_id,p.queue_id,p.entry_id FROM playback_v1_sessions p JOIN catalog_entities i ON i.id=p.item_id WHERE p.id=? AND p.ended_ms=0 AND p.lease_expires_ms>?`, out.SessionID, time.Now().UnixMilli()).Scan(&rawKey, &body, &device, &account, &profile, &authority, &actual.SessionID, &actual.Generation, &part, &item, &version, &queueID, &entryID); err != nil {
		return err
	}
	keyHash := sha256.Sum256([]byte(rawKey))
	if hex.EncodeToString(keyHash[:]) != v.Key.StartKeyDigest || body != v.Binding.StartBodyDigest || device != v.Key.DeviceID || account != a.AccountID || profile != a.ProfileID || authority != a.Authority || actual != out.Legacy || part != v.Binding.PartIndex || item != v.Binding.ItemID || part == 0 && version != v.Binding.AssetID || queueID != queue.QueueID || entryID != queue.QueueEntryID {
		return ErrCurrent
	}
	return nil
}

// AcceptPreparationTx is the FINAL original continuation writer, after ordinary
// actor/source/content/queue/custody/idempotency/capacity proof, real legacy/v1
// inserts and exact snapshot activation. No background worker may call it.
// The process borrow/custody transfers only on outer commit, never this return.
func (s *Store) AcceptPreparationTx(ctx context.Context, tx *sql.Tx, claim PreparationClaim, out PreparationAccepted, check PreparationCheck, continuation PreparationContinuationCheck) error {
	v, err := s.CheckPreparedContinuationTx(ctx, tx, claim, check, continuation)
	if err != nil {
		return err
	}
	if err = s.acceptedPreparationResultTx(ctx, tx, v, out); err != nil {
		return err
	}
	revision, err := nextPreparationRevision(v)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO playback_start_preparation_results(operation_id,session_id,legacy_session_id,legacy_generation,snapshot_id) VALUES(?,?,?,?,?)`, v.ID, out.SessionID, out.Legacy.SessionID, out.Legacy.Generation, out.SnapshotID); err != nil {
		return err
	}
	// Repeat the original live proof immediately before consuming this claim.
	if _, err = s.CheckPreparedContinuationTx(ctx, tx, claim, check, continuation); err != nil {
		return err
	}
	return affectedOne(tx.ExecContext(ctx, `UPDATE playback_start_preparation_lifecycle SET state='accepted',revision=? WHERE operation_id=? AND state='prepared' AND revision=? AND execution_id=? AND sealed=1`, revision, v.ID, claim.Revision, claim.ExecutionID))
}

// ResolveAcceptedPreparationTx resolves only that original committed outcome.
// The normal playing-session proof is mandatory; a preparation deadline or
// stored actor never grants later playback or renews a retired preparation.
func (s *Store) ResolveAcceptedPreparationTx(ctx context.Context, tx *sql.Tx, id string, expected PreparationAccepted, check AcceptedPreparationCheck) (Preparation, error) {
	if s == nil || ctx == nil || tx == nil || check == nil || !text(id, 256, false) {
		return Preparation{}, ErrInput
	}
	v, err := readPreparation(ctx, tx, id)
	if err != nil {
		return Preparation{}, err
	}
	if !validPreparation(v) || v.Binding.Actor.ServerID != s.serverID || v.State != "accepted" || v.Accepted != expected {
		return Preparation{}, ErrCurrent
	}
	if err = s.acceptedPreparationResultTx(ctx, tx, v, expected); err != nil {
		return Preparation{}, err
	}
	if err = check(ctx, tx, v, expected); err != nil {
		return Preparation{}, err
	}
	return v, ctx.Err()
}

// RetirePreparationTx releases only this exact unaccepted execution claim.
// Media still cancels/joins its own worker and closes its original accounted
// input/pins. Terminal rows remain idempotency tombstones and are not revived.
func (s *Store) RetirePreparationTx(ctx context.Context, tx *sql.Tx, claim PreparationClaim, terminal PreparationTerminal) error {
	if err := writeTx(ctx, tx); err != nil {
		return err
	}
	if s == nil || !text(claim.OperationID, 256, false) || !text(claim.ExecutionID, 256, true) || claim.Revision < 1 || terminal != PreparationFailed && terminal != PreparationRetired {
		return ErrInput
	}
	v, err := readPreparation(ctx, tx, claim.OperationID)
	if err != nil {
		return err
	}
	if v.Binding.Actor.ServerID != s.serverID || v.Claim != claim || v.State != "pending" && v.State != "preparing" && v.State != "prepared" {
		return ErrCurrent
	}
	revision, err := nextPreparationRevision(v)
	if err != nil {
		return err
	}
	return affectedOne(tx.ExecContext(ctx, `UPDATE playback_start_preparation_lifecycle SET state=?,revision=? WHERE operation_id=? AND revision=? AND execution_id=? AND state IN('pending','preparing','prepared')`, string(terminal), revision, v.ID, claim.Revision, claim.ExecutionID))
}

// UnacceptedPreparationClaimsTx is bounded restart/cleanup enumeration only.
// Returned claims can be retired, never adopted as reconstructed worker proof.
// It starts no timer and holds no provider/decoder work under a SQL snapshot.
func (s *Store) UnacceptedPreparationClaimsTx(ctx context.Context, tx *sql.Tx, after string, limit int) ([]PreparationClaim, string, error) {
	if s == nil || s.serverID == "" || ctx == nil || tx == nil || !text(after, 256, true) || limit < 1 || limit > 64 {
		return nil, "", ErrInput
	}
	rows, err := tx.QueryContext(ctx, `SELECT l.operation_id,l.execution_id,l.revision FROM playback_start_preparation_lifecycle l JOIN playback_start_preparation_actor a ON a.operation_id=l.operation_id WHERE a.server_id=? AND l.operation_id>? AND l.sealed=1 AND l.state IN('pending','preparing','prepared') ORDER BY l.operation_id LIMIT ?`, s.serverID, after, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	claims := make([]PreparationClaim, 0, limit)
	for rows.Next() {
		var claim PreparationClaim
		if err = rows.Scan(&claim.OperationID, &claim.ExecutionID, &claim.Revision); err != nil {
			return nil, "", err
		}
		claims = append(claims, claim)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(claims) == limit {
		next = claims[len(claims)-1].OperationID
	}
	return claims, next, nil
}
