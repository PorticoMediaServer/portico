package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func mgRecordingIntentStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgRecordingIntentRead, mgRecordingIntentScope, mgRecordingIntentOwner, mgRecordingIntentReservationRead, mgRecordingIntentOwnerRevisionRead, mgRecordingIntentFloorRead, mgRecordingIntentInsert, mgRecordingIntentUpdate, mgRecordingIntentPromote, mgRecordingIntentKeep, mgRecordingIntentReserveInsert, mgRecordingIntentReserveUpdate, mgRecordingIntentDependencyInsert, mgRecordingIntentOwnerRevisionInsert, mgRecordingIntentOwnerRevisionUpdate:
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
func mgReadRecordingIntent(ctx context.Context, t *commandTurn, id string) (out mgRecordingSnapshot, found bool, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
		if err != nil {
			found = false
		}
	}()
	if !mgRecordingHex(id, 64) {
		return out, false, ErrInvalid
	}
	row, err := mgRecordingIntentQuery(ctx, t, mgRecordingIntentRead, id)
	if err != nil {
		return out, false, err
	}
	n := &out.capture
	targets := append([]any{&n.id, &n.ownerKey, &n.authority, &n.accountID, &n.profileID, &n.sourceID, &n.channelID, &n.programmeID, &n.guideGeneration, &n.ruleID, &n.ruleRevision, &n.startMS, &n.endMS, &n.revision, &n.nextAttemptMS, &n.recoveryAttempt, &n.hasShare, &n.updatedMS, &n.finishedMS, &n.incarnation, &n.createOperation, &n.birthDigest, &n.memberIncarnation, &n.profileLifetime, &n.lastSequence, &n.ackSequence}, mgCaptureSavedTargets(&n.mgCaptureSavedResult)...)
	targets = append(targets, &out.programmeJSON, &out.optionsJSON, &out.manual, &out.priority, &out.keep, &out.createdMS)
	err = row.Scan(targets...)
	if errors.Is(err, sql.ErrNoRows) {
		return mgRecordingSnapshot{}, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if !out.valid() {
		return out, false, ErrInvalid
	}
	return out, true, nil
}
func mgRecordingMatch(ctx context.Context, t *commandTurn, want mgRecordingSnapshot) error {
	if !want.valid() {
		return ErrInvalid
	}
	got, found, err := mgReadRecordingIntent(ctx, t, want.capture.id)
	if err != nil {
		return err
	}
	if !found || got != want {
		return ErrConflict
	}
	return nil
}
func mgRecordingCheckOwner(ctx context.Context, t *commandTurn, o mgRecordingOwner) error {
	if !o.valid() {
		return ErrInvalid
	}
	row, err := mgRecordingIntentQuery(ctx, t, mgRecordingIntentOwner, o.profile, o.account)
	if err != nil {
		return err
	}
	var epoch, memberRevision, disabled, profileRevision, deleted int64
	var incarnation, lifetime string
	err = row.Scan(&epoch, &incarnation, &memberRevision, &disabled, &lifetime, &profileRevision, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisclosureDenied
	}
	if err != nil {
		return err
	}
	if epoch != o.accountEpoch || incarnation != o.memberIncarnation || memberRevision != o.memberRevision || disabled != 0 || lifetime != o.profileLifetime || profileRevision != o.profileRevision || deleted != 0 {
		return ErrDisclosureDenied
	}
	return nil
}
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
func mgRecordingReadReservation(ctx context.Context, t *commandTurn, id string) (mgRecordingReservation, error) {
	row, err := mgRecordingIntentQuery(ctx, t, mgRecordingIntentReservationRead, id)
	if err != nil {
		return mgRecordingReservation{}, err
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
func (s mgRecordingSnapshot) fenceValues() []any {
	n := s.capture
	return []any{n.id, n.ownerKey, n.accountID, n.profileID, n.revision, n.incarnation[:], n.memberIncarnation, n.profileLifetime, n.lastSequence, n.ackSequence, n.state, n.token, n.claimGeneration, n.cancelGeneration, n.ownerInstance, n.leaseMS}
}
func mgRecordingReadback(ctx context.Context, t *commandTurn, want mgRecordingSnapshot, reservation mgRecordingReservation, ownerRevision int64) (mgRecordingIntentFacts, error) {
	got, found, err := mgReadRecordingIntent(ctx, t, want.capture.id)
	if err != nil {
		return mgRecordingIntentFacts{}, err
	}
	if !found || got != want {
		return mgRecordingIntentFacts{}, ErrConflict
	}
	actual, err := mgRecordingReadReservation(ctx, t, want.capture.id)
	if err != nil {
		return mgRecordingIntentFacts{}, err
	}
	if actual != reservation {
		return mgRecordingIntentFacts{}, ErrConflict
	}
	revision, err := mgRecordingOwnerRevision(ctx, t, want.capture.ownerKey)
	if err != nil {
		return mgRecordingIntentFacts{}, err
	}
	if revision != ownerRevision {
		return mgRecordingIntentFacts{}, ErrConflict
	}
	return mgRecordingIntentFacts{got, actual, revision}, nil
}

// Manual external-guide intent with explicit resolved options only. For
// UseDefaults, parent must load/CAS the defaults on this same commandTurn before
// freezing them; this body cannot validate an outside-turn defaults snapshot.
// Fresh auth/rating checks and original public receipt resolution must
// precede this body on the same turn; no field here proves those gates passed.
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
	now, err := mgCaptureNow(ctx, t)
	if err != nil {
		return out, err
	}
	if f.endMS <= now {
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
		// Parent must freshly disclose this ACTUAL existing copied programme, whose
		// rating/metadata can differ from today's guide; the new guide is no grant.
		n := existing.capture
		if !existing.ownerMatches(owner) || n.sourceID != scope.source || n.programmeID != scope.programme || n.channelID != scope.channel || n.state == "cancelled" || n.state == "failed" || n.state == "deleted" || n.state == "pending-delete" {
			return out, ErrConflict
		}
		reservation, err := mgRecordingReadReservation(ctx, t, id)
		if err != nil {
			return out, err
		}
		revision, err := mgRecordingOwnerRevision(ctx, t, n.ownerKey)
		if err != nil {
			return out, err
		}
		pending := n.state == "scheduled" || n.state == "conflicted" || n.state == "waiting-source" || n.state == "waiting-guide"
		if pending && n.ruleID != "" {
			// Explicit manual adoption changes only rule intent. It cannot change an
			// attached capture's window, reservation, pin or physical custody.
			if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentPromote, append([]any{now}, existing.fenceValues()...)...); err != nil {
				return out, err
			}
			existing.manual = 1
			existing.capture.ruleID = ""
			existing.capture.ruleRevision = 0
			existing.capture.revision++
			existing.capture.updatedMS = now
			revision, err = mgRecordingTouchOwner(ctx, t, n.ownerKey)
			if err != nil {
				return out, err
			}
		}
		return mgRecordingReadback(ctx, t, existing, reservation, revision)
	}
	n := mgCaptureSnapshot{id: id, ownerKey: owner.key(), authority: "local", accountID: owner.account, profileID: owner.profile, sourceID: scope.source, channelID: scope.channel, programmeID: scope.programme, guideGeneration: scope.generation, startMS: f.startMS, endMS: f.endMS, revision: 1, updatedMS: now, incarnation: birth.incarnation, createOperation: birth.operation, birthDigest: birth.digest, memberIncarnation: owner.memberIncarnation, profileLifetime: owner.profileLifetime}
	n.state = "scheduled"
	n.guidePin = mgCaptureNullableText{text: scope.generation, valid: true}
	want := mgRecordingSnapshot{n, f.programmeJSON, f.optionsJSON, 1, f.options.Priority, 0, now}
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentInsert, id, n.ownerKey, "local", owner.account, owner.profile, scope.source, scope.channel, scope.programme, scope.generation, f.programmeJSON, int64(1), f.optionsJSON, f.startMS, f.endMS, f.options.Priority, int64(1), "scheduled", now, now, birth.incarnation[:], birth.operation[:], birth.digest[:], owner.memberIncarnation, owner.profileLifetime, scope.generation); err != nil {
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
	return mgRecordingReadback(ctx, t, want, reservation, revision)
}

// The copied programme is immutable. An option edit uses its historical pinned
// occurrence while comparing the current source head/revision independently.
func mgUpdateRecording(ctx context.Context, t *commandTurn, owner mgRecordingOwner, before mgRecordingSnapshot, scope mgRecordingScope, f mgRecordingFrozen) (out mgRecordingIntentFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	n := before.capture
	if !before.valid() || !before.editable() || !before.ownerMatches(owner) || !mgRecordingRevisionValid(n.revision) || !f.valid() || f.programmeJSON != before.programmeJSON || scope.source != n.sourceID || scope.generation != n.guideGeneration || scope.channel != n.channelID || scope.programme != n.programmeID {
		return out, ErrConflict
	}
	if err = mgRecordingCheckOwner(ctx, t, owner); err != nil {
		return out, err
	}
	if err = mgRecordingCheckProgramme(ctx, t, scope, f); err != nil {
		return out, err
	}
	if err = mgRecordingMatch(ctx, t, before); err != nil {
		return out, err
	}
	now, err := mgCaptureNow(ctx, t)
	if err != nil {
		return out, err
	}
	if f.endMS <= now {
		return out, ErrInvalid
	}
	reservation, err := mgRecordingReadReservation(ctx, t, n.id)
	if err != nil {
		return out, err
	}
	if !reservation.present || reservation.source != n.sourceID || reservation.startMS != n.startMS || reservation.endMS != n.endMS || reservation.priority != before.priority {
		return out, ErrConflict
	}
	args := append([]any{f.optionsJSON, f.startMS, f.endMS, f.options.Priority, now}, before.fenceValues()...)
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentUpdate, args...); err != nil {
		return out, err
	}
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentReserveUpdate, f.startMS, f.endMS, f.options.Priority, n.id, reservation.source, reservation.startMS, reservation.endMS, reservation.priority, reservation.enabled); err != nil {
		return out, err
	}
	want := before
	want.optionsJSON = f.optionsJSON
	want.priority = f.options.Priority
	want.manual = 1
	want.capture.ruleID = ""
	want.capture.ruleRevision = 0
	want.capture.startMS = f.startMS
	want.capture.endMS = f.endMS
	want.capture.state = "scheduled"
	want.capture.reason = ""
	want.capture.revision++
	want.capture.updatedMS = now
	reservation.startMS, reservation.endMS, reservation.priority, reservation.enabled = f.startMS, f.endMS, f.options.Priority, 1
	revision, err := mgRecordingTouchOwner(ctx, t, n.ownerKey)
	if err != nil {
		return out, err
	}
	return mgRecordingReadback(ctx, t, want, reservation, revision)
}

// Keep is retention intent, allowed after provider removal. It neither acquires
// guide/capture authority nor touches a reservation/allocation/pin/namespace.
func mgKeepRecording(ctx context.Context, t *commandTurn, owner mgRecordingOwner, before mgRecordingSnapshot, keep bool) (out mgRecordingIntentFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	n := before.capture
	if !before.valid() || !before.ownerMatches(owner) || !mgRecordingRevisionValid(n.revision) || n.state == "pending-delete" || n.state == "deleted" {
		return out, ErrConflict
	}
	if err = mgRecordingCheckOwner(ctx, t, owner); err != nil {
		return out, err
	}
	if err = mgRecordingMatch(ctx, t, before); err != nil {
		return out, err
	}
	now, err := mgCaptureNow(ctx, t)
	if err != nil {
		return out, err
	}
	reservation, err := mgRecordingReadReservation(ctx, t, n.id)
	if err != nil {
		return out, err
	}
	value := int64(0)
	if keep {
		value = 1
	}
	if err = mgRecordingIntentChange(ctx, t, mgRecordingIntentKeep, append([]any{value, now}, before.fenceValues()...)...); err != nil {
		return out, err
	}
	want := before
	want.keep = value
	want.capture.revision++
	want.capture.updatedMS = now
	revision, err := mgRecordingTouchOwner(ctx, t, n.ownerKey)
	if err != nil {
		return out, err
	}
	return mgRecordingReadback(ctx, t, want, reservation, revision)
}
