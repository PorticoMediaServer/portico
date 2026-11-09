package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Bodies only on the existing admitted commandTurn. Frozen owner/source/
// programme values are NOT authority: the accepting frame resolves the current
// US owner session and original receipt first, and W owns admission, events,
// quota and COMMIT. No provider, tuner, decoder, catalogue or filesystem IO
// happens inside these holds, and no physical custody is acquired or released.
func mgRecordingIntentStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgRecordingIntentRead, mgRecordingIntentScope, mgRecordingIntentReservationRead, mgRecordingIntentOwnerRevisionRead, mgRecordingIntentFloorRead, mgRecordingIntentInsert, mgRecordingIntentUpdate, mgRecordingIntentPromote, mgRecordingIntentCancel, mgRecordingIntentKeep, mgRecordingIntentReserveInsert, mgRecordingIntentReserveUpdate, mgRecordingIntentReserveDisable, mgRecordingIntentDependencyInsert, mgRecordingIntentDependencyDelete, mgRecordingIntentOwnerRevisionInsert, mgRecordingIntentOwnerRevisionUpdate:
	default:
		return statementStep{}, ErrInvalid
	}
	s, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return s.statementStep, nil
}

func mgRecordingIntentQuery(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := mgRecordingIntentStep(t, id)
	if err != nil {
		return commandRow{}, err
	}
	return t.queryRow(ctx, step, args...)
}

func mgRecordingIntentChange(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, err := mgRecordingIntentStep(t, id)
	if err != nil {
		return err
	}
	result, err := t.exec(ctx, step, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}

// The bounded dependency cleanup is a legitimate zero-row no-op when another
// intent still owns the source reference; more than one row is impossible for
// the exact (source,kind,id) key and is refused.
func mgRecordingIntentOptionalChange(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, err := mgRecordingIntentStep(t, id)
	if err != nil {
		return err
	}
	result, err := t.exec(ctx, step, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n < 0 || n > 1 {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}

func scanMGRecordingIntent(row commandRow) (out mgRecordingIntentRow, err error) {
	err = row.Scan(&out.id, &out.ownerKey, &out.authority, &out.account, &out.profile, &out.source, &out.channel, &out.programme,
		&out.generation, &out.ruleID, &out.ruleRevision, &out.startMS, &out.endMS, &out.revision, &out.state, &out.reason,
		&out.keep, &out.cancelGeneration, &out.claimGeneration, &out.token, &out.nextAttemptMS, &out.recoveryAttempt,
		&out.leaseMS, &out.ownerInstance, &out.allocationID, &out.itemID, &out.artifactID, &out.artifactDigest, &out.shares,
		&out.shareLeader, &out.incarnation, &out.operation, &out.birth, &out.member, &out.lifetime, &out.lastSequence,
		&out.ackSequence, &out.guidePin, &out.launch, &out.allocationGeneration, &out.allocationToken, &out.sourceRevision,
		&out.sourceFence, &out.writerRetiredSequence, &out.physicalExitSequence, &out.sharedReadySequence, &out.createdMS,
		&out.updatedMS, &out.finishedMS, &out.programmeJSON, &out.optionsJSON, &out.manual, &out.priority, &out.hasImage, &out.image)
	if err != nil {
		return mgRecordingIntentRow{}, err
	}
	if !out.valid() {
		return mgRecordingIntentRow{}, ErrInvalid
	}
	return out, nil
}

func mgReadRecordingIntent(ctx context.Context, t *commandTurn, id string) (out mgRecordingIntentRow, found bool, err error) {
	defer func() {
		value, failure := finishCommand(ctx, commandLifetime(t), struct {
			row   mgRecordingIntentRow
			found bool
		}{out, found}, err)
		out, found, err = value.row, value.found, failure
	}()
	if !mgRecordingHex(id, 64) {
		return out, false, ErrInvalid
	}
	row, err := mgRecordingIntentQuery(ctx, t, mgRecordingIntentRead, id)
	if err != nil {
		return out, false, err
	}
	out, err = scanMGRecordingIntent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return mgRecordingIntentRow{}, false, nil
	}
	if err != nil {
		return mgRecordingIntentRow{}, false, err
	}
	return out, true, nil
}

func mgRecordingMatch(ctx context.Context, t *commandTurn, want mgRecordingIntentRow) error {
	if !want.valid() {
		return ErrInvalid
	}
	got, found, err := mgReadRecordingIntent(ctx, t, want.id)
	if err != nil {
		return err
	}
	if !found || got != want {
		return ErrConflict
	}
	return nil
}

// The parent-selected head must be the source's current published generation,
// the source must still be active and unremoved, and the programme row must be
// byte-identical to the frozen current copy. A stale head, a disabled or
// removed source, a replaced guide and changed metadata are all refusals with
// no effects.
func mgRecordingCheckProgramme(ctx context.Context, t *commandTurn, scope mgRecordingScope, f mgRecordingFrozen) error {
	if !scope.valid() || !f.valid() || scope.programme != f.programme.id || scope.channel != f.programme.channel {
		return ErrInvalid
	}
	row, err := mgRecordingIntentQuery(ctx, t, mgRecordingIntentScope, scope.generation, scope.channel, scope.programme, scope.source)
	if err != nil {
		return err
	}
	var revision, removed int64
	var state, head string
	var actual mgRecordingProgramme
	err = row.Scan(&revision, &state, &head, &removed, &actual.id, &actual.channel, &actual.provider, &actual.title, &actual.start, &actual.end, &actual.lineage, &actual.metadata, &actual.series, &actual.episode, &actual.newEvidence, &actual.description, &actual.facts, &actual.hasIcon, &actual.icon)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if revision != scope.revision || head != scope.head || state != "active" || removed != 0 || actual != f.programme {
		return ErrConflict
	}
	return nil
}

func mgRecordingReadReservation(ctx context.Context, t *commandTurn, id string) (out mgRecordingReservation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mgRecordingIntentQuery(ctx, t, mgRecordingIntentReservationRead, id)
	if err != nil {
		return out, err
	}
	var r mgRecordingReservation
	err = row.Scan(&r.source, &r.startMS, &r.endMS, &r.priority, &r.enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return mgRecordingReservation{}, nil
	}
	if err != nil {
		return mgRecordingReservation{}, err
	}
	if !mgRecordingHex(r.source, 48) || r.startMS < 0 || r.endMS <= r.startMS || r.priority < -1000 || r.priority > 1000 || (r.enabled != 0 && r.enabled != 1) {
		return mgRecordingReservation{}, ErrInvalid
	}
	r.present = true
	return r, nil
}

func mgRecordingOwnerRevision(ctx context.Context, t *commandTurn, owner string) (int64, error) {
	row, err := mgRecordingIntentQuery(ctx, t, mgRecordingIntentOwnerRevisionRead, owner)
	if err != nil {
		return 0, err
	}
	var revision int64
	err = row.Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if revision < 1 {
		return 0, ErrInvalid
	}
	return revision, nil
}

// One sparse per-owner revision advance: insert 1 when the owner has no row yet,
// otherwise exact old-value CAS. It is the owner's intent epoch, never a
// capability or a physical byte refund.
func mgRecordingTouchOwner(ctx context.Context, t *commandTurn, owner string) (int64, error) {
	before, err := mgRecordingOwnerRevision(ctx, t, owner)
	if err != nil {
		return 0, err
	}
	if before == math.MaxInt64 {
		return 0, ErrConflict
	}
	if before == 0 {
		err = mgRecordingIntentChange(ctx, t, mgRecordingIntentOwnerRevisionInsert, owner, int64(1))
	} else {
		err = mgRecordingIntentChange(ctx, t, mgRecordingIntentOwnerRevisionUpdate, owner, before)
	}
	if err != nil {
		return 0, err
	}
	after, err := mgRecordingOwnerRevision(ctx, t, owner)
	if err != nil {
		return 0, err
	}
	if after != before+1 {
		return 0, ErrConflict
	}
	return after, nil
}

// Exact readback of the complete row, its reservation window and the owner
// revision after the effect. Any divergence is a whole-frame rollback.
func mgRecordingReadback(ctx context.Context, t *commandTurn, want mgRecordingIntentRow, reservation mgRecordingReservation, ownerRevision int64) (out mgRecordingIntentFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	got, found, err := mgReadRecordingIntent(ctx, t, want.id)
	if err != nil {
		return out, err
	}
	if !found || got != want {
		return out, ErrConflict
	}
	actual, err := mgRecordingReadReservation(ctx, t, want.id)
	if err != nil {
		return out, err
	}
	if actual != reservation {
		return out, ErrConflict
	}
	revision, err := mgRecordingOwnerRevision(ctx, t, want.ownerKey)
	if err != nil {
		return out, err
	}
	if revision != ownerRevision || revision < 1 {
		return out, ErrConflict
	}
	return mgRecordingIntentFacts{row: got, reservation: actual, ownerRevision: revision}, nil
}

// Fresh manual external-guide intent with explicit resolved options only. The
// birth namespace is the accepted frame's own command identity, so a forged or
// replayed birth can never be inserted here. For UseDefaults the parent must
// load and CAS the defaults on this same turn before freezing them; this body
// cannot validate an outside-turn defaults snapshot.
func mgScheduleRecording(ctx context.Context, t *commandTurn, owner mgRecordingOwner, scope mgRecordingScope, f mgRecordingFrozen, birth mgRecordingBirth) (out mgRecordingIntentFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !birth.valid() || !scope.valid() || scope.generation != scope.head || !f.valid() {
		return out, ErrInvalid
	}
	if err = mgRecordingCheckOwner(ctx, t, owner); err != nil {
		return out, err
	}
	if err = mgRecordingCheckProgramme(ctx, t, scope, f); err != nil {
		return out, err
	}
	now, err := mgPersonalNow(ctx, t)
	if err != nil {
		return out, err
	}
	nowMS := now.UnixMilli()
	if f.endMS <= nowMS {
		return out, ErrInvalid
	}
	id := mgRecordingID(owner, scope.source, scope.programme)
	row, err := mgRecordingIntentQuery(ctx, t, mgRecordingIntentFloorRead, id)
	if err != nil {
		return out, err
	}
	var retired string
	floorErr := row.Scan(&retired)
	if floorErr == nil {
		// A retired birth namespace is never resurrected, even by its owner.
		return out, ErrConflict
	}
	if !errors.Is(floorErr, sql.ErrNoRows) {
		return out, floorErr
	}
	existing, found, err := mgReadRecordingIntent(ctx, t, id)
	if err != nil {
		return out, err
	}
	if found {
		n := existing
		if !n.ownerMatches(owner) || n.source != scope.source || n.programme != scope.programme || n.channel != scope.channel ||
			n.state == "cancelled" || n.state == "failed" || n.state == "deleted" || n.state == "pending-delete" {
			return out, ErrConflict
		}
		pending := n.state == "scheduled" || n.state == "conflicted" || n.state == "waiting-source" || n.state == "waiting-guide"
		// Explicit manual adoption changes only rule intent. It cannot change an
		// attached occurrence's window, reservation, pin or physical custody, and
		// an already-manual duplicate identity is a conflict, never a silent
		// second schedule.
		if !pending || n.ruleID == "" {
			return out, ErrConflict
		}
		if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentPromote, append([]any{nowMS}, n.fenceValues()...)...); err != nil {
			return out, err
		}
		want := n
		want.manual, want.ruleID, want.ruleRevision = 1, "", 0
		want.revision++
		want.updatedMS = nowMS
		reservation, err := mgRecordingReadReservation(ctx, t, id)
		if err != nil {
			return out, err
		}
		revision, err := mgRecordingTouchOwner(ctx, t, n.ownerKey)
		if err != nil {
			return out, err
		}
		return mgRecordingReadback(ctx, t, want, reservation, revision)
	}
	n := mgRecordingIntentRow{
		id: id, ownerKey: owner.key(), authority: "local", account: owner.account, profile: owner.profile,
		source: scope.source, channel: scope.channel, programme: scope.programme, generation: scope.generation,
		startMS: f.startMS, endMS: f.endMS, revision: 1, state: "scheduled", priority: f.options.Priority, manual: 1,
		createdMS: nowMS, updatedMS: nowMS, incarnation: birth.incarnation, operation: birth.operation, birth: birth.digest,
		member: owner.memberIncarnation, lifetime: owner.profileLifetime,
		guidePin:      mgRecordingNullableText{text: scope.generation, valid: true},
		programmeJSON: f.programmeJSON, optionsJSON: f.optionsJSON,
	}
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentInsert, id, n.ownerKey, "local", owner.account, owner.profile, scope.source, scope.channel, scope.programme, scope.generation, f.programmeJSON, int64(1), f.optionsJSON, f.startMS, f.endMS, f.options.Priority, int64(1), "scheduled", nowMS, nowMS, birth.incarnation[:], birth.operation[:], birth.digest[:], owner.memberIncarnation, owner.profileLifetime, scope.generation); err != nil {
		return out, err
	}
	reservation := mgRecordingReservation{true, scope.source, f.startMS, f.endMS, f.options.Priority, 1}
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentReserveInsert, id, scope.source, f.startMS, f.endMS, f.options.Priority, int64(1)); err != nil {
		return out, err
	}
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentDependencyInsert, scope.source, "recording", id); err != nil {
		return out, err
	}
	revision, err := mgRecordingTouchOwner(ctx, t, n.ownerKey)
	if err != nil {
		return out, err
	}
	return mgRecordingReadback(ctx, t, n, reservation, revision)
}

// The copied programme is immutable. An option edit compares the historical
// pinned occurrence while independently re-checking the current source
// revision/head and the exact current programme facts. A waiting-source,
// waiting-guide or conflicted row is still editable: the legacy reconcile path
// only disabled its reservation, so either observed enabled value is accepted
// and the edit re-enables that same window.
func mgUpdateRecording(ctx context.Context, t *commandTurn, owner mgRecordingOwner, id string, expected int64, scope mgRecordingScope, f mgRecordingFrozen) (out mgRecordingIntentFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !scope.valid() || scope.generation != scope.head || !f.valid() || !mgRecordingHex(id, 64) || expected < 1 {
		return out, ErrInvalid
	}
	if err = mgRecordingCheckOwner(ctx, t, owner); err != nil {
		return out, err
	}
	before, found, err := mgReadRecordingIntent(ctx, t, id)
	if err != nil {
		return out, err
	}
	if !found {
		return out, ErrConflict
	}
	if !before.editable() || !before.ownerMatches(owner) || before.revision != expected || f.programmeJSON != before.programmeJSON ||
		scope.source != before.source || scope.generation != before.generation || scope.channel != before.channel || scope.programme != before.programme {
		return out, ErrConflict
	}
	if err = mgRecordingCheckProgramme(ctx, t, scope, f); err != nil {
		return out, err
	}
	if err = mgRecordingMatch(ctx, t, before); err != nil {
		return out, err
	}
	now, err := mgPersonalNow(ctx, t)
	if err != nil {
		return out, err
	}
	nowMS := now.UnixMilli()
	if f.endMS <= nowMS {
		return out, ErrInvalid
	}
	reservation, err := mgRecordingReadReservation(ctx, t, id)
	if err != nil {
		return out, err
	}
	// The legacy reconcile path moves a waiting-source, waiting-guide or
	// conflicted row to that state and only disables its reservation
	// (enabled=0), so an owner can still edit it back to scheduled. Either
	// observed enabled value is admitted; the reserve-update CAS below stays on
	// the exact value read. A missing row, or a window/source/priority that no
	// longer matches the intent, is not this intent's window and refuses.
	if !reservation.present || reservation.source != before.source || reservation.startMS != before.startMS || reservation.endMS != before.endMS || reservation.priority != before.priority {
		return out, ErrConflict
	}
	args := append([]any{f.optionsJSON, f.startMS, f.endMS, f.options.Priority, nowMS}, before.fenceValues()...)
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentUpdate, args...); err != nil {
		return out, err
	}
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentReserveUpdate, f.startMS, f.endMS, f.options.Priority, id, reservation.source, reservation.startMS, reservation.endMS, reservation.priority, reservation.enabled); err != nil {
		return out, err
	}
	want := before
	want.optionsJSON, want.priority, want.manual = f.optionsJSON, f.options.Priority, 1
	want.ruleID, want.ruleRevision, want.state, want.reason = "", 0, "scheduled", ""
	want.startMS, want.endMS = f.startMS, f.endMS
	want.revision++
	want.updatedMS = nowMS
	reservation.startMS, reservation.endMS, reservation.priority, reservation.enabled = f.startMS, f.endMS, f.options.Priority, 1
	revision, err := mgRecordingTouchOwner(ctx, t, before.ownerKey)
	if err != nil {
		return out, err
	}
	return mgRecordingReadback(ctx, t, want, reservation, revision)
}

// Cancellation is owner/revision custody of the logical intent. It always
// advances the cancel generation, keeps the guide pin, allocation, artifact,
// share and capture namespace untouched, and never refunds a physical byte.
// An already-cancelled intent is a truthful no-op at the same revision. A
// waiting-source, waiting-guide or conflicted intent stays cancellable after a
// source or guide replacement: its disabled reservation (enabled=0) is this
// intent's same window, not a reason to refuse.
func mgCancelRecording(ctx context.Context, t *commandTurn, owner mgRecordingOwner, id string, expected int64) (out mgRecordingIntentFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgRecordingHex(id, 64) || expected < 1 {
		return out, ErrInvalid
	}
	if err = mgRecordingCheckOwner(ctx, t, owner); err != nil {
		return out, err
	}
	before, found, err := mgReadRecordingIntent(ctx, t, id)
	if err != nil {
		return out, err
	}
	if !found || !before.ownerMatches(owner) || before.revision != expected {
		return out, ErrConflict
	}
	if err = mgRecordingMatch(ctx, t, before); err != nil {
		return out, err
	}
	reservation, err := mgRecordingReadReservation(ctx, t, id)
	if err != nil {
		return out, err
	}
	if before.state == "cancelled" {
		revision, err := mgRecordingOwnerRevision(ctx, t, before.ownerKey)
		if err != nil {
			return out, err
		}
		return mgRecordingReadback(ctx, t, before, reservation, revision)
	}
	switch before.state {
	case "completed", "incomplete-playable", "failed", "pending-delete", "deleted":
		return out, ErrConflict
	}
	if before.cancelGeneration == math.MaxInt64 {
		return out, ErrConflict
	}
	// A waiting-source, waiting-guide or conflicted row still names this
	// intent's exclusive window even when the legacy reconcile path only
	// disabled it (enabled=0), so either observed enabled value is admitted and
	// the reserve-disable CAS below stays on the exact value read. A missing
	// row, or a window/source/priority that no longer matches the intent, is a
	// different window and refuses before any effect.
	if !reservation.present || reservation.source != before.source || reservation.startMS != before.startMS || reservation.endMS != before.endMS || reservation.priority != before.priority {
		return out, ErrConflict
	}
	now, err := mgPersonalNow(ctx, t)
	if err != nil {
		return out, err
	}
	nowMS := now.UnixMilli()
	args := append([]any{nowMS, nowMS}, before.fenceValues()...)
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentCancel, args...); err != nil {
		return out, err
	}
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentReserveDisable, id, reservation.source, reservation.startMS, reservation.endMS, reservation.priority, reservation.enabled); err != nil {
		return out, err
	}
	// Disable the conflict window, then drop only the source dependency this
	// cancelled occurrence still owns. Any artifact/item intent keeps it.
	if before.artifactID == "" && before.itemID == 0 {
		if err = mgRecordingIntentOptionalChange(ctx, t, mgRecordingIntentDependencyDelete, before.source, id); err != nil {
			return out, err
		}
	}
	want := before
	want.state, want.reason = "cancelled", "owner-cancelled"
	want.cancelGeneration++
	want.revision++
	want.updatedMS, want.finishedMS = nowMS, nowMS
	reservation.enabled = 0
	revision, err := mgRecordingTouchOwner(ctx, t, before.ownerKey)
	if err != nil {
		return out, err
	}
	return mgRecordingReadback(ctx, t, want, reservation, revision)
}

// Keep is retention intent, allowed after provider removal and after cancel. It
// neither acquires guide/capture authority nor touches a reservation,
// allocation, pin or capture namespace.
func mgKeepRecording(ctx context.Context, t *commandTurn, owner mgRecordingOwner, id string, expected int64, keep bool) (out mgRecordingIntentFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgRecordingHex(id, 64) || expected < 1 {
		return out, ErrInvalid
	}
	if err = mgRecordingCheckOwner(ctx, t, owner); err != nil {
		return out, err
	}
	before, found, err := mgReadRecordingIntent(ctx, t, id)
	if err != nil {
		return out, err
	}
	if !found || !before.ownerMatches(owner) || before.revision != expected || before.state == "pending-delete" || before.state == "deleted" {
		return out, ErrConflict
	}
	if err = mgRecordingMatch(ctx, t, before); err != nil {
		return out, err
	}
	now, err := mgPersonalNow(ctx, t)
	if err != nil {
		return out, err
	}
	nowMS := now.UnixMilli()
	value := int64(0)
	if keep {
		value = 1
	}
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentKeep, append([]any{value, nowMS}, before.fenceValues()...)...); err != nil {
		return out, err
	}
	want := before
	want.keep = value
	want.revision++
	want.updatedMS = nowMS
	reservation, err := mgRecordingReadReservation(ctx, t, id)
	if err != nil {
		return out, err
	}
	revision, err := mgRecordingTouchOwner(ctx, t, before.ownerKey)
	if err != nil {
		return out, err
	}
	return mgRecordingReadback(ctx, t, want, reservation, revision)
}
