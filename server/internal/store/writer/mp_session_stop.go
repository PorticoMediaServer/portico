package writer

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"time"
)

type mpSessionStopAuthorization struct {
	self             *mpSessionStopAuthorization
	turn             *commandTurn
	life             context.Context
	account, profile string
	purpose          CommandID
	nowMS            int64
}

// Private SAME-turn prefix. Trusted composition supplies ordinary auth facts;
// all account/profile/family/token birth and revision facts are re-read natively.
// This is neither a public constructor nor an accepting command/receipt driver.
func authorizeMPSessionStop(ctx context.Context, t *commandTurn, s wcProgressScope, life context.Context) (out *mpSessionStopAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || life == nil || life.Err() != nil {
		return nil, ErrDisclosureDenied
	}
	purpose := t.transaction.descriptor.ID
	if purpose != 715 && purpose != 718 {
		return nil, ErrInvalid
	}
	// Retained own-account stop permits an ordinary viewer to free a stream
	// on a hidden profile. It grants no item/transport/media disclosure.
	id := wcProgressNativeScope
	args := []any{s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.memberLibraries, s.memberRevision, s.profileRevision, s.membershipRole, s.primary, s.primaryProfile}
	if s.profileLibraries.Valid {
		id = wcProgressNativeProfileScope
		args = append(args, s.profileLibraries.String)
	}
	row, err := wc008DraftQuery(ctx, t, id, args...)
	if err != nil {
		return nil, err
	}
	var epoch, primary int64
	if err = row.Scan(&epoch, &primary); err != nil {
		return nil, errors.Join(ErrDisclosureDenied, err)
	}
	if epoch != s.epoch || s.role != "member" && (primary == 0 || s.membershipRole != s.role) {
		return nil, ErrDisclosureDenied
	}
	row, err = wc008DraftQuery(ctx, t, wcProgressNativeToken, s.tokenHash, s.familyID, s.serverID, s.accountID, s.profileID, s.role, s.epoch, s.tokenGeneration, s.memberBirth, s.profileBirth, s.horizon, s.expires)
	if err != nil {
		return nil, err
	}
	var generation int64
	if err = row.Scan(&generation); err != nil {
		return nil, errors.Join(ErrDisclosureDenied, err)
	}
	horizon, e := time.Parse(time.RFC3339Nano, s.horizon)
	if e != nil {
		return nil, ErrDisclosureDenied
	}
	expiry, e := time.Parse(time.RFC3339Nano, s.expires)
	if e != nil {
		return nil, ErrDisclosureDenied
	}
	deadline, ok := t.transaction.ctx.Deadline()
	if !ok || generation != s.tokenGeneration || deadline.After(horizon) || deadline.After(expiry) {
		return nil, ErrDisclosureDenied
	}
	now, err := mpSessionStopTime(ctx, t)
	if err != nil {
		return nil, err
	}
	out = &mpSessionStopAuthorization{turn: t, life: life, account: s.accountID, profile: s.profileID, purpose: purpose, nowMS: now}
	out.self = out
	return out, nil
}

// Existing MP05 supervisor lifetime is the cleanup owner; issuer revocation
// cannot stand in for exit or prevent an eligible expiry. This grants ONLY 716,
// never account-stop718 or a public/user root. Native server binding is current.
func authorizeMPSessionExpiry(ctx context.Context, t *commandTurn, s *mpPhysicalSupervisor) (out *mpSessionStopAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != 716 || s == nil || s.registry == nil {
		return nil, ErrInvalid
	}
	s.registry.mu.Lock()
	defer s.registry.mu.Unlock()
	if err = s.check(s.writer); err != nil {
		return nil, err
	}
	if s.writer.effective == nil || s.writer.effective.executor != t.transaction.writer {
		return nil, ErrDisclosureDenied
	}
	if _, err = wc008DraftInteger(ctx, t, mpPhysicalServer, s.server); err != nil {
		return nil, err
	}
	now, err := mpSessionStopTime(ctx, t)
	if err != nil {
		return nil, err
	}
	out = &mpSessionStopAuthorization{turn: t, life: s.life, purpose: 716, nowMS: now}
	out.self = out
	return out, nil
}
func mpSessionStopTime(ctx context.Context, t *commandTurn) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.writer == nil || t.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	now := t.transaction.writer.now()
	if err = t.transaction.observeTime(now); err != nil {
		return 0, err
	}
	if out = now.UnixMilli(); out <= 0 || out > 9999999999999 {
		return 0, ErrInvalid
	}
	return out, nil
}

type mpSessionStopInput struct {
	SessionID, MediaSessionID, ExecutionID, AccountID, ProfileID, RetireRequestID string
	LogicalRevision, LogicalGeneration, PhysicalRevision, PhysicalGeneration      int64
}
type mpSessionStopValue struct {
	Changed, PhysicalPending                   bool
	LogicalRevision, PhysicalRevision, EndedMS int64
	RetireRequestID                            string
}

const (
	mpSessionStopCurrent  stmt.ID = "mp.session_stop.current"
	mpSessionStopPhysical stmt.ID = "mp.session_stop.physical"
	mpSessionStopRequest  stmt.ID = "mp.session_stop.request"
	mpSessionStopLogical  stmt.ID = "mp.session_stop.logical"
)

// Fresh-only effect after the outer owner classifies its native session stream.
// The outer accepting frame owns its receipt/events/debt and commit. No Applied,
// physical release or receipt GC is possible here. Any error rolls back ALL of W.
func applyMPSessionStop(ctx context.Context, t *commandTurn, a *mpSessionStopAuthorization, q mpSessionStopInput) (out mpSessionStopValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if a == nil || a.self != a || a.turn != t || t == nil || t.transaction == nil || a.purpose != t.transaction.descriptor.ID || a.life == nil || a.life.Err() != nil {
		return out, ErrDisclosureDenied
	}
	for _, s := range []string{q.SessionID, q.MediaSessionID, q.ExecutionID, q.AccountID, q.ProfileID, q.RetireRequestID} {
		if !mpWorkerID(s) {
			return out, ErrInvalid
		}
	}
	for _, n := range []int64{q.LogicalRevision, q.LogicalGeneration, q.PhysicalRevision, q.PhysicalGeneration} {
		if n <= 0 || n == 9223372036854775807 {
			return out, ErrInvalid
		}
	}
	if a.purpose != 716 && (a.account != q.AccountID || a.purpose == 715 && a.profile != q.ProfileID) {
		return out, ErrDisclosureDenied
	}
	row, err := wc008DraftQuery(ctx, t, mpSessionStopCurrent, q.SessionID, q.LogicalRevision, q.LogicalGeneration, q.MediaSessionID, q.PhysicalGeneration, q.AccountID, q.ProfileID)
	if err != nil {
		return out, err
	}
	var ended, lease int64
	if err = row.Scan(&ended, &lease); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	row, err = wc008DraftQuery(ctx, t, mpSessionStopPhysical, q.MediaSessionID, q.PhysicalRevision, q.PhysicalGeneration, q.ExecutionID, q.AccountID, q.ProfileID)
	if err != nil {
		return out, err
	}
	var retired, exited, grant int64
	var request, state string
	if err = row.Scan(&retired, &exited, &request, &state, &grant); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if len(request) > 128 || len(state) > 128 {
		return out, ErrResolveRequired
	}
	out = mpSessionStopValue{LogicalRevision: q.LogicalRevision, PhysicalRevision: q.PhysicalRevision, EndedMS: ended, RetireRequestID: request, PhysicalPending: exited != q.PhysicalGeneration}
	if ended > 0 {
		if retired != q.PhysicalGeneration || state != "stopped" || grant != 1 {
			return out, ErrConflict
		}
		return out, nil
	}
	if a.purpose == 716 && lease > a.nowMS {
		return out, ErrConflict
	}
	if retired == 0 && exited == 0 {
		if _, err = wc008DraftInteger(ctx, t, mpSessionStopRequest, q.RetireRequestID, q.MediaSessionID, q.PhysicalRevision, q.PhysicalGeneration, q.ExecutionID, q.AccountID, q.ProfileID); err != nil {
			return out, err
		}
		out.PhysicalRevision++
		out.RetireRequestID = q.RetireRequestID
	} else if retired != q.PhysicalGeneration || request == "" || state != "stopped" || grant != 1 {
		return out, ErrConflict
	}
	reason := "stopped"
	if a.purpose == 716 {
		reason = "lease_expired"
	}
	revision, err := wc008DraftInteger(ctx, t, mpSessionStopLogical, a.nowMS, reason, a.nowMS, q.SessionID, q.LogicalRevision, q.LogicalGeneration, q.MediaSessionID, q.PhysicalGeneration, q.AccountID, q.ProfileID)
	if err != nil {
		return out, err
	}
	out.Changed = true
	out.LogicalRevision = revision
	out.EndedMS = a.nowMS
	return out, t.checkLifetimes(ctx)
}

// Native due/account selection is one raw candidate, no account-wide fan-out.
// The outer cursor owns retries and receipt originals. Stop never deletes a row.
const mpSessionStopAccountNext stmt.ID = "mp.session_stop.account_next"

func nextMPSessionAccountStop(ctx context.Context, t *commandTurn, a *mpSessionStopAuthorization, after string) (out string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if a == nil || a.self != a || a.turn != t || a.purpose != 718 || a.life == nil || a.life.Err() != nil {
		return "", ErrDisclosureDenied
	}
	row, err := wc008DraftQuery(ctx, t, mpSessionStopAccountNext, a.account, after)
	if err != nil {
		return "", err
	}
	err = row.Scan(&out)
	if errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return out, err
}

const mpSessionStopExpiryNext stmt.ID = "mp.session_stop.expiry_next"

// The existing lease index chooses one earliest due local VOD row with a
// physical occurrence this recipe can stop. Channel sessions belong to MP12;
// they must not block VOD expiry. A successful stop removes this candidate from
// ended_ms=0, so restart needs no volatile progress cursor.
func nextMPSessionExpiry(ctx context.Context, t *commandTurn, a *mpSessionStopAuthorization) (out string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if a == nil || a.self != a || a.turn != t || a.purpose != 716 || a.life == nil || a.life.Err() != nil {
		return "", ErrDisclosureDenied
	}
	row, err := wc008DraftQuery(ctx, t, mpSessionStopExpiryNext, a.nowMS)
	if err != nil {
		return "", err
	}
	if err = row.Scan(&out); err != nil {
		return "", err
	}
	if !mpWorkerID(out) {
		return "", ErrResolveRequired
	}
	return out, nil
}
