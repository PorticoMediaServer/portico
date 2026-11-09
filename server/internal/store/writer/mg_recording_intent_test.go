package writer

import (
	"errors"
	"testing"
	"time"
)

// Fresh schedule persists one occurrence, its enabled reservation window, its
// source dependency and the owner revision, then replays its original scalar.
// The effect starts no capture: every capture namespace column stays empty.
func TestMGRecordingScheduleAcceptReplayAndRetired(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v)
	identity := mgPersonalIdentity(1, 1)
	request := v.schedule()
	reference := mgRecordingScheduleReference(t, identity, request)

	turn := mgRecordingTurn(t, w, MGScheduleRecording)
	fresh, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	want := mgRecordingExpectedResult(t, v, v.scope, v.programme, v.options(), "scheduled", 1, 0, 1, 1)
	if err != nil || fresh.decision != receiptFresh || fresh.result != want || fresh.digest == (ResultDigest{}) {
		t.Fatal(fresh, err)
	}
	mgPersonalCommit(t, turn)

	recordings, reservations, dependencies, receipts, sequence := mgRecordingCounts(t, w, v.root)
	if recordings != 1 || reservations != 1 || dependencies != 1 || receipts != 1 || sequence != 1 {
		t.Fatal(recordings, reservations, dependencies, receipts, sequence)
	}
	// A schedule is authored intent only: no tuner allocation, claim, launch,
	// artifact, share or capture sequence was created.
	var allocations, claimed, launched, artifacts int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_allocations),(SELECT count(*) FROM dvr_recordings WHERE claim_generation<>0 OR claim_token<>'' OR owner_instance<>'' OR lease_until_ms<>0),(SELECT count(*) FROM dvr_recordings WHERE capture_launch_id IS NOT NULL),(SELECT count(*) FROM dvr_recordings WHERE artifact_id<>'' OR item_id<>0 OR has_dvr_recording_shares<>0)`, v.root).Scan(&allocations, &claimed, &launched, &artifacts); err != nil {
		t.Fatal(err)
	}
	if allocations != 0 || claimed != 0 || launched != 0 || artifacts != 0 {
		t.Fatal("schedule acquired physical custody", allocations, claimed, launched, artifacts)
	}
	var enabled, revision, pin, state int64
	var guidePin string
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT enabled FROM live_reservations WHERE id=?),(SELECT revision FROM dvr_recordings WHERE id=?),(SELECT count(*) FROM dvr_recordings WHERE id=? AND guide_pin_generation=guide_generation),(SELECT count(*) FROM dvr_recordings WHERE id=? AND state='scheduled'),(SELECT guide_pin_generation FROM dvr_recordings WHERE id=?)`, want.id, want.id, want.id, want.id, want.id).Scan(&enabled, &revision, &pin, &state, &guidePin); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 || revision != 1 || pin != 1 || state != 1 || guidePin != v.scope.generation {
		t.Fatal(enabled, revision, pin, state, guidePin)
	}

	// Identical retry: the original saved scalar is returned with no new row,
	// reservation, dependency, receipt or owner advance.
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	replay, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || replay.decision != receiptReplay || replay.result != want || replay.digest != fresh.digest {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A tampered retained payload is never decoded as the original result.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_payload=substr(result_payload,1,length(result_payload)-1) WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrResolveRequired) || got != (mgRecordingAcceptance{}) {
		t.Fatal("tampered payload replayed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A different input under the same identity, or a caller digest that
	// disagrees with the typed request, conflicts before any effect.
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	changed := request
	changed.options.Priority = 6
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), reference, changed); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("changed input under one identity", got, err)
	}
	bad := reference
	bad.RequestDigest[0] ^= 0xff
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), bad, request); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("caller digest not recomputed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// ACK retires the original: a complete zero result, never a reconstructed
	// success and never a fresh effect.
	ackMGRecording(t, w, v.root, 1)
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	retired, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || retired.decision != receiptRetired || retired.result != (mgRecordingIntentResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal(retired, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if recordings, reservations, dependencies, receipts, sequence := mgRecordingCounts(t, w, v.root); recordings != 1 || reservations != 1 || dependencies != 1 || receipts != 1 || sequence != 1 {
		t.Fatal("retired replay changed custody", recordings, reservations, dependencies, receipts, sequence)
	}
}

// An option edit moves exactly the occurrence window and its reservation. A
// stale revision, a launched occurrence, a stale source head or changed
// programme facts refuse without effects.
func TestMGRecordingUpdateWindowAndStaleFacts(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v)
	saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
	ackMGRecording(t, w, v.root, 1)

	options := v.options()
	options.BeforeMS, options.AfterMS, options.Priority, options.RetentionDays = 0, 0, 9, 30
	request := v.update(saved.result.id, 1, options)
	reference := mgRecordingUpdateReference(t, mgPersonalIdentity(2, 2), request)
	turn := mgRecordingTurn(t, w, MGUpdateRecording)
	updated, err := acceptMGRecordingUpdate(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	want := mgRecordingExpectedResult(t, v, v.scope, v.programme, options, "scheduled", 2, 0, 1, 2)
	if err != nil || updated.decision != receiptFresh || updated.result != want {
		t.Fatal(updated, err)
	}
	mgPersonalCommit(t, turn)

	// The row and its one reservation moved to the same exact window; the
	// guide pin, birth namespace and absence of capture custody are retained.
	var startMS, endMS, revision, priority, keep, pinned int64
	var reservationStart, reservationEnd, reservationPriority, reservationEnabled int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT start_ms,end_ms,revision,priority,keep,count(guide_pin_generation) FROM dvr_recordings WHERE id=?`, want.id).Scan(&startMS, &endMS, &revision, &priority, &keep, &pinned); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT start_ms,end_ms,priority,enabled FROM live_reservations WHERE id=?`, want.id).Scan(&reservationStart, &reservationEnd, &reservationPriority, &reservationEnabled); err != nil {
		t.Fatal(err)
	}
	if startMS != want.startMS || endMS != want.endMS || revision != 2 || priority != 9 || keep != 0 || pinned != 1 ||
		reservationStart != want.startMS || reservationEnd != want.endMS || reservationPriority != 9 || reservationEnabled != 1 {
		t.Fatal(startMS, endMS, revision, priority, keep, pinned, reservationStart, reservationEnd, reservationPriority, reservationEnabled)
	}

	// Replay returns the original updated scalar with zero effects.
	turn = mgRecordingTurn(t, w, MGUpdateRecording)
	replay, err := acceptMGRecordingUpdate(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || replay.decision != receiptReplay || replay.result != want {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// Stale revision: the same window under the pre-update revision conflicts.
	ackMGRecording(t, w, v.root, 2)
	turn = mgRecordingTurn(t, w, MGUpdateRecording)
	stale := v.update(saved.result.id, 1, options)
	if got, err := acceptMGRecordingUpdate(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingUpdateReference(t, mgPersonalIdentity(3, 3), stale), stale); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("stale revision updated the occurrence", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A launched occurrence is capture custody, not editable intent.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET capture_launch_id=X'0102030405060708090a0b0c0d0e0f10' WHERE id=?`, want.id); err != nil {
		t.Fatal(err)
	}
	second := options
	second.Priority = 4
	turn = mgRecordingTurn(t, w, MGUpdateRecording)
	launched := v.update(saved.result.id, 2, second)
	if got, err := acceptMGRecordingUpdate(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingUpdateReference(t, mgPersonalIdentity(3, 3), launched), launched); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("launched occurrence was edited", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET capture_launch_id=NULL WHERE id=?`, want.id); err != nil {
		t.Fatal(err)
	}

	// A stale source revision refuses before any comparison with the row.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=8 WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}
	turn = mgRecordingTurn(t, w, MGUpdateRecording)
	headDrift := v.update(saved.result.id, 2, second)
	if got, err := acceptMGRecordingUpdate(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingUpdateReference(t, mgPersonalIdentity(3, 3), headDrift), headDrift); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("stale source revision was accepted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=7 WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}

	// Changed programme facts (a re-titled or re-rated occurrence) refuse: the
	// stored copy is immutable and today's guide is not a new grant.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_programmes SET title='Changed' WHERE generation_id=? AND id=?`, v.scope.generation, v.scope.programme); err != nil {
		t.Fatal(err)
	}
	turn = mgRecordingTurn(t, w, MGUpdateRecording)
	changedFacts := v.update(saved.result.id, 2, second)
	if got, err := acceptMGRecordingUpdate(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingUpdateReference(t, mgPersonalIdentity(3, 3), changedFacts), changedFacts); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("changed programme facts were accepted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// Cancel is logical intent custody: it advances the cancel generation, retains
// the guide pin and every allocation/artifact/share fact, disables but never
// deletes the reservation window, and is a truthful no-op when already
// cancelled. A terminal recording is refused.
func TestMGRecordingCancelRetainsCustody(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v)
	saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
	ackMGRecording(t, w, v.root, 1)

	// Simulate a capture worker holding physical custody on the same row. These
	// are native facts, not authority; cancel must leave all of them intact.
	lease := v.now.Add(time.Hour).UnixMilli()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='recording',claim_generation=3,claim_token='claim-token',owner_instance='worker-1',lease_until_ms=?,allocation_id='allocation-1',artifact_id='artifact-1',item_id=9 WHERE id=?`, lease, saved.result.id); err != nil {
		t.Fatal(err)
	}

	request := v.cancel(saved.result.id, 1)
	reference := mgRecordingCancelReference(t, mgPersonalIdentity(2, 2), request)
	turn := mgRecordingTurn(t, w, MGCancelRecording)
	cancelled, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	want := mgRecordingExpectedResult(t, v, v.scope, v.programme, v.options(), "cancelled", 2, 0, 1, 2)
	if err != nil || cancelled.decision != receiptFresh || cancelled.result != want {
		t.Fatal(cancelled, err)
	}
	mgPersonalCommit(t, turn)

	var state, reason, guidePin string
	var cancelGeneration, revision, claimGeneration, itemID int64
	var allocationID, artifactID, claimToken, ownerInstance string
	if err := w.db.QueryRowContext(t.Context(), `SELECT state,reason,guide_pin_generation,cancel_generation,revision,claim_generation,item_id,allocation_id,artifact_id,claim_token,owner_instance FROM dvr_recordings WHERE id=?`, want.id).Scan(&state, &reason, &guidePin, &cancelGeneration, &revision, &claimGeneration, &itemID, &allocationID, &artifactID, &claimToken, &ownerInstance); err != nil {
		t.Fatal(err)
	}
	if state != "cancelled" || reason != "owner-cancelled" || guidePin != v.scope.generation || cancelGeneration != 1 || revision != 2 ||
		claimGeneration != 3 || itemID != 9 || allocationID != "allocation-1" || artifactID != "artifact-1" || claimToken != "claim-token" || ownerInstance != "worker-1" {
		t.Fatal("cancel released capture custody", state, reason, guidePin, cancelGeneration, revision, claimGeneration, itemID, allocationID, artifactID, claimToken, ownerInstance)
	}
	// The window is disabled, not deleted; the artifact-bearing intent keeps its
	// source dependency so physical work is not orphaned.
	var reservationRows, enabled, dependencyRows int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_reservations WHERE id=?),(SELECT enabled FROM live_reservations WHERE id=?),(SELECT count(*) FROM live_source_dependencies WHERE source_id=? AND kind='recording' AND id=?)`, want.id, want.id, v.scope.source, want.id).Scan(&reservationRows, &enabled, &dependencyRows); err != nil {
		t.Fatal(err)
	}
	if reservationRows != 1 || enabled != 0 || dependencyRows != 1 {
		t.Fatal(reservationRows, enabled, dependencyRows)
	}

	// A second cancel at the same revision is a no-op: no revision, generation,
	// custody or owner advance.
	ackMGRecording(t, w, v.root, 2)
	noopRequest := v.cancel(saved.result.id, 2)
	turn = mgRecordingTurn(t, w, MGCancelRecording)
	noop, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(3, 3), noopRequest), noopRequest)
	if err != nil || noop.decision != receiptFresh || noop.result != want {
		t.Fatal(noop, err)
	}
	mgPersonalCommit(t, turn)
	var afterRevision, afterGeneration, afterSequence, ownerEpoch int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision,cancel_generation,(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?),(SELECT revision FROM dvr_owner_revisions WHERE owner_key=?) FROM dvr_recordings WHERE id=?`, v.root, v.owner.key(), want.id).Scan(&afterRevision, &afterGeneration, &afterSequence, &ownerEpoch); err != nil {
		t.Fatal(err)
	}
	// The no-op still persists its own receipt, so the user-mutation sequence
	// advances; the intent revision, cancel generation and owner intent epoch
	// do not.
	if afterRevision != 2 || afterGeneration != 1 || afterSequence != 3 || ownerEpoch != 2 {
		t.Fatal("cancelled no-op advanced intent", afterRevision, afterGeneration, afterSequence, ownerEpoch)
	}

	// A deleted occurrence is terminal and never cancellable again.
	ackMGRecording(t, w, v.root, 3)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='deleted' WHERE id=?`, want.id); err != nil {
		t.Fatal(err)
	}
	terminal := v.cancel(saved.result.id, 2)
	turn = mgRecordingTurn(t, w, MGCancelRecording)
	if got, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(4, 4), terminal), terminal); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("terminal occurrence was cancelled", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A plain pending intent (no artifact/item) drops only its own dependency
	// after the window is disabled.
	ackMGRecording(t, w, v.root, 3)
	plain := v.schedule()
	plain.scope, plain.programme = v.other, v.otherProgramme
	plainReference := mgRecordingScheduleReference(t, mgPersonalIdentity(4, 4), plain)
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	plainSaved, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), plainReference, plain)
	if err != nil || plainSaved.decision != receiptFresh {
		t.Fatal(plainSaved, err)
	}
	mgPersonalCommit(t, turn)
	ackMGRecording(t, w, v.root, 4)
	plainCancel := v.cancel(plainSaved.result.id, 1)
	turn = mgRecordingTurn(t, w, MGCancelRecording)
	if _, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(5, 5), plainCancel), plainCancel); err != nil {
		t.Fatal(err)
	}
	mgPersonalCommit(t, turn)
	var plainDependencies, plainReservations, plainEnabled int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_source_dependencies WHERE id=?),(SELECT count(*) FROM live_reservations WHERE id=?),(SELECT enabled FROM live_reservations WHERE id=?)`, plainSaved.result.id, plainSaved.result.id, plainSaved.result.id).Scan(&plainDependencies, &plainReservations, &plainEnabled); err != nil {
		t.Fatal(err)
	}
	if plainDependencies != 0 || plainReservations != 1 || plainEnabled != 0 {
		t.Fatal("plain cancel cleanup", plainDependencies, plainReservations, plainEnabled)
	}
}

// Keep is retention intent only. It is allowed after cancel and provider
// removal, refuses a pending-delete/deleted occurrence, and never touches the
// reservation, allocation, artifact or capture namespace.
func TestMGRecordingKeepRetentionAndTerminalRefusal(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v)
	saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
	ackMGRecording(t, w, v.root, 1)

	request := v.keep(saved.result.id, 1, true)
	reference := mgRecordingKeepReference(t, mgPersonalIdentity(2, 2), request)
	turn := mgRecordingTurn(t, w, MGKeepRecording)
	kept, err := acceptMGRecordingKeep(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	want := mgRecordingExpectedResult(t, v, v.scope, v.programme, v.options(), "scheduled", 2, 1, 1, 2)
	if err != nil || kept.decision != receiptFresh || kept.result != want {
		t.Fatal(kept, err)
	}
	mgPersonalCommit(t, turn)

	// Repeat keep at the new revision is another accepted intent revision with
	// the same value; the window and the absence of capture custody are intact.
	ackMGRecording(t, w, v.root, 2)
	repeat := v.keep(saved.result.id, 2, true)
	turn = mgRecordingTurn(t, w, MGKeepRecording)
	again, err := acceptMGRecordingKeep(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingKeepReference(t, mgPersonalIdentity(3, 3), repeat), repeat)
	if err != nil || again.decision != receiptFresh || again.result.revision != 3 || again.result.keep != 1 {
		t.Fatal(again, err)
	}
	mgPersonalCommit(t, turn)
	var enabled, claim, revision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT enabled FROM live_reservations WHERE id=?),(SELECT count(*) FROM dvr_recordings WHERE id=? AND (claim_generation<>0 OR allocation_id<>'' OR artifact_id<>'' OR capture_launch_id IS NOT NULL)),(SELECT revision FROM dvr_recordings WHERE id=?)`, want.id, want.id, want.id).Scan(&enabled, &claim, &revision); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 || claim != 0 || revision != 3 {
		t.Fatal("keep changed custody", enabled, claim, revision)
	}

	// Keep survives logical cancellation: retention is independent of state.
	ackMGRecording(t, w, v.root, 3)
	cancelRequest := v.cancel(saved.result.id, 3)
	turn = mgRecordingTurn(t, w, MGCancelRecording)
	if _, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(4, 4), cancelRequest), cancelRequest); err != nil {
		t.Fatal(err)
	}
	mgPersonalCommit(t, turn)
	ackMGRecording(t, w, v.root, 4)
	afterCancel := v.keep(saved.result.id, 4, false)
	turn = mgRecordingTurn(t, w, MGKeepRecording)
	cleared, err := acceptMGRecordingKeep(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingKeepReference(t, mgPersonalIdentity(5, 5), afterCancel), afterCancel)
	if err != nil || cleared.decision != receiptFresh || cleared.result.state != "cancelled" || cleared.result.keep != 0 || cleared.result.revision != 5 {
		t.Fatal(cleared, err)
	}
	mgPersonalCommit(t, turn)

	// Stale revision refuses.
	ackMGRecording(t, w, v.root, 5)
	stale := v.keep(saved.result.id, 4, true)
	turn = mgRecordingTurn(t, w, MGKeepRecording)
	if got, err := acceptMGRecordingKeep(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingKeepReference(t, mgPersonalIdentity(6, 6), stale), stale); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("stale keep was accepted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A pending-delete occurrence refuses keep: deletion already owns it.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='pending-delete' WHERE id=?`, want.id); err != nil {
		t.Fatal(err)
	}
	pending := v.keep(saved.result.id, 5, true)
	turn = mgRecordingTurn(t, w, MGKeepRecording)
	if got, err := acceptMGRecordingKeep(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingKeepReference(t, mgPersonalIdentity(6, 6), pending), pending); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("pending-delete occurrence was kept", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// Schedule adopts an existing rule occurrence as manual intent without moving
// its window, reservation or pin; an already-manual duplicate is refused.
func TestMGRecordingScheduleAdoptsRuleIntent(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v)
	saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
	ackMGRecording(t, w, v.root, 1)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET rule_id='rule-1',rule_revision=4,manual=0 WHERE id=?`, saved.result.id); err != nil {
		t.Fatal(err)
	}
	request := v.schedule()
	turn := mgRecordingTurn(t, w, MGScheduleRecording)
	adopted, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(2, 2), request), request)
	want := saved.result
	want.manual, want.revision, want.ownerRevision = 1, 2, 2
	if err != nil || adopted.decision != receiptFresh || adopted.result != want {
		t.Fatal(adopted, err)
	}
	mgPersonalCommit(t, turn)
	var ruleID string
	var ruleRevision, manual, revision, reservationStart int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT rule_id,rule_revision,manual,revision,(SELECT start_ms FROM live_reservations WHERE id=dvr_recordings.id) FROM dvr_recordings WHERE id=?`, want.id).Scan(&ruleID, &ruleRevision, &manual, &revision, &reservationStart); err != nil {
		t.Fatal(err)
	}
	if ruleID != "" || ruleRevision != 0 || manual != 1 || revision != 2 || reservationStart != want.startMS {
		t.Fatal("rule adoption changed custody", ruleID, ruleRevision, manual, revision, reservationStart)
	}
}

// Schedule refuses a duplicate manual identity, a retired birth namespace, a
// stale or removed source, a replaced head and an already-ended window.
func TestMGRecordingScheduleRefusals(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v)
	saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
	ackMGRecording(t, w, v.root, 1)

	// The same owner/source/programme occurrence already exists as manual
	// intent: a second schedule under a new identity conflicts.
	turn := mgRecordingTurn(t, w, MGScheduleRecording)
	duplicate := v.schedule()
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(2, 2), duplicate), duplicate); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("duplicate identity was scheduled twice", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A retired birth namespace is never resurrected.
	retiredID := mgRecordingID(v.owner, v.other.source, v.other.programme)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_capture_retirements(recording_id,command_incarnation,create_operation_id,birth_request_digest,source_id,final_sequence,final_result_digest,retired_ms,owner_key,authority,account_id,profile_id,member_incarnation,profile_lifetime,last_command_sequence,acknowledged_command_sequence) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		retiredID, make([]byte, 16), make([]byte, 16), make([]byte, 32), v.other.source, int64(1), make([]byte, 32), int64(1), v.owner.key(), "local", v.owner.account, v.owner.profile, v.owner.memberIncarnation, v.owner.profileLifetime, int64(1), int64(1)); err != nil {
		t.Fatal(err)
	}
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	retired := v.schedule()
	retired.scope, retired.programme = v.other, v.otherProgramme
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(2, 2), retired), retired); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("retired namespace was scheduled", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A stale source revision refuses before the programme point is trusted.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=8 WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	stale := v.schedule()
	stale.scope, stale.programme = v.other, v.otherProgramme
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(2, 2), stale), stale); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("stale source revision was scheduled", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=7 WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}

	// A removed source refuses even with a valid revision and head.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='removal-1',removal_revision=1,removal_removed_at='2026-11-09T00:00:00Z' WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	removed := v.schedule()
	removed.scope, removed.programme = v.other, v.otherProgramme
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(2, 2), removed), removed); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("removed source was scheduled", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=0,removal_request_id=NULL,removal_revision=NULL,removal_removed_at=NULL WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}

	// A replaced head refuses: the pinned generation is no longer current.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET active_generation=? WHERE id=?`, v.replacement, v.scope.source); err != nil {
		t.Fatal(err)
	}
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	replaced := v.schedule()
	replaced.scope, replaced.programme = v.other, v.otherProgramme
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(2, 2), replaced), replaced); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("replaced head was scheduled", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET active_generation=? WHERE id=?`, v.scope.generation, v.scope.source); err != nil {
		t.Fatal(err)
	}

	// An already-ended window refuses without an effect.
	w.now = func() time.Time { return v.now.Add(48 * time.Hour) }
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	ended := v.schedule()
	ended.scope, ended.programme = v.other, v.otherProgramme
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(2, 2), ended), ended); !errors.Is(err, ErrInvalid) || got != (mgRecordingAcceptance{}) {
		t.Fatal("ended window was scheduled", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A caller cannot schedule for another owner tuple, and a non-hex
	// occurrence/option shape refuses at construction.
	foreign := v.schedule()
	foreign.owner.profile = "another-profile"
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(2, 2), foreign), foreign); !errors.Is(err, ErrDisclosureDenied) || got != (mgRecordingAcceptance{}) {
		t.Fatal("foreign owner tuple was scheduled", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := newMGRecordingScheduleCommand(mgPersonalIdentity(2, 2), v.schedule()); err != nil {
		t.Fatal(err)
	}
	bad := v.schedule()
	bad.options.BeforeMS = -1
	if _, err := newMGRecordingScheduleCommand(mgPersonalIdentity(2, 2), bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid options accepted", err)
	}
	if saved.result.id == "" {
		t.Fatal("fixture schedule lost its identity")
	}
}
