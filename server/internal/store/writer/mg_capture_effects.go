package writer

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Bodies only on the existing admitted commandTurn. The frozen allocation,
// launch, source and claim values are NOT authority: the accepting frame
// resolves the current US owner session and original receipt first, and W owns
// admission, events, quota and COMMIT. No provider, tuner, decoder, catalogue or
// filesystem IO happens inside these holds.
func mgCaptureStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgCaptureRead, mgCaptureSourceRead, mgCaptureAllocationRead, mgCaptureLaunchRead, mgCaptureClaim, mgCaptureRenew, mgCaptureCheckpoint:
	default:
		return statementStep{}, ErrInvalid
	}
	s, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return s.statementStep, nil
}

func mgCaptureQuery(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := mgCaptureStep(t, id)
	if err != nil {
		return commandRow{}, err
	}
	return t.queryRow(ctx, step, args...)
}

func mgCaptureMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}

func scanMGCapture(row commandRow) (out mgCaptureRow, err error) {
	if err = row.Scan(mgCaptureTargets(&out)...); err != nil {
		return mgCaptureRow{}, err
	}
	if !out.valid() {
		return mgCaptureRow{}, ErrInvalid
	}
	return out, nil
}

func mgReadCapture(ctx context.Context, t *commandTurn, id string) (out mgCaptureRow, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgRecordingHex(id, 64) {
		return out, ErrInvalid
	}
	row, err := mgCaptureQuery(ctx, t, mgCaptureRead, id)
	if err != nil {
		return out, err
	}
	out, err = scanMGCapture(row)
	if err != nil {
		return out, err
	}
	return out, nil
}

func mgCaptureMatch(ctx context.Context, t *commandTurn, want mgCaptureRow) error {
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

func mgCaptureTime(ctx context.Context, t *commandTurn) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	now, err := mgPersonalNow(ctx, t)
	if err != nil {
		return 0, err
	}
	ms := now.UnixMilli()
	if ms < 0 || !time.UnixMilli(ms).Equal(now.Truncate(time.Millisecond)) {
		return 0, ErrInvalid
	}
	return ms, nil
}

// The parent-selected revision must still be the source's current native
// revision and the source must still be active and unremoved. A claim also
// requires the pinned guide generation to be the current head; an in-progress
// capture renewal survives a guide replacement because the physical occurrence
// and its copied programme do not depend on today's head. A stale revision, a
// disabled or removed source and (for a claim) a replaced head refuse with no
// effects.
func mgCaptureCheckSource(ctx context.Context, t *commandTurn, s mgCaptureRow, revision int64, requireHead bool) error {
	if !mgRecordingHex(s.source, 48) || !mgRecordingHex(s.generation, 48) || revision < 1 {
		return ErrInvalid
	}
	row, err := mgCaptureQuery(ctx, t, mgCaptureSourceRead, s.source)
	if err != nil {
		return err
	}
	var current, removed int64
	var state, active string
	if err = row.Scan(&current, &state, &active, &removed); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	} else if err != nil {
		return err
	}
	if current != revision || state != "active" || removed != 0 || (requireHead && active != s.generation) {
		return ErrConflict
	}
	return nil
}

// Allocation custody belongs to the tuner command that wrote the row. This
// point proves the exact current (kind, resource, owner, source, channel,
// generation, token, lease) tuple; it acquires and releases nothing and is not
// physical exclusion or permission to launch.
func mgCaptureCheckAllocation(ctx context.Context, t *commandTurn, s mgCaptureRow, a mgCaptureAllocationFence, lease, now int64) error {
	if !a.valid() || lease <= now || a.id != s.allocationID || a.generation != s.allocationGeneration || a.token != s.allocationToken {
		return ErrInvalid
	}
	row, err := mgCaptureQuery(ctx, t, mgCaptureAllocationRead, a.id)
	if err != nil {
		return err
	}
	var source, channel, resource, kind, authority, account, profile, token, state string
	var generation, expires, preemptMS int64
	if err = row.Scan(&source, &channel, &resource, &kind, &authority, &account, &profile, &token, &generation, &state, &expires, &preemptMS); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	} else if err != nil {
		return err
	}
	if source != s.source || channel != s.channel || resource != s.id || kind != "recording" ||
		authority != s.authority || account != s.account || profile != s.profile || token != a.token ||
		generation != a.generation || state != "active" || expires != a.leaseMS || a.leaseMS < lease ||
		(preemptMS != 0 && lease > preemptMS) {
		return ErrConflict
	}
	return nil
}

// A launch identity is unique across every recording. A second worker with the
// same launch is refused before any row effect, without trusting its claim.
func mgCaptureCheckLaunch(ctx context.Context, t *commandTurn, launch mgCaptureLaunch) error {
	if !launch.valid() {
		return ErrInvalid
	}
	row, err := mgCaptureQuery(ctx, t, mgCaptureLaunchRead, launch[:])
	if err != nil {
		return err
	}
	var existing string
	if err = row.Scan(&existing); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if !mgRecordingHex(existing, 64) {
		return ErrInvalid
	}
	return ErrConflict
}

func mgCaptureWrite(ctx context.Context, t *commandTurn, id stmt.ID, before, after mgCaptureRow, c mgCaptureCommand, values ...any) (out mgCaptureFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	step, err := mgCaptureStep(t, id)
	if err != nil {
		return out, err
	}
	args := append(values, before.fenceValues()...)
	result, err := t.exec(ctx, step, args...)
	if err != nil {
		return out, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	if n != 1 {
		return out, ErrConflict
	}
	got, err := mgReadCapture(ctx, t, before.id)
	if err != nil {
		return out, err
	}
	if got != after {
		return out, ErrConflict
	}
	return mgCaptureFacts{row: got, command: c}, nil
}

// The row must still be the exact fresh intent, no other recording may own this
// launch, the source must be current and the allocation must still hold this
// recording. The claim records the launch and allocation witness; it does not
// start capture, acquire bytes or retire anything.
func mgClaimCapture(ctx context.Context, t *commandTurn, s mgCaptureRow, c mgCaptureCommand, in mgCaptureClaimRequest) (out mgCaptureFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err := in.validate(); err != nil {
		return out, err
	}
	if !c.next(s) || !s.valid() || !s.ownerIs(in.owner) || s.revision != in.expected {
		return out, ErrConflict
	}
	if !s.fresh() {
		return out, ErrConflict
	}
	now, err := mgCaptureTime(ctx, t)
	if err != nil {
		return out, err
	}
	if s.startMS > now || s.endMS <= now || s.nextAttemptMS > now || in.leaseMS <= now {
		return out, ErrConflict
	}
	if err = mgCaptureMatch(ctx, t, s); err != nil {
		return out, err
	}
	if err = mgCaptureCheckSource(ctx, t, s, in.sourceRevision, true); err != nil {
		return out, err
	}
	if err = mgCaptureCheckLaunch(ctx, t, in.launch); err != nil {
		return out, err
	}
	after := s
	after.state, after.reason = "preparing", ""
	after.claimGeneration++
	after.token, after.ownerInstance, after.leaseMS = in.token, in.ownerInstance, in.leaseMS
	after.allocationID, after.artifactID = in.allocation.id, in.artifactID
	after.allocationGeneration, after.allocationToken = in.allocation.generation, in.allocation.token
	after.launch = mgRecordingOptionalID{id: mgRecordingID16(in.launch), valid: true}
	after.sourceRevision, after.sourceFence = in.sourceRevision, in.sourceFence
	after.nextAttemptMS = 0
	after.revision++
	after.lastSequence++
	after.updatedMS = now
	if err = mgCaptureCheckAllocation(ctx, t, after, in.allocation, in.leaseMS, now); err != nil {
		return out, err
	}
	return mgCaptureWrite(ctx, t, mgCaptureClaim, s, after, c, in.token, in.ownerInstance,
		in.leaseMS, in.allocation.id, in.artifactID, now, in.launch[:], in.allocation.generation,
		in.allocation.token, in.sourceRevision, in.sourceFence)
}

// Renewal extends only the logical lease of the exact live claim. An expired
// lease, a stale claim generation/token/instance/launch, a changed allocation,
// a retired source revision or any physical-exit advance refuses.
func mgRenewCapture(ctx context.Context, t *commandTurn, s mgCaptureRow, c mgCaptureCommand, in mgCaptureRenewRequest) (out mgCaptureFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err := in.validate(); err != nil {
		return out, err
	}
	if !c.next(s) || !s.valid() || !s.ownerIs(in.owner) || s.revision != in.expected {
		return out, ErrConflict
	}
	if !s.capturing() || !s.unshared() || s.physicalExitSequence != 0 || !s.claimIs(in.claimGeneration, in.token, in.ownerInstance, in.launch) || !s.allocationIs(mgCaptureAllocationIdentity{in.allocation.id, in.allocation.token, in.allocation.generation}) {
		return out, ErrConflict
	}
	if s.sourceRevision < 1 || !mgCaptureFence(s.sourceFence) {
		return out, ErrConflict
	}
	now, err := mgCaptureTime(ctx, t)
	if err != nil {
		return out, err
	}
	if s.leaseMS <= now || in.leaseMS <= s.leaseMS {
		return out, ErrConflict
	}
	if err = mgCaptureMatch(ctx, t, s); err != nil {
		return out, err
	}
	if err = mgCaptureCheckSource(ctx, t, s, s.sourceRevision, false); err != nil {
		return out, err
	}
	if err = mgCaptureCheckAllocation(ctx, t, s, in.allocation, in.leaseMS, now); err != nil {
		return out, err
	}
	after := s
	after.leaseMS = in.leaseMS
	after.revision++
	after.lastSequence++
	after.updatedMS = now
	return mgCaptureWrite(ctx, t, mgCaptureRenew, s, after, c, in.leaseMS, now)
}

// A checkpoint stores native captured progress scalars on the exact live claim.
// It never writes checkpoint_json, never retires the writer, never sets an exit
// launch or physical-exit sequence and never accepts a caller boolean or
// callback as physical evidence. Progress is monotone; a lost/unknown callback
// is resolved by replaying the ORIGINAL command, not by trusting a later one.
func mgCheckpointCapture(ctx context.Context, t *commandTurn, s mgCaptureRow, c mgCaptureCommand, in mgCaptureCheckpointRequest) (out mgCaptureFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err := in.validate(); err != nil {
		return out, err
	}
	if !c.next(s) || !s.valid() || !s.ownerIs(in.owner) || s.revision != in.expected {
		return out, ErrConflict
	}
	if !s.capturing() || !s.unshared() || s.physicalExitSequence != 0 || !s.claimIs(in.claimGeneration, in.token, in.ownerInstance, in.launch) || !s.allocationIs(mgCaptureAllocationIdentity{in.allocation.id, in.allocation.token, in.allocation.generation}) {
		return out, ErrConflict
	}
	if s.sourceRevision < 1 || !mgCaptureFence(s.sourceFence) || !s.progressAdvances(in) {
		return out, ErrConflict
	}
	now, err := mgCaptureTime(ctx, t)
	if err != nil {
		return out, err
	}
	if s.leaseMS <= now {
		return out, ErrConflict
	}
	if err = mgCaptureMatch(ctx, t, s); err != nil {
		return out, err
	}
	after := s
	if in.capturedStartMS != 0 {
		after.capturedStartMS = in.capturedStartMS
	}
	after.capturedEndMS = in.capturedEndMS
	after.gapCount = in.gapCount
	after.producerSucceeded = mgCaptureSticky(s.producerSucceeded, in.producerSucceeded)
	after.boundaryReached = mgCaptureSticky(s.boundaryReached, in.boundaryReached)
	after.revision++
	after.lastSequence++
	after.updatedMS = now
	return mgCaptureWrite(ctx, t, mgCaptureCheckpoint, s, after, c,
		after.capturedStartMS, after.capturedEndMS, after.gapCount, after.producerSucceeded,
		after.boundaryReached, now)
}

func mgCaptureSticky(old int64, value bool) int64 {
	if old == 1 || value {
		return 1
	}
	return 0
}
