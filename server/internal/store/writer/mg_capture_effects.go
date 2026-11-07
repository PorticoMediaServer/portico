package writer

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func mgCaptureStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgCaptureRead, mgCaptureAllocationRead, mgCaptureFollowersRead, mgCaptureClaim, mgCaptureRenew, mgCaptureCancel, mgCaptureAttach:
	default:
		return statementStep{}, ErrInvalid
	}
	s, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return s.statementStep, nil
}

func mgReadCapture(ctx context.Context, t *commandTurn, id string) (out mgCaptureSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgCaptureText(id, true) {
		return out, ErrInvalid
	}
	step, err := mgCaptureStep(t, mgCaptureRead)
	if err != nil {
		return out, err
	}
	row, err := t.queryRow(ctx, step, id)
	if err != nil {
		return out, err
	}
	var leader sql.NullString
	err = row.Scan(&out.id, &out.ownerKey, &out.authority, &out.accountID, &out.profileID, &out.sourceID, &out.channelID, &out.programmeID, &out.guideGeneration, &out.ruleID, &out.ruleRevision, &out.startMS, &out.endMS, &out.revision, &out.state, &out.reason, &out.claimGeneration, &out.cancelGeneration, &out.token, &out.ownerInstance, &out.leaseMS, &out.allocationID, &out.artifactID, &out.itemID, &out.nextAttemptMS, &out.recoveryAttempt, &out.hasShare, &leader, &out.updatedMS, &out.finishedMS)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	out.leaderID = mgCaptureNullableText{leader.String, leader.Valid}
	if !out.valid() {
		return out, ErrInvalid
	}
	return out, nil
}

func mgCaptureNow(ctx context.Context, t *commandTurn) (int64, error) {
	if err := t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	tx := t.transaction
	if tx == nil || tx.writer == nil || tx.writer.now == nil || tx.done || !tx.begun {
		return 0, ErrInvalid
	}
	defer tx.guardPanic()
	now := tx.writer.now()
	ms := now.UnixMilli()
	if ms < 0 || !time.UnixMilli(ms).Equal(now.Truncate(time.Millisecond)) {
		return 0, ErrInvalid
	}
	if err := tx.observeTime(now); err != nil {
		return 0, err
	}
	return ms, nil
}

func mgCaptureMatch(ctx context.Context, t *commandTurn, want mgCaptureSnapshot) error {
	if !want.valid() {
		return ErrInvalid
	}
	got, err := mgReadCapture(ctx, t, want.id)
	if err != nil {
		return err
	}
	if got != want {
		return ErrConflict
	}
	return nil
}
func (s mgCaptureSnapshot) fenceValues() []any {
	return []any{s.id, s.ownerKey, s.revision, s.state, s.token, s.claimGeneration, s.cancelGeneration, s.ownerInstance, s.leaseMS}
}
func mgCaptureWrite(ctx context.Context, t *commandTurn, id stmt.ID, before, after mgCaptureSnapshot, values ...any) (mgCaptureFacts, error) {
	step, err := mgCaptureStep(t, id)
	if err != nil {
		return mgCaptureFacts{}, err
	}
	args := append(values, before.fenceValues()...)
	result, err := t.exec(ctx, step, args...)
	if err != nil {
		return mgCaptureFacts{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return mgCaptureFacts{}, err
	}
	if n != 1 {
		return mgCaptureFacts{}, ErrConflict
	}
	got, err := mgReadCapture(ctx, t, before.id)
	if err != nil {
		return mgCaptureFacts{}, err
	}
	if got != after {
		return mgCaptureFacts{}, ErrConflict
	}
	return mgCaptureFact(got), nil
}

// Allocation acquisition/renewal is another private effect on this turn. This
// point recheck links only the actual current recording allocation; it does not
// acquire/release a slot or claim physical exclusion/permission to launch.
func mgCaptureCheckAllocation(ctx context.Context, t *commandTurn, s mgCaptureSnapshot, a mgCaptureAllocationFence, lease, now int64) error {
	if !mgCaptureText(a.id, true) || !mgCaptureText(a.token, true) || a.generation <= 0 || a.leaseMS < lease || lease <= now {
		return ErrInvalid
	}
	step, err := mgCaptureStep(t, mgCaptureAllocationRead)
	if err != nil {
		return err
	}
	row, err := t.queryRow(ctx, step, a.id)
	if err != nil {
		return err
	}
	var source, channel, resource, kind, authority, account, profile, token, state string
	var generation, expires, preemptMS int64
	err = row.Scan(&source, &channel, &resource, &kind, &authority, &account, &profile, &token, &generation, &state, &expires, &preemptMS)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if source != s.sourceID || channel != s.channelID || resource != s.id || kind != "recording" || authority != s.authority || account != s.accountID || profile != s.profileID || token != a.token || generation != a.generation || state != "active" || expires != a.leaseMS || (preemptMS != 0 && lease > preemptMS) {
		return ErrConflict
	}
	return nil
}

func mgClaimCapture(ctx context.Context, t *commandTurn, s mgCaptureSnapshot, in mgCaptureClaimInput) (out mgCaptureFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !s.valid() || !s.fresh() || !mgCaptureText(in.token, true) || !mgCaptureText(in.ownerInstance, true) || !mgCaptureText(in.artifactID, true) {
		return out, ErrInvalid
	}
	now, err := mgCaptureNow(ctx, t)
	if err != nil {
		return out, err
	}
	if s.startMS > now || s.endMS <= now || s.nextAttemptMS > now || in.leaseMS <= now {
		return out, ErrConflict
	}
	if err = mgCaptureMatch(ctx, t, s); err != nil {
		return out, err
	}
	if err = mgCaptureCheckAllocation(ctx, t, s, in.allocation, in.leaseMS, now); err != nil {
		return out, err
	}
	after := s
	after.updatedMS = now
	after.state, after.reason = "preparing", ""
	after.claimGeneration++
	after.token, after.ownerInstance, after.leaseMS = in.token, in.ownerInstance, in.leaseMS
	after.allocationID, after.artifactID = in.allocation.id, in.artifactID
	after.nextAttemptMS = 0
	after.revision++
	return mgCaptureWrite(ctx, t, mgCaptureClaim, s, after, in.token, in.ownerInstance, in.leaseMS, in.allocation.id, in.artifactID, now)
}

func mgRenewCapture(ctx context.Context, t *commandTurn, s mgCaptureSnapshot, a mgCaptureAllocationFence, lease int64) (out mgCaptureFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !s.capturing() {
		return out, ErrConflict
	}
	return mgRenewCaptureCustody(ctx, t, s, a, lease)
}

// A logically withdrawn leader can continue only for a concrete independently
// reauthorized follower. The parent supplies that exact native snapshot, NOT an
// authorized=true flag or a retained leader grant. Last-follower denial remains
// the parent's fresh core-frame decision. This never uncancels the leader.
func mgRenewCaptureForFollower(ctx context.Context, t *commandTurn, s, follower mgCaptureSnapshot, a mgCaptureAllocationFence, lease int64) (out mgCaptureFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if s.state != "cancelled" || s.reason != "owner-cancelled" || !follower.valid() || !follower.mutable() || follower.hasShare != 1 || !follower.leaderID.valid || follower.leaderID.text != s.id || !mgCaptureSameOccurrence(s, follower) {
		return out, ErrConflict
	}
	if err = mgCaptureMatch(ctx, t, follower); err != nil {
		return out, err
	}
	return mgRenewCaptureCustody(ctx, t, s, a, lease)
}

func mgRenewCaptureCustody(ctx context.Context, t *commandTurn, s mgCaptureSnapshot, a mgCaptureAllocationFence, lease int64) (mgCaptureFacts, error) {
	if !s.valid() || s.claimGeneration <= 0 || s.token == "" || s.ownerInstance == "" || s.artifactID == "" || s.allocationID != a.id || !s.unshared() {
		return mgCaptureFacts{}, ErrInvalid
	}
	now, err := mgCaptureNow(ctx, t)
	if err != nil {
		return mgCaptureFacts{}, err
	}
	if s.leaseMS <= now || lease <= s.leaseMS {
		return mgCaptureFacts{}, ErrConflict
	}
	if err = mgCaptureMatch(ctx, t, s); err != nil {
		return mgCaptureFacts{}, err
	}
	if err = mgCaptureCheckAllocation(ctx, t, s, a, lease, now); err != nil {
		return mgCaptureFacts{}, err
	}
	after := s
	after.updatedMS = now
	after.leaseMS = lease
	after.revision++
	return mgCaptureWrite(ctx, t, mgCaptureRenew, s, after, lease, now)
}

// Security denial callers must use the parent's FULL command frame. This helper
// publishes only logical cancellation; finished_ms is a logical timestamp,
// never physical retirement. All physical/resource custody survives.
func mgCancelCapture(ctx context.Context, t *commandTurn, s mgCaptureSnapshot, reason string) (out mgCaptureFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !s.valid() || (reason != "owner-cancelled" && reason != "permission-denied") {
		return out, ErrInvalid
	}
	if !s.mutable() {
		return out, ErrConflict
	}
	now, err := mgCaptureNow(ctx, t)
	if err != nil {
		return out, err
	}
	if err = mgCaptureMatch(ctx, t, s); err != nil {
		return out, err
	}
	after := s
	after.updatedMS = now
	after.state, after.reason = "cancelled", reason
	after.finishedMS = now
	after.cancelGeneration++
	after.revision++
	return mgCaptureWrite(ctx, t, mgCaptureCancel, s, after, reason, now, now)
}

func mgCaptureSameOccurrence(a, b mgCaptureSnapshot) bool {
	return a.id != b.id && a.sourceID == b.sourceID && a.channelID == b.channelID && a.programmeID == b.programmeID && a.guideGeneration == b.guideGeneration && a.startMS == b.startMS && a.endMS == b.endMS
}
func mgCaptureHasFollowers(ctx context.Context, t *commandTurn, id string) (bool, error) {
	step, err := mgCaptureStep(t, mgCaptureFollowersRead)
	if err != nil {
		return false, err
	}
	row, err := t.queryRow(ctx, step, id)
	if err != nil {
		return false, err
	}
	var follower string
	err = row.Scan(&follower)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Attach records waiting intent only. It neither copies media nor makes a
// follower complete/playable/shared-ready. Failed hard-link attempts therefore
// retain this reverse hold. Physical publication/detach awaits typed evidence.
func mgAttachCapture(ctx context.Context, t *commandTurn, follower, leader mgCaptureSnapshot) (out mgCaptureFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !follower.valid() || !leader.valid() {
		return out, ErrInvalid
	}
	if !follower.fresh() || !leader.unshared() || !mgCaptureSameOccurrence(follower, leader) || !leader.capturing() || leader.claimGeneration <= 0 || leader.token == "" || leader.ownerInstance == "" || leader.allocationID == "" || leader.artifactID == "" {
		return out, ErrConflict
	}
	now, err := mgCaptureNow(ctx, t)
	if err != nil {
		return out, err
	}
	if leader.leaseMS <= now || follower.endMS <= now {
		return out, ErrConflict
	}
	if err = mgCaptureMatch(ctx, t, follower); err != nil {
		return out, err
	}
	if err = mgCaptureMatch(ctx, t, leader); err != nil {
		return out, err
	}
	hasFollowers, err := mgCaptureHasFollowers(ctx, t, follower.id)
	if err != nil {
		return out, err
	}
	if hasFollowers {
		return out, ErrConflict
	}
	after := follower
	after.updatedMS = now
	after.hasShare = 1
	after.leaderID = mgCaptureNullableText{leader.id, true}
	after.revision++
	return mgCaptureWrite(ctx, t, mgCaptureAttach, follower, after, leader.id, now)
}
