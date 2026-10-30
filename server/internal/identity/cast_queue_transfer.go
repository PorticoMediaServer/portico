package identity

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CastQueueSource is the sender's captured, own-device occurrence, not a request
// to discover or copy another device's queue. Only bootstrap accepts these facts.
type CastQueueSource struct {
	SessionID     string `json:"sessionId"`
	Generation    int64  `json:"generation"`
	QueueID       string `json:"queueId"`
	EntryID       string `json:"entryId"`
	QueueRevision string `json:"queueRevision"`
}

// CastQueueTransferStatus is returned only to the intent's original live family.
// Committed is an authoritative receipt even when the receiver ACK was lost.
type CastQueueTransferStatus struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	ItemID        string `json:"itemId,omitempty"`
	PlaybackState string `json:"playbackState,omitempty"`
}

var castTransferID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type castQueueTransfer struct {
	id, authority, account, profile, sourceFamily, sourceDevice, sourceSession, queue, entry string
	generation, revision, position                                                           int64
	targetCast, targetFamily, targetDevice, state, targetSession, key, digest                string
	targetGeneration, targetCustody, expires                                                 int64
}

func readCastQueueTransfer(ctx context.Context, tx *sql.Tx, id string) (castQueueTransfer, error) {
	var r castQueueTransfer
	err := tx.QueryRowContext(ctx, `SELECT id,authority,account_id,profile_id,source_family_id,source_device_id,source_session_id,source_generation,queue_id,entry_id,queue_revision,source_position,target_cast_id,target_family_id,target_device_id,target_generation,target_custody_generation,state,target_session_id,start_key,start_digest,expires_ms FROM social_cast_queue_transfers WHERE id=?`, id).Scan(&r.id, &r.authority, &r.account, &r.profile, &r.sourceFamily, &r.sourceDevice, &r.sourceSession, &r.generation, &r.queue, &r.entry, &r.revision, &r.position, &r.targetCast, &r.targetFamily, &r.targetDevice, &r.targetGeneration, &r.targetCustody, &r.state, &r.targetSession, &r.key, &r.digest, &r.expires)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrForbidden
	}
	return r, err
}
func (s *Service) castTransferSourceCurrent(ctx context.Context, tx *sql.Tx, r castQueueTransfer, now int64) error {
	// A durable family id alone is not current authorization. Reuse the same
	// live-token, membership, epoch and device checks as authenticated callers.
	var hash string
	if err := tx.QueryRowContext(ctx, `SELECT t.token_hash FROM authorization_session_families f JOIN authorization_family_tokens t ON t.family_id=f.id AND t.generation=f.current_generation WHERE f.id=? AND f.revoked=0`, r.sourceFamily).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUnauthorized
		}
		return err
	}
	record, err := s.familyTokenTx(ctx, tx, hash)
	if err != nil {
		return err
	}
	family, err := s.sessionFamilyRecordTx(ctx, tx, record.principal, record)
	if err != nil {
		return err
	}
	if family.ID != r.sourceFamily || family.DeviceID != r.sourceDevice || record.principal.Grant == GrantCastTarget || record.principal.Authority != r.authority || record.principal.AccountID != r.account || record.principal.ProfileID != r.profile {
		return ErrForbidden
	}
	var found int
	var horizon string
	err = tx.QueryRowContext(ctx, `SELECT 1,f.authorization_horizon FROM authorization_session_families f JOIN playback_v1_sessions s ON s.id=? JOIN queues_v1 q ON q.id=?
 WHERE f.id=? AND f.revoked=0 AND f.authority=? AND f.account_id=? AND f.profile_id=?
 AND s.device_id=? AND s.authority=f.authority AND s.account_id=f.account_id AND s.profile_id=f.profile_id
 AND s.generation=? AND s.queue_id=q.id AND s.entry_id=? AND s.ended_ms=0 AND s.state IN('preparing','playing','paused') AND s.lease_expires_ms>?
 AND q.owner_kind='device' AND q.owner_id=? AND q.authority=s.authority AND q.account_id=s.account_id AND q.profile_id=s.profile_id
 AND q.session_id=s.id AND q.revision=? AND q.current_position=? AND q.current_entry_id=s.entry_id`, r.sourceSession, r.queue, r.sourceFamily, r.authority, r.account, r.profile, r.sourceDevice, r.generation, r.entry, now, r.sourceDevice, r.revision, r.position).Scan(&found, &horizon)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	end, err := familyTime(horizon)
	if err != nil || !end.After(time.UnixMilli(now)) {
		return ErrUnauthorized
	}
	return nil
}
func (s *Service) CreateCastQueueTransferTx(ctx context.Context, tx *sql.Tx, p Principal, id string, source CastQueueSource, expires, now int64) error {
	if tx == nil || p.Grant == GrantCastTarget || !castTransferID.MatchString(id) || !castTransferID.MatchString(source.SessionID) || !castTransferID.MatchString(source.QueueID) || !castTransferID.MatchString(source.EntryID) || source.Generation < 1 {
		return ErrForbidden
	}
	revision := strings.TrimSpace(source.QueueRevision)
	if strings.HasPrefix(revision, "W/") {
		return ErrForbidden
	}
	if len(revision) >= 2 && revision[0] == '"' && revision[len(revision)-1] == '"' {
		revision = revision[1 : len(revision)-1]
	}
	rev, err := strconv.ParseInt(revision, 10, 64)
	if err != nil || rev < 0 {
		return ErrForbidden
	}
	family, err := s.SessionFamilyTx(ctx, tx, p)
	if err != nil {
		return err
	}
	if p.DeviceID != "" && p.DeviceID != family.DeviceID {
		return ErrForbidden
	}
	var position int64
	if err = tx.QueryRowContext(ctx, `SELECT current_position FROM queues_v1 WHERE id=?`, source.QueueID).Scan(&position); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		return err
	}
	if position < 0 {
		return ErrForbidden
	}
	r := castQueueTransfer{id: id, authority: p.Authority, account: p.AccountID, profile: p.ProfileID, sourceFamily: family.ID, sourceDevice: family.DeviceID, sourceSession: source.SessionID, generation: source.Generation, queue: source.QueueID, entry: source.EntryID, revision: rev, position: position}
	if err = s.castTransferSourceCurrent(ctx, tx, r, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO social_cast_queue_transfers(id,authority,account_id,profile_id,source_family_id,source_device_id,source_session_id,source_generation,queue_id,entry_id,queue_revision,source_position,state,expires_ms,created_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'pending',?,?)`, id, r.authority, r.account, r.profile, r.sourceFamily, r.sourceDevice, r.sourceSession, r.generation, r.queue, r.entry, r.revision, r.position, expires, now)
	return err
}

// Binding occurs once at the actual initial redemption, not on reconnect.
func (s *Service) BindCastQueueTransferTx(ctx context.Context, tx *sql.Tx, id, castID, family, device string, generation, now int64) error {
	r, err := readCastQueueTransfer(ctx, tx, id)
	if errors.Is(err, ErrForbidden) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.state != "pending" || r.expires <= now {
		return ErrForbidden
	}
	if err = s.castTransferSourceCurrent(ctx, tx, r, now); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE social_cast_queue_transfers SET state='bound',target_cast_id=?,target_family_id=?,target_device_id=?,target_generation=? WHERE id=? AND state='pending'`, castID, family, device, generation, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrForbidden
	}
	return nil
}
func (s *Service) transferTarget(ctx context.Context, tx *sql.Tx, p Principal, r castQueueTransfer) error {
	if p.Grant != GrantCastTarget || r.authority != p.Authority || r.account != p.AccountID || r.profile != p.ProfileID {
		return ErrForbidden
	}
	family, err := s.SessionFamilyTx(ctx, tx, p)
	if err != nil {
		return err
	}
	if family.ID != r.targetFamily || family.DeviceID != r.targetDevice || p.DeviceID != "" && p.DeviceID != family.DeviceID {
		return ErrForbidden
	}
	var found int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM social_cast_devices WHERE id=? AND session_family_id=? AND generation=? AND authority=? AND account_id=? AND profile_id=? AND state='active' AND expires_at_ms>?`, r.targetCast, r.targetFamily, r.targetGeneration, r.authority, r.account, r.profile, time.Now().UnixMilli()).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	return err
}
func (s *Service) CastQueueTransferStartTx(ctx context.Context, tx *sql.Tx, p Principal, id, queue, entry, item string) error {
	r, err := readCastQueueTransfer(ctx, tx, id)
	if err != nil {
		return err
	}
	if r.state != "bound" || r.expires <= time.Now().UnixMilli() || r.queue != queue || r.entry != entry {
		return ErrForbidden
	}
	if err = s.transferTarget(ctx, tx, p, r); err != nil {
		return err
	}
	if err = s.castTransferSourceCurrent(ctx, tx, r, time.Now().UnixMilli()); err != nil {
		return err
	}
	var actual string
	err = tx.QueryRowContext(ctx, `SELECT pid(e.public_id) FROM playback_v1_sessions s JOIN catalog_entities e ON e.id=s.item_id WHERE s.id=?`, r.sourceSession).Scan(&actual)
	if err != nil {
		return err
	}
	if actual != item {
		return ErrForbidden
	}
	return nil
}

// The caller has resolved the exact current entry and admitted the target media
// in this same accepting transaction. Custody, occurrence and receipt commit together.
func (s *Service) CommitCastQueueTransferTx(ctx context.Context, tx *sql.Tx, p Principal, id, queue, entry, item, target, key, digest string) error {
	if err := s.CastQueueTransferStartTx(ctx, tx, p, id, queue, entry, item); err != nil {
		return err
	}
	r, err := readCastQueueTransfer(ctx, tx, id)
	if err != nil {
		return err
	}
	var admitted int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM playback_v1_sessions s JOIN catalog_entities e ON e.id=s.item_id WHERE s.id=? AND s.device_id=? AND s.authority=? AND s.account_id=? AND s.profile_id=? AND s.queue_id=? AND s.entry_id=? AND pid(e.public_id)=? AND s.start_key=? AND s.start_digest=? AND s.ended_ms=0 AND s.lease_expires_ms>? AND s.state IN('preparing','playing','paused')`, target, r.targetDevice, r.authority, r.account, r.profile, r.queue, r.entry, item, key, digest, time.Now().UnixMilli()).Scan(&admitted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE queues_v1 SET owner_id=?,session_id=?,revision=revision+1,custody_generation=custody_generation+1,updated_ms=? WHERE id=? AND owner_kind='device' AND owner_id=? AND session_id=? AND revision=? AND current_position=?`, r.targetDevice, target, time.Now().UnixMilli(), queue, r.sourceDevice, r.sourceSession, r.revision, r.position)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrForbidden
	}
	result, err = tx.ExecContext(ctx, `UPDATE social_cast_queue_transfers SET state='committed',target_session_id=?,start_key=?,start_digest=?,target_custody_generation=(SELECT custody_generation FROM queues_v1 WHERE id=?) WHERE id=? AND state='bound'`, target, key, digest, queue, id)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrForbidden
	}
	return nil
}
func (s *Service) ReplayCastQueueTransferTx(ctx context.Context, tx *sql.Tx, p Principal, id, target, key, digest string) error {
	r, err := readCastQueueTransfer(ctx, tx, id)
	if err != nil {
		return err
	}
	if r.state != "committed" || r.targetSession != target || r.key != key || r.digest != digest {
		return ErrForbidden
	}
	return s.transferTarget(ctx, tx, p, r)
}
func (s *Service) CastQueueTransferStatusTx(ctx context.Context, tx *sql.Tx, p Principal, id string, cancel bool) (*CastQueueTransferStatus, error) {
	r, err := readCastQueueTransfer(ctx, tx, id)
	if errors.Is(err, ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	family, err := s.SessionFamilyTx(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	if p.Grant == GrantCastTarget || p.DeviceID != "" && p.DeviceID != family.DeviceID || r.sourceFamily != family.ID || r.sourceDevice != family.DeviceID || r.authority != p.Authority || r.account != p.AccountID || r.profile != p.ProfileID {
		return nil, ErrForbidden
	}
	if cancel && r.state != "committed" {
		if _, err = tx.ExecContext(ctx, `UPDATE social_cast_queue_transfers SET state='cancelled' WHERE id=? AND state IN('pending','bound')`, id); err != nil {
			return nil, err
		}
		r.state = "cancelled"
	}
	out := &CastQueueTransferStatus{ID: id, State: r.state}
	if r.state != "committed" && r.expires <= time.Now().UnixMilli() {
		out.State = "cancelled"
	}
	if r.state == "committed" {
		// The original family may recover only its own result, never another pairing's.
		var horizon string
		err = tx.QueryRowContext(ctx, `SELECT f.authorization_horizon FROM social_cast_devices c JOIN authorization_session_families f ON f.id=c.session_family_id WHERE c.id=? AND c.session_family_id=? AND c.generation=? AND c.authority=? AND c.account_id=? AND c.profile_id=? AND c.state='active' AND c.expires_at_ms>? AND f.revoked=0`, r.targetCast, r.targetFamily, r.targetGeneration, r.authority, r.account, r.profile, time.Now().UnixMilli()).Scan(&horizon)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrForbidden
		}
		if err != nil {
			return nil, err
		}
		expires, err := familyTime(horizon)
		if err != nil || !expires.After(time.Now()) {
			return nil, ErrUnauthorized
		}
		err = tx.QueryRowContext(ctx, `SELECT pid(e.public_id),CASE WHEN s.ended_ms>0 THEN 'ended' ELSE s.state END FROM playback_v1_sessions s JOIN catalog_entities e ON e.id=s.item_id WHERE s.id=? AND s.device_id=? AND s.authority=? AND s.account_id=? AND s.profile_id=?`, r.targetSession, r.targetDevice, r.authority, r.account, r.profile).Scan(&out.ItemID, &out.PlaybackState)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// The original live controller may stop only the accepted queue's unchanged
// custody. The private session id never becomes a cross-device browse surface.
func (s *Service) CastQueueTransferStopTargetTx(ctx context.Context, tx *sql.Tx, p Principal, id string) (string, error) {
	status, err := s.CastQueueTransferStatusTx(ctx, tx, p, id, false)
	if err != nil || status == nil || status.State != "committed" {
		return "", err
	}
	r, err := readCastQueueTransfer(ctx, tx, id)
	if err != nil {
		return "", err
	}
	var target string
	err = tx.QueryRowContext(ctx, `SELECT s.id FROM queues_v1 q JOIN playback_v1_sessions s ON s.id=q.session_id WHERE q.id=? AND q.owner_kind='device' AND q.owner_id=? AND q.custody_generation=? AND s.device_id=? AND s.queue_id=q.id AND s.authority=? AND s.account_id=? AND s.profile_id=? AND s.ended_ms=0`, r.queue, r.targetDevice, r.targetCustody, r.targetDevice, r.authority, r.account, r.profile).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return target, err
}
