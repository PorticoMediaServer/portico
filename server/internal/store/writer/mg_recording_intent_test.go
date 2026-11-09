package writer

import (
	"errors"
	"strings"
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

// The legacy reconcile path (livechannels/dvr/reconcile.go and scheduled.go)
// moves a waiting-source, waiting-guide or conflicted intent and only disables
// its reservation (enabled=0). Cancel must still accept that row: the observed
// enabled value is not the intent's custody, and the CAS stays on the value it
// actually read. A window, priority or source that no longer belongs to the row
// is still refused.
func TestMGRecordingCancelDisabledReservation(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v)
	rows := []struct {
		scope     mgRecordingScope
		programme mgRecordingProgramme
		state     string
		reason    string
	}{
		{v.scope, v.programme, "waiting-source", "source-disabled"},
		{v.other, v.otherProgramme, "waiting-guide", "programme-unavailable"},
		{v.third, v.thirdProgramme, "conflicted", ""},
	}
	saved := make([]mgRecordingAcceptance, len(rows))
	for n, row := range rows {
		saved[n] = scheduleMGRecordingScope(t, w, v, fake, mgPersonalIdentity(int64(n+1), byte(n+1)), row.scope, row.programme)
		ackMGRecording(t, w, v.root, int64(n+1))
		disableMGRecordingReservation(t, w, saved[n].result.id, row.state, row.reason)
	}

	// A reservation whose window, priority or source moved is a different
	// window, never this intent's disabled one: cancel refuses with no effect.
	foreignSource := mgRecordingHexValue('c', 48)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_source_identities(id) VALUES(?)`, foreignSource); err != nil {
		t.Fatal(err)
	}
	for _, tamper := range []struct {
		name, sql string
		args      []any
		restore   string
	}{
		{"window", `UPDATE live_reservations SET end_ms=end_ms+1000 WHERE id=?`, []any{saved[0].result.id}, `UPDATE live_reservations SET end_ms=end_ms-1000 WHERE id=?`},
		{"priority", `UPDATE live_reservations SET priority=priority+1 WHERE id=?`, []any{saved[0].result.id}, `UPDATE live_reservations SET priority=priority-1 WHERE id=?`},
		{"source", `UPDATE live_reservations SET source_id=? WHERE id=?`, []any{foreignSource, saved[0].result.id}, `UPDATE live_reservations SET source_id=? WHERE id=?`},
	} {
		if _, err := w.db.ExecContext(t.Context(), tamper.sql, tamper.args...); err != nil {
			t.Fatal(tamper.name, err)
		}
		request := v.cancel(saved[0].result.id, 1)
		turn := mgRecordingTurn(t, w, MGCancelRecording)
		if got, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(4, 4), request), request); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
			t.Fatal(tamper.name, "tampered reservation was cancelled", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		if tamper.name == "source" {
			if _, err := w.db.ExecContext(t.Context(), tamper.restore, v.scope.source, saved[0].result.id); err != nil {
				t.Fatal(tamper.name, err)
			}
		} else if _, err := w.db.ExecContext(t.Context(), tamper.restore, saved[0].result.id); err != nil {
			t.Fatal(tamper.name, err)
		}
	}

	// A missing reservation row is also refused: cancel never invents a window
	// it did not read.
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM live_reservations WHERE id=?`, saved[0].result.id); err != nil {
		t.Fatal(err)
	}
	missing := v.cancel(saved[0].result.id, 1)
	turn := mgRecordingTurn(t, w, MGCancelRecording)
	if got, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(4, 4), missing), missing); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("missing reservation was cancelled", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_reservations(id,source_id,start_ms,end_ms,priority,enabled) VALUES(?,?,?,?,?,0)`, saved[0].result.id, v.scope.source, saved[0].result.startMS, saved[0].result.endMS, saved[0].result.priority); err != nil {
		t.Fatal(err)
	}
	var tamperedState string
	var tamperedRevision, tamperedEnabled int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT state,revision,(SELECT enabled FROM live_reservations WHERE id=?) FROM dvr_recordings WHERE id=?`, saved[0].result.id, saved[0].result.id).Scan(&tamperedState, &tamperedRevision, &tamperedEnabled); err != nil {
		t.Fatal(err)
	}
	if tamperedState != "waiting-source" || tamperedRevision != 1 || tamperedEnabled != 0 {
		t.Fatal("tampered reservation attempt changed the intent", tamperedState, tamperedRevision, tamperedEnabled)
	}

	for n, row := range rows {
		want := mgRecordingExpectedResult(t, v, row.scope, row.programme, v.options(), "cancelled", 2, 0, 1, int64(4+n))
		request := v.cancel(saved[n].result.id, 1)
		turn := mgRecordingTurn(t, w, MGCancelRecording)
		got, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(int64(4+n), byte(4+n)), request), request)
		if err != nil || got.decision != receiptFresh || got.result != want {
			t.Fatal(row.state, "disabled reservation was not cancellable", got, err)
		}
		mgPersonalCommit(t, turn)
		ackMGRecording(t, w, v.root, int64(4+n))
	}
	for _, row := range rows {
		id := mgRecordingID(v.owner, row.scope.source, row.scope.programme)
		var state, reason string
		var revision, reservationRows, enabled int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT state,reason,revision,(SELECT count(*) FROM live_reservations WHERE id=?),(SELECT enabled FROM live_reservations WHERE id=?) FROM dvr_recordings WHERE id=?`, id, id, id).Scan(&state, &reason, &revision, &reservationRows, &enabled); err != nil {
			t.Fatal(err)
		}
		if state != "cancelled" || reason != "owner-cancelled" || revision != 2 || reservationRows != 1 || enabled != 0 {
			t.Fatal(row.state, "cancel custody", state, reason, revision, reservationRows, enabled)
		}
	}
}

// A waiting/conflicted intent with a disabled reservation is also editable back
// to scheduled: update accepts either observed enabled value, keeps the CAS on
// it, and re-enables the same window. A tampered window is still refused.
func TestMGRecordingUpdateDisabledReservation(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v)
	rows := []struct {
		scope     mgRecordingScope
		programme mgRecordingProgramme
		state     string
		reason    string
	}{
		{v.scope, v.programme, "waiting-source", "source-disabled"},
		{v.other, v.otherProgramme, "waiting-guide", "programme-unavailable"},
		{v.third, v.thirdProgramme, "conflicted", ""},
	}
	saved := make([]mgRecordingAcceptance, len(rows))
	for n, row := range rows {
		saved[n] = scheduleMGRecordingScope(t, w, v, fake, mgPersonalIdentity(int64(n+1), byte(n+1)), row.scope, row.programme)
		ackMGRecording(t, w, v.root, int64(n+1))
		disableMGRecordingReservation(t, w, saved[n].result.id, row.state, row.reason)
	}

	options := v.options()
	options.Priority = 9
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_reservations SET end_ms=end_ms+1000 WHERE id=?`, saved[0].result.id); err != nil {
		t.Fatal(err)
	}
	tampered := v.update(saved[0].result.id, 1, options)
	tampered.scope, tampered.programme = rows[0].scope, rows[0].programme
	turn := mgRecordingTurn(t, w, MGUpdateRecording)
	if got, err := acceptMGRecordingUpdate(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingUpdateReference(t, mgPersonalIdentity(4, 4), tampered), tampered); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("tampered reservation window was updated", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_reservations SET end_ms=end_ms-1000 WHERE id=?`, saved[0].result.id); err != nil {
		t.Fatal(err)
	}

	for n, row := range rows {
		request := v.update(saved[n].result.id, 1, options)
		request.scope, request.programme = row.scope, row.programme
		want := mgRecordingExpectedResult(t, v, row.scope, row.programme, options, "scheduled", 2, 0, 1, int64(4+n))
		turn := mgRecordingTurn(t, w, MGUpdateRecording)
		got, err := acceptMGRecordingUpdate(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingUpdateReference(t, mgPersonalIdentity(int64(4+n), byte(4+n)), request), request)
		if err != nil || got.decision != receiptFresh || got.result != want {
			t.Fatal(row.state, "disabled reservation was not editable", got, err)
		}
		mgPersonalCommit(t, turn)
		ackMGRecording(t, w, v.root, int64(4+n))
	}
	for n, row := range rows {
		var state string
		var revision, startMS, endMS, priority, enabled int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT state,revision,(SELECT start_ms FROM live_reservations WHERE id=?),(SELECT end_ms FROM live_reservations WHERE id=?),(SELECT priority FROM live_reservations WHERE id=?),(SELECT enabled FROM live_reservations WHERE id=?) FROM dvr_recordings WHERE id=?`, saved[n].result.id, saved[n].result.id, saved[n].result.id, saved[n].result.id, saved[n].result.id).Scan(&state, &revision, &startMS, &endMS, &priority, &enabled); err != nil {
			t.Fatal(err)
		}
		want := mgRecordingExpectedResult(t, v, row.scope, row.programme, options, "scheduled", 2, 0, 1, 0)
		if state != "scheduled" || revision != 2 || startMS != want.startMS || endMS != want.endMS || priority != 9 || enabled != 1 {
			t.Fatal(row.state, "update re-enabled custody", state, revision, startMS, endMS, priority, enabled)
		}
	}
}

// Cancel refuses every terminal capture state that is not an already-cancelled
// no-op: completed, incomplete-playable and failed intents stay untouched.
func TestMGRecordingCancelTerminalRefusal(t *testing.T) {
	for _, state := range []string{"completed", "incomplete-playable", "failed"} {
		t.Run(state, func(t *testing.T) {
			w, _, v := mgRecordingFixture(t)
			fake := mgRecordingFake(v)
			saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
			ackMGRecording(t, w, v.root, 1)
			if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state=? WHERE id=?`, state, saved.result.id); err != nil {
				t.Fatal(err)
			}
			request := v.cancel(saved.result.id, 1)
			turn := mgRecordingTurn(t, w, MGCancelRecording)
			if got, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(2, 2), request), request); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
				t.Fatal(state, "terminal occurrence was cancelled", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var gotState string
			var revision, enabled int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT state,revision,(SELECT enabled FROM live_reservations WHERE id=?) FROM dvr_recordings WHERE id=?`, saved.result.id, saved.result.id).Scan(&gotState, &revision, &enabled); err != nil {
				t.Fatal(err)
			}
			if gotState != state || revision != 1 || enabled != 1 {
				t.Fatal(state, "terminal refusal changed the intent", gotState, revision, enabled)
			}
		})
	}
}

// Cancelling an intent that owns an artifact alone or an item alone retains its
// source dependency, exactly as the combined custody case does: cancel never
// orphans physical work.
func TestMGRecordingCancelDependencyRetention(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"artifact-alone", `UPDATE dvr_recordings SET artifact_id='artifact-1',item_id=0 WHERE id=?`},
		{"item-alone", `UPDATE dvr_recordings SET artifact_id='',item_id=9 WHERE id=?`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, v := mgRecordingFixture(t)
			fake := mgRecordingFake(v)
			saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
			ackMGRecording(t, w, v.root, 1)
			if _, err := w.db.ExecContext(t.Context(), tc.sql, saved.result.id); err != nil {
				t.Fatal(err)
			}
			request := v.cancel(saved.result.id, 1)
			turn := mgRecordingTurn(t, w, MGCancelRecording)
			if _, err := acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(2, 2), request), request); err != nil {
				t.Fatal(err)
			}
			mgPersonalCommit(t, turn)
			var dependencies, reservations, enabled int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_source_dependencies WHERE source_id=? AND kind='recording' AND id=?),(SELECT count(*) FROM live_reservations WHERE id=?),(SELECT enabled FROM live_reservations WHERE id=?)`, v.scope.source, saved.result.id, saved.result.id, saved.result.id).Scan(&dependencies, &reservations, &enabled); err != nil {
				t.Fatal(err)
			}
			if dependencies != 1 || reservations != 1 || enabled != 0 {
				t.Fatal(tc.name, "dependency retention", dependencies, reservations, enabled)
			}
		})
	}
}

// A session authorized for owner A cannot schedule, update, cancel or keep
// owner B's real intent, even when the typed request names B and its actual
// recording id. The second account/profile/recording below are genuine native
// rows, so removing the root-to-owner binding would let A act on B.
func TestMGRecordingOwnerBindingRejectsForeignSession(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v) // current session: fixture owner A
	second := mgRecordingOwner{
		account: "second-account", profile: "second-profile",
		memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32),
		accountEpoch: 1, memberRevision: 1, profileRevision: 1,
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,X'',?)`, []any{second.account, second.account, second.profile}},
		{`INSERT INTO direct_memberships(account_id,incarnation,role) VALUES(?,?,'member')`, []any{second.account, second.memberIncarnation}},
		{`INSERT INTO direct_profiles(id,account_id,name,is_primary,lifetime) VALUES(?,?,'Second',1,?)`, []any{second.profile, second.account, second.profileLifetime}},
	} {
		if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	// Owner B's own accepted occurrence, created through the same effect the
	// accepting frame uses (the fake session is minted only for owner A).
	frozen, err := freezeMGRecordingIntent(v.otherProgramme, v.options())
	if err != nil {
		t.Fatal(err)
	}
	var birth mgRecordingBirth
	birth.incarnation[0], birth.operation[0], birth.digest[0] = 1, 1, 1
	turn := mgRecordingTurn(t, w, MGScheduleRecording)
	facts, err := mgScheduleRecording(t.Context(), turn, second, v.other, frozen, birth)
	if err != nil {
		t.Fatal(err)
	}
	mgPersonalCommit(t, turn)
	// A clean fresh command point for the fixture root: a missing root-to-owner
	// binding would then reach the effect instead of failing at receipt
	// sequencing, so this cell fails on that mutation.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET last_command_sequence=1,acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}

	options := v.options()
	options.Priority = 8
	updateRequest := v.update(facts.row.id, 1, options)
	updateRequest.owner, updateRequest.scope, updateRequest.programme = second, v.other, v.otherProgramme
	cancelRequest := mgRecordingCancelRequest{owner: second, id: facts.row.id, expected: 1}
	keepRequest := mgRecordingKeepRequest{owner: second, id: facts.row.id, expected: 1, keep: true}
	scheduleRequest := v.schedule()
	scheduleRequest.owner, scheduleRequest.scope, scheduleRequest.programme = second, v.third, v.thirdProgramme

	attempts := []struct {
		purpose CommandID
		run     func(*commandTurn) (mgRecordingAcceptance, error)
	}{
		{MGScheduleRecording, func(turn *commandTurn) (mgRecordingAcceptance, error) {
			return acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(2, 2), scheduleRequest), scheduleRequest)
		}},
		{MGUpdateRecording, func(turn *commandTurn) (mgRecordingAcceptance, error) {
			return acceptMGRecordingUpdate(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingUpdateReference(t, mgPersonalIdentity(2, 2), updateRequest), updateRequest)
		}},
		{MGCancelRecording, func(turn *commandTurn) (mgRecordingAcceptance, error) {
			return acceptMGRecordingCancel(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingCancelReference(t, mgPersonalIdentity(2, 2), cancelRequest), cancelRequest)
		}},
		{MGKeepRecording, func(turn *commandTurn) (mgRecordingAcceptance, error) {
			return acceptMGRecordingKeep(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingKeepReference(t, mgPersonalIdentity(2, 2), keepRequest), keepRequest)
		}},
	}
	for _, attempt := range attempts {
		turn := mgRecordingTurn(t, w, attempt.purpose)
		if got, err := attempt.run(turn); !errors.Is(err, ErrDisclosureDenied) || got != (mgRecordingAcceptance{}) {
			t.Fatal(attempt.purpose, "foreign session acted on owner B", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}

	var state string
	var revision, enabled, recordings, dependencies int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT state,revision,(SELECT enabled FROM live_reservations WHERE id=?),(SELECT count(*) FROM dvr_recordings),(SELECT count(*) FROM live_source_dependencies WHERE id=?) FROM dvr_recordings WHERE id=?`, facts.row.id, facts.row.id, facts.row.id).Scan(&state, &revision, &enabled, &recordings, &dependencies); err != nil {
		t.Fatal(err)
	}
	if state != "scheduled" || revision != 1 || enabled != 1 || recordings != 1 || dependencies != 1 {
		t.Fatal("foreign session changed owner B", state, revision, enabled, recordings, dependencies)
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

	// A deleted occurrence refuses keep as well.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='deleted' WHERE id=?`, want.id); err != nil {
		t.Fatal(err)
	}
	deleted := v.keep(saved.result.id, 5, true)
	turn = mgRecordingTurn(t, w, MGKeepRecording)
	if got, err := acceptMGRecordingKeep(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingKeepReference(t, mgPersonalIdentity(7, 7), deleted), deleted); !errors.Is(err, ErrConflict) || got != (mgRecordingAcceptance{}) {
		t.Fatal("deleted occurrence was kept", got, err)
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
