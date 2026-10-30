package social

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Two-phase, readiness-gated handoff (sharing contract §6.2).
//
//	prepare   controller proposes; the source is retained and keeps playing
//	readiness the receiver proves it reached `playing` at a real position
//	commit    the controller ends the source v1 session and the receiver's
//	          session becomes authoritative at the committed position
//
// The three facts that make this safe:
//
//  1. Readiness is not acceptance. A receiver reporting ready moves the
//     observable outcome from `waiting` to `pending` and nothing else. Only the
//     commit step ends the source session (Playback.EndSessionTx).
//  2. Commit is fenced. It carries the handoff revision the controller last
//     read, and rechecks source/receiver session identity and liveness.
//     Ordinary progress reports do not invalidate the proposal.
//  3. Failure rolls back to the retained source. A definitive failure, a
//     rollback, an expiry, a revoked grant or a rotated receiver key all discard
//     the prepared target and leave the source exactly as it was.
//
// `state` is the durable machine (prepared, committing, committed, rolled_back,
// expired, failed). `outcome` is what a polling client reads: `waiting` until
// the receiver proves readiness, `pending` once it has, `accepted` after commit,
// `rejected` for every terminal failure.

type HandoffRequest struct {
	ProtocolVersion  string `json:"protocolVersion"`
	RequestID        string `json:"requestId"`
	ReceiverID       string `json:"receiverId"`
	GrantID          string `json:"grantId"`
	SourcePlaybackID string `json:"sourcePlaybackId"`
	StartPositionUS  string `json:"startPositionUs,omitempty"`
	ExpectedRevision string `json:"expectedPlaybackRevision,omitempty"`
}

type HandoffReadiness struct {
	ProtocolVersion    string `json:"protocolVersion"`
	Readiness          string `json:"readiness"`
	ReceiverPlaybackID string `json:"receiverPlaybackId"`
	PositionUS         string `json:"positionUs"`
	ExpectedRevision   string `json:"expectedRevision,omitempty"`
}

type HandoffCommit struct {
	ProtocolVersion  string `json:"protocolVersion"`
	ExpectedRevision string `json:"expectedRevision"`
}

type HandoffRollback struct {
	ProtocolVersion  string `json:"protocolVersion"`
	ExpectedRevision string `json:"expectedRevision,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

type Handoff struct {
	ID                  string  `json:"id"`
	ReceiverID          string  `json:"receiverId"`
	GrantID             string  `json:"grantId"`
	RequestID           string  `json:"requestId"`
	State               string  `json:"state"`
	Outcome             string  `json:"outcome"`
	Reason              string  `json:"reason"`
	Revision            string  `json:"revision"`
	ItemID              string  `json:"itemId"`
	SourcePlaybackID    string  `json:"sourcePlaybackId"`
	ReceiverPlaybackID  string  `json:"receiverPlaybackId"`
	RequestedPositionUS string  `json:"requestedPositionUs"`
	ReadyPositionUS     string  `json:"readyPositionUs"`
	CommittedPositionUS string  `json:"committedPositionUs"`
	SourceRetired       bool    `json:"sourceRetired"`
	CreatedAt           string  `json:"createdAt"`
	ExpiresAt           string  `json:"expiresAt"`
	SettledAt           *string `json:"settledAt"`
}

type HandoffResponse struct {
	ProtocolVersion string  `json:"protocolVersion"`
	ServerTime      string  `json:"serverTime"`
	Handoff         Handoff `json:"handoff"`
}

type handoffRow struct {
	id, receiverID, grantID, requestID, requestDigest string
	sourcePlaybackID, itemID                          string
	receiverPlaybackID, state, outcome, reason        string
	requestedUS, readyUS, committedUS, revision       int64
	created, expires                                  int64
	settled                                           sql.NullInt64
}

const handoffColumns = `id,receiver_id,grant_id,request_id,request_digest,source_playback_id,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=social_handoffs.item_id),''),receiver_playback_id,state,outcome,reason,requested_position_us,ready_position_us,committed_position_us,revision,created_at_ms,expires_at_ms,settled_at_ms`

func scanHandoff(row interface{ Scan(...any) error }) (handoffRow, error) {
	var h handoffRow
	e := row.Scan(&h.id, &h.receiverID, &h.grantID, &h.requestID, &h.requestDigest, &h.sourcePlaybackID, &h.itemID, &h.receiverPlaybackID, &h.state, &h.outcome, &h.reason, &h.requestedUS, &h.readyUS, &h.committedUS, &h.revision, &h.created, &h.expires, &h.settled)
	return h, e
}

func (h handoffRow) wire() Handoff {
	return Handoff{
		ID: h.id, ReceiverID: h.receiverID, GrantID: h.grantID, RequestID: h.requestID,
		State: h.state, Outcome: h.outcome, Reason: h.reason, Revision: counter(h.revision),
		ItemID: h.itemID, SourcePlaybackID: h.sourcePlaybackID, ReceiverPlaybackID: h.receiverPlaybackID,
		RequestedPositionUS: counter(h.requestedUS), ReadyPositionUS: counter(h.readyUS), CommittedPositionUS: counter(h.committedUS),
		SourceRetired: h.state == "committed", CreatedAt: stamp(h.created), ExpiresAt: stamp(h.expires),
		SettledAt: optionalStamp(h.settled.Int64),
	}
}

// settledHandoff is the expiry decision without the write: an open handoff past
// its deadline is expired, and expiry is a rollback — the prepared target is
// discarded. A reader computes this and a writer records it, so that polling the
// status of a handoff does not take the write gate. Both arrive at the same
// answer, because it is a function of the row and the clock.
func (s *Store) settledHandoff(h handoffRow) handoffRow {
	if (h.state == "prepared" || h.state == "committing") && s.ms() >= h.expires {
		h.state, h.outcome, h.reason = "expired", "rejected", "handoff-timeout"
		h.revision++
		h.settled = sql.NullInt64{Int64: s.ms(), Valid: true}
	}
	return h
}

// scanOneHandoff reads the row without deciding anything about it.
func (s *Store) scanOneHandoff(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (handoffRow, error) {
	h, e := scanHandoff(tx.QueryRowContext(ctx, `SELECT `+handoffColumns+` FROM social_handoffs WHERE id=? AND authority=? AND account_id=? AND profile_id=?`, id, p.Authority, p.AccountID, p.ProfileID))
	if errors.Is(e, sql.ErrNoRows) {
		return h, errNotFound
	}
	return h, e
}

// loadHandoff is the write path's form: it records the expiry it finds, because
// a caller about to act on this handoff must not act on a state the database
// disagrees with.
func (s *Store) loadHandoff(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (handoffRow, error) {
	h, e := s.scanOneHandoff(ctx, tx, p, id)
	if e != nil {
		return h, e
	}
	settled := s.settledHandoff(h)
	if settled.state == h.state {
		return h, nil
	}
	if _, e = tx.ExecContext(ctx, `UPDATE social_handoffs SET state='expired',outcome='rejected',reason='handoff-timeout',revision=?,settled_at_ms=? WHERE id=?`, settled.revision, s.ms(), h.id); e != nil {
		return h, e
	}
	return settled, nil
}

// PrepareHandoff proposes a transfer. It never touches the source.
func (s *Store) PrepareHandoff(ctx context.Context, p identity.Principal, req HandoffRequest) (HandoffResponse, error) {
	var out HandoffResponse
	if req.ProtocolVersion != Protocol || !ValidID(req.RequestID) || !ValidID(req.ReceiverID) || !ValidID(req.GrantID) || !ValidID(req.SourcePlaybackID) {
		return out, errInvalid
	}
	start, ok := parseCounter(req.StartPositionUS)
	if !ok && req.StartPositionUS != "" {
		return out, errInvalid
	}
	if s.Playback == nil {
		return out, &Fault{Code: "playback_authority_unavailable", Status: 503, Message: "Playback authority is not available.", Retryable: true}
	}
	gated, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = s.limit(ctx, tx, "handoff-create:"+viewerKey(p.Viewer), 60, time.Minute); e != nil {
		return out, e
	}
	// A retried prepare with the same request id returns the same handoff; the
	// same id with a different proposal is a conflict.
	want := digestJSON(req)
	existing, e := scanHandoff(tx.QueryRowContext(ctx, `SELECT `+handoffColumns+` FROM social_handoffs WHERE authority=? AND account_id=? AND profile_id=? AND request_id=?`, p.Authority, p.AccountID, p.ProfileID, req.RequestID))
	if e == nil {
		if !sameSecret(existing.requestDigest, want) {
			return out, &Fault{Code: "idempotency_key_reused", Status: 409, Message: "That request id was used for a different handoff."}
		}
		return HandoffResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), Handoff: existing.wire()}, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	receiver, e := s.readReceiver(ctx, tx, p, req.ReceiverID)
	if e != nil {
		return out, e
	}
	if receiver.State != "active" {
		return out, &Fault{Code: "target_unavailable", Status: 409, Message: "That receiver is retired."}
	}
	grant, e := s.readGrant(ctx, tx, p, req.GrantID)
	if e != nil {
		return out, e
	}
	if grant.ReceiverID != req.ReceiverID {
		return out, errNotFound
	}
	if grant.State != "accepted" {
		return out, &Fault{Code: "grant_not_accepted", Status: 403, Message: "That receiver has not authorized this controller."}
	}
	if !sameSecret(grant.KeyFingerprint, receiver.KeyFingerprint) {
		return out, &Fault{Code: "receiver_key_mismatch", Status: 403, Message: "The authorization was issued against a different receiver key."}
	}
	if !contains(grant.AllowedCommands, "load") {
		return out, &Fault{Code: "participant_incompatible", Status: 403, Message: "The authorization does not permit loading media."}
	}
	source, e := s.Playback.SessionTx(ctx, tx, p, req.SourcePlaybackID)
	if errors.Is(e, sql.ErrNoRows) {
		return out, errNotFound
	}
	if e != nil {
		return out, e
	}
	if !source.Live {
		return out, &Fault{Code: "handoff_source_conflict", Status: 409, Message: "The source is no longer playing."}
	}
	if req.ExpectedRevision != "" {
		expected, ok := parseCounter(req.ExpectedRevision)
		if !ok {
			return out, errInvalid
		}
		if expected != source.Revision {
			return out, &Fault{Code: "handoff_source_revision_conflict", Status: 409, Message: "The source session changed.", Detail: map[string]any{"currentRevision": counter(source.Revision)}}
		}
	}
	// Only one open handoff per source: two prepared proposals racing for the
	// same terminal is exactly the ambiguity the contract forbids.
	var open int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM social_handoffs WHERE source_playback_id=? AND state IN ('prepared','committing')`, req.SourcePlaybackID).Scan(&open); e != nil {
		return out, e
	}
	if open > 0 {
		return out, &Fault{Code: "handoff_in_progress", Status: 409, Message: "That source already has a handoff in flight."}
	}
	if req.StartPositionUS == "" {
		start = source.PositionUS
	}
	now := s.ms()
	id := token("hdf_")
	entity, e := resolveSocialItem(ctx, tx, source.ItemID)
	if e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO social_handoffs(id,receiver_id,grant_id,authority,account_id,profile_id,request_id,request_digest,source_playback_id,item_id,requested_position_us,state,outcome,revision,created_at_ms,expires_at_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,'prepared','waiting',1,?,?)`,
		id, req.ReceiverID, req.GrantID, p.Authority, p.AccountID, p.ProfileID, req.RequestID, want, req.SourcePlaybackID, entity, start, now, now+HandoffTTLSeconds*1000); e != nil {
		return out, e
	}
	h, e := s.loadHandoff(ctx, tx, p, id)
	if e != nil {
		return out, e
	}
	if e = gated.Commit(); e != nil {
		return out, e
	}
	s.publishReceiverEvent(p, req.ReceiverID, "handoff")
	return HandoffResponse{ProtocolVersion: Protocol, ServerTime: stamp(now), Handoff: h.wire()}, nil
}

// ReadHandoff is the status poll both halves use.
func (s *Store) ReadHandoff(ctx context.Context, p identity.Principal, id string) (HandoffResponse, error) {
	var out HandoffResponse
	// A status poll is a read: both halves of a hand-off poll this every second
	// or two while it is open, and every one of them took the single writer.
	// A read, on a snapshot, with no write gate.
	tx, done, e := dbwork.BeginRead(ctx, s.DB)
	if e != nil {
		return out, e
	}
	defer done()
	h, e := s.scanOneHandoff(ctx, tx, p, id)
	if e != nil {
		return out, e
	}
	return HandoffResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), Handoff: s.settledHandoff(h).wire()}, nil
}

// ReportHandoffReadiness is the receiver proving it reached `playing`. The only
// accepted readiness value is `playing`: an HTTP success, a metadata load or a
// buffered first segment is not readiness.
func (s *Store) ReportHandoffReadiness(ctx context.Context, p identity.Principal, id string, req HandoffReadiness) (HandoffResponse, error) {
	var out HandoffResponse
	if req.ProtocolVersion != Protocol || req.Readiness != "playing" || !ValidID(req.ReceiverPlaybackID) {
		return out, errInvalid
	}
	position, ok := parseCounter(req.PositionUS)
	if !ok {
		return out, errInvalid
	}
	gated3, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	h, e := s.loadHandoff(ctx, tx, p, id)
	if e != nil {
		return out, e
	}
	if h.state != "prepared" {
		return out, handoffStateFault(h)
	}
	if req.ExpectedRevision != "" {
		expected, ok := parseCounter(req.ExpectedRevision)
		if !ok {
			return out, errInvalid
		}
		if expected != h.revision {
			return out, &Fault{Code: "revision_conflict", Status: 409, Message: "The handoff changed.", Detail: map[string]any{"currentRevision": counter(h.revision)}}
		}
	}
	grant, e := s.readGrant(ctx, tx, p, h.grantID)
	if e != nil {
		return out, e
	}
	if grant.State != "accepted" {
		return out, &Fault{Code: "grant_not_accepted", Status: 403, Message: "That authorization is no longer valid."}
	}
	receiver, e := s.readReceiver(ctx, tx, p, h.receiverID)
	if e != nil {
		return out, e
	}
	if p.DeviceID == "" || p.DeviceID != receiver.DeviceID {
		return out, fault("receiver_required", 403, "Only the receiving device may do that.")
	}
	if e = s.checkReceiverPlayback(ctx, tx, p, h, req.ReceiverPlaybackID); e != nil {
		return out, e
	}
	h.revision++
	h.receiverPlaybackID, h.readyUS, h.outcome = req.ReceiverPlaybackID, position, "pending"
	if _, e = tx.ExecContext(ctx, `UPDATE social_handoffs SET receiver_playback_id=?,ready_position_us=?,outcome='pending',revision=? WHERE id=?`, req.ReceiverPlaybackID, position, h.revision, id); e != nil {
		return out, e
	}
	return HandoffResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), Handoff: h.wire()}, gated3.Commit()
}

// CommitHandoff is the only step that retires the source. It requires the
// receiver to have already proven readiness, and it refuses a source whose own
// playback revision moved after the handoff was prepared.
func (s *Store) CommitHandoff(ctx context.Context, p identity.Principal, id string, req HandoffCommit) (HandoffResponse, error) {
	var out HandoffResponse
	if req.ProtocolVersion != Protocol {
		return out, errInvalid
	}
	expected, ok := parseCounter(req.ExpectedRevision)
	if !ok {
		return out, errInvalid
	}
	if s.Playback == nil {
		return out, &Fault{Code: "playback_authority_unavailable", Status: 503, Message: "Playback authority is not available.", Retryable: true}
	}
	gated4, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	h, e := s.loadHandoff(ctx, tx, p, id)
	if e != nil {
		return out, e
	}
	if h.state == "committed" {
		// A retried commit is the same commit.
		return HandoffResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), Handoff: h.wire()}, nil
	}
	if h.state != "prepared" {
		return out, handoffStateFault(h)
	}
	if h.outcome != "pending" {
		return out, &Fault{Code: "handoff_not_ready", Status: 409, Message: "The receiver has not reported that it is playing."}
	}
	if expected != h.revision {
		return out, &Fault{Code: "revision_conflict", Status: 409, Message: "The handoff changed.", Detail: map[string]any{"currentRevision": counter(h.revision)}}
	}
	grant, e := s.readGrant(ctx, tx, p, h.grantID)
	if e != nil {
		return out, e
	}
	if grant.State != "accepted" {
		return out, &Fault{Code: "grant_not_accepted", Status: 403, Message: "That authorization is no longer valid."}
	}
	if e = s.checkReceiverPlayback(ctx, tx, p, h, h.receiverPlaybackID); e != nil {
		return out, e
	}
	source, e := s.Playback.SessionTx(ctx, tx, p, h.sourcePlaybackID)
	if errors.Is(e, sql.ErrNoRows) {
		return out, s.failHandoff(ctx, gated4, h, "handoff-source-missing")
	}
	if e != nil {
		return out, e
	}
	if source.ItemID != h.itemID {
		return out, s.failHandoff(ctx, gated4, h, "handoff-source-changed")
	}
	if !source.Live {
		return out, s.failHandoff(ctx, gated4, h, "handoff-source-terminal")
	}
	now := s.ms()
	if _, e = tx.ExecContext(ctx, `UPDATE social_handoffs SET state='committing' WHERE id=?`, id); e != nil {
		return out, e
	}
	// The source session ends in this transaction; its media and wakes stop only
	// after the commit, so a rollback leaves it playing.
	after, e := s.Playback.EndSessionTx(ctx, tx, p, h.sourcePlaybackID, "transferred")
	if e != nil {
		return out, e
	}
	h.revision++
	h.state, h.outcome, h.committedUS = "committed", "accepted", h.readyUS
	h.settled = sql.NullInt64{Int64: now, Valid: true}
	if _, e = tx.ExecContext(ctx, `UPDATE social_handoffs SET state='committed',outcome='accepted',committed_position_us=?,revision=?,settled_at_ms=? WHERE id=?`, h.committedUS, h.revision, now, id); e != nil {
		return out, e
	}
	if e = gated4.Commit(); e != nil {
		return out, e
	}
	if after != nil {
		after()
	}
	return HandoffResponse{ProtocolVersion: Protocol, ServerTime: stamp(now), Handoff: h.wire()}, nil
}

// RollbackHandoff discards a prepared target. The source is left untouched: it
// was never retired, so there is nothing to restore.
func (s *Store) RollbackHandoff(ctx context.Context, p identity.Principal, id string, req HandoffRollback) (HandoffResponse, error) {
	var out HandoffResponse
	if req.ProtocolVersion != Protocol {
		return out, errInvalid
	}
	if req.Reason == "" {
		req.Reason = "controller-cancelled"
	}
	if !text(req.Reason, 64) {
		return out, errInvalid
	}
	gated5, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	h, e := s.loadHandoff(ctx, tx, p, id)
	if e != nil {
		return out, e
	}
	if h.state == "rolled_back" {
		return HandoffResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), Handoff: h.wire()}, nil
	}
	if h.state != "prepared" {
		return out, handoffStateFault(h)
	}
	if req.ExpectedRevision != "" {
		expected, ok := parseCounter(req.ExpectedRevision)
		if !ok {
			return out, errInvalid
		}
		if expected != h.revision {
			return out, &Fault{Code: "revision_conflict", Status: 409, Message: "The handoff changed.", Detail: map[string]any{"currentRevision": counter(h.revision)}}
		}
	}
	now := s.ms()
	h.revision++
	h.state, h.outcome, h.reason = "rolled_back", "rejected", req.Reason
	h.settled = sql.NullInt64{Int64: now, Valid: true}
	if _, e = tx.ExecContext(ctx, `UPDATE social_handoffs SET state='rolled_back',outcome='rejected',reason=?,revision=?,settled_at_ms=? WHERE id=?`, req.Reason, h.revision, now, id); e != nil {
		return out, e
	}
	return HandoffResponse{ProtocolVersion: Protocol, ServerTime: stamp(now), Handoff: h.wire()}, gated5.Commit()
}

// failHandoff records a definitive pre-commit failure and commits it, so the
// caller sees a settled handoff rather than a retryable error.
func (s *Store) failHandoff(ctx context.Context, gatedArg *dbwork.Write, h handoffRow, reason string) error {
	tx := gatedArg.Tx()
	now := s.ms()
	if _, e := tx.ExecContext(ctx, `UPDATE social_handoffs SET state='failed',outcome='rejected',reason=?,revision=revision+1,settled_at_ms=? WHERE id=?`, reason, now, h.id); e != nil {
		return e
	}
	if e := gatedArg.Commit(); e != nil {
		return e
	}
	return &Fault{Code: "handoff_source_conflict", Status: 409, Message: "The source is no longer playing; the handoff was rolled back.", Detail: map[string]any{"handoffId": h.id, "reason": reason}}
}

func handoffStateFault(h handoffRow) *Fault {
	if h.state == "expired" {
		return &Fault{Code: "handoff_expired", Status: 410, Message: "The prepared handoff expired and was rolled back.", Detail: map[string]any{"state": h.state, "reason": h.reason}}
	}
	return &Fault{Code: "handoff_state_conflict", Status: 409, Message: "The handoff is not open.", Detail: map[string]any{"state": h.state, "outcome": h.outcome, "reason": h.reason}}
}

// Readiness must describe the live session on the named receiving device.
func (s *Store) checkReceiverPlayback(ctx context.Context, tx *sql.Tx, p identity.Principal, h handoffRow, id string) error {
	if s.Playback == nil {
		return fault("playback_authority_unavailable", 503, "Playback authority is not available.")
	}
	receiver, e := s.readReceiver(ctx, tx, p, h.receiverID)
	if e != nil {
		return e
	}
	facts, e := s.Playback.SessionTx(ctx, tx, p, id)
	if errors.Is(e, sql.ErrNoRows) {
		return fault("handoff_not_ready", 409, "The receiver is not playing that title.")
	}
	if e != nil {
		return e
	}
	if !facts.Live || facts.DeviceID != receiver.DeviceID || facts.ItemID != h.itemID || facts.State != "playing" {
		return fault("handoff_not_ready", 409, "The receiver is not playing that title.")
	}
	return nil
}
