package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func mgScheduledFixture(t *testing.T) (*executor, string, mgReconcileValues) {
	t.Helper()
	w, path, v := mgReconcileFixture(t)
	retireMGReconcileRule(t, w, &v)
	mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "discover", "", 7, 1, v.now+10000, 1, 3, v.now)
	return w, path, v
}

func mgScheduledAccept(t *testing.T, w *executor, v mgReconcileValues, identity CommandIdentity, request mgReconcileScheduledRequest) (mgReconcileAcceptance, *commandTurn) {
	t.Helper()
	fake := mgPersonalFake(t, v.ruleValues.personal)
	reference := mgReconcileScheduledReference(t, identity, request)
	turn := mgReconcileTurn(t, w, MGScheduledReconcile)
	out, err := acceptMGScheduledReconcile(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), reference, request)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	mgRuleCommit(t, turn)
	return out, turn
}

// 827 keep: a retired rule's bounded future intents are detached to manual
// intent; their reservations keep running and the exact work cursor retires.
func TestMGScheduledReconcileKeepDetachesFutureIntents(t *testing.T) {
	w, path, v := mgScheduledFixture(t)
	first := mgReconcileIntent(t, w, v, 1, v.now+3600000, "", 0)
	second := mgReconcileIntent(t, w, v, 2, v.now+7200000, "", 0)
	before := mgReconcileStoredWork(t, w, v.rule)
	request := v.scheduledRequest("keep", 7, before)
	out, turn := mgScheduledAccept(t, w, v, mgPersonalIdentity(1, 1), request)
	want := mgReconcileResult{
		variant: mgScheduledResultVariant, rule: v.rule, owner: v.owner().key(), generation: v.ruleValues.generation,
		phase: "idle", ruleRevision: 2, workRevision: 4, jobGeneration: 1, attempts: 1,
		units: 2, ownerRevision: 1, ids: []string{first, second},
	}
	if !mgReconcileResultEqual(out.result, want) {
		t.Fatal(out.result)
	}
	for _, id := range []string{first, second} {
		state, rule, ruleRevision, manual, revision, cancelGeneration := mgReconcileIntentRow(t, w.db, id)
		if state != "scheduled" || rule != "" || ruleRevision != 0 || manual != 1 || revision != 2 || cancelGeneration != 0 {
			t.Fatal("detach", id, state, rule, ruleRevision, manual, revision, cancelGeneration)
		}
		if enabled := mgReconcileReservationEnabled(t, w.db, id); enabled != 1 {
			t.Fatal("detach disabled a reservation", id)
		}
	}
	if got := mgReconcileDependencies(t, w.db, v.ruleValues.source); got != 2 {
		t.Fatal("detach dropped dependencies", got)
	}
	stored := mgReconcileStoredWork(t, w, v.rule)
	if stored.phase != "idle" || stored.hasDue || stored.jobOwner != 0 || stored.expires != 0 ||
		stored.attempts != 1 || stored.leaseGeneration != 1 || stored.jobRevision != 4 || stored.cursor != "" {
		t.Fatal("work progress", stored)
	}
	var ownerRevision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM dvr_owner_revisions WHERE owner_key=?`, v.owner().key()).Scan(&ownerRevision); err != nil || ownerRevision != 1 {
		t.Fatal(ownerRevision, err)
	}
	readOnly := mgPersonalReadOnly(t, path)
	if got := mgReconcileStoredWorkAt(t, readOnly, v.rule); got != stored {
		t.Fatal("fresh connection work", got)
	}
	_ = turn

	// Identical retry returns the original saved scalar with no row change.
	replayTurn := mgReconcileTurn(t, w, MGScheduledReconcile)
	replay, err := acceptMGScheduledReconcile(t.Context(), replayTurn, mgPersonalFake(t, v.ruleValues.personal), v.ruleValues.proof, t.Context(),
		mgReconcileScheduledReference(t, mgPersonalIdentity(1, 1), request), request)
	if err != nil || replay.decision != receiptReplay || !mgReconcileResultEqual(replay.result, want) || replay.digest != out.digest {
		t.Fatal(replay, err)
	}
	if _, err := replayTurn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if got := mgReconcileStoredWork(t, w, v.rule); got != stored {
		t.Fatal("replay changed work", got)
	}

	// ACK retires the original.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=? AND last_command_sequence=1`, v.ruleValues.root); err != nil {
		t.Fatal(err)
	}
	doneTurn := mgReconcileTurn(t, w, MGScheduledReconcile)
	done, err := acceptMGScheduledReconcile(t.Context(), doneTurn, mgPersonalFake(t, v.ruleValues.personal), v.ruleValues.proof, t.Context(),
		mgReconcileScheduledReference(t, mgPersonalIdentity(1, 1), request), request)
	if err != nil || done.decision != receiptRetired || !mgReconcileAcceptanceZero(done) {
		t.Fatal(done, err)
	}
	if _, err := doneTurn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// 827 cancel: future intents are cancelled, their conflict windows disabled,
// and only an intent with no artifact/item debt loses its source dependency.
func TestMGScheduledReconcileCancelRetiresFutureIntents(t *testing.T) {
	w, _, v := mgScheduledFixture(t)
	debt := mgReconcileIntent(t, w, v, 1, v.now+3600000, strings.Repeat("a", 64), 0)
	free := mgReconcileIntent(t, w, v, 2, v.now+7200000, "", 0)
	// The cancel cohort is the legacy not-yet-launched state set, not only
	// 'scheduled': a waiting-source intent is reconciled in the same page.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='waiting-source' WHERE id=?`, debt); err != nil {
		t.Fatal(err)
	}
	before := mgReconcileStoredWork(t, w, v.rule)
	out, _ := mgScheduledAccept(t, w, v, mgPersonalIdentity(1, 1), v.scheduledRequest("cancel", 7, before))
	if !mgReconcileResultEqual(out.result, mgReconcileResult{
		variant: mgScheduledResultVariant, rule: v.rule, owner: v.owner().key(), generation: v.ruleValues.generation,
		phase: "idle", ruleRevision: 2, workRevision: 4, jobGeneration: 1, attempts: 1,
		units: 2, ownerRevision: 1, ids: []string{debt, free},
	}) {
		t.Fatal(out.result)
	}
	for _, id := range []string{debt, free} {
		state, rule, ruleRevision, manual, revision, cancelGeneration := mgReconcileIntentRow(t, w.db, id)
		if state != "cancelled" || rule != v.rule || ruleRevision != 1 || manual != 0 || revision != 2 || cancelGeneration != 1 {
			t.Fatal("cancel", id, state, rule, ruleRevision, manual, revision, cancelGeneration)
		}
		var reason string
		if err := w.db.QueryRowContext(t.Context(), `SELECT reason FROM dvr_recordings WHERE id=?`, id).Scan(&reason); err != nil || reason != mgScheduledCanonicalRule {
			t.Fatal("reason", reason, err)
		}
		if enabled := mgReconcileReservationEnabled(t, w.db, id); enabled != 0 {
			t.Fatal("cancel left reservation enabled", id)
		}
	}
	// Only the debt-free occurrence's dependency is removed.
	if got := mgReconcileDependencies(t, w.db, v.ruleValues.source); got != 1 {
		t.Fatal("dependency custody", got)
	}
	var kept int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_source_dependencies WHERE source_id=? AND kind='recording' AND id=?`, v.ruleValues.source, debt).Scan(&kept); err != nil || kept != 1 {
		t.Fatal("debt dependency dropped", kept, err)
	}
}

// Partial pages stay truthfully pending: eight intents and a cursor first, then
// the remaining two and idle retirement. No intent is processed twice.
func TestMGScheduledReconcileBoundedPendingCursor(t *testing.T) {
	w, _, v := mgScheduledFixture(t)
	var all []string
	for n := 1; n <= 10; n++ {
		all = append(all, mgReconcileIntent(t, w, v, n, v.now+int64(n)*3600000, "", 0))
	}
	first, _ := mgScheduledAccept(t, w, v, mgPersonalIdentity(1, 1), v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule)))
	ackMGReconcile(t, w, v.ruleValues.root, 1)
	if first.result.pending != true || first.result.phase != "discover" || first.result.units != 8 || len(first.result.ids) != 8 ||
		!mgScheduledCursorMatches(t, first.result.cursor, all[7]) {
		t.Fatal("first page", first.result)
	}
	stored := mgReconcileStoredWork(t, w, v.rule)
	if stored.phase != "discover" || stored.cursor != first.result.cursor || stored.jobRevision != 4 || !stored.hasDue || stored.jobOwner != 7 {
		t.Fatal("first page work", stored)
	}
	// The lookahead rows are untouched by the first page.
	for _, id := range all[8:] {
		if _, rule, _, _, revision, _ := mgReconcileIntentRow(t, w.db, id); rule != v.rule || revision != 1 {
			t.Fatal("page one touched a lookahead intent", id)
		}
	}
	second, _ := mgScheduledAccept(t, w, v, mgPersonalIdentity(2, 2), v.scheduledRequest("keep", 7, stored))
	if second.result.pending || second.result.phase != "idle" || second.result.units != 2 ||
		!mgReconcileResultIdsEqual(second.result.ids, all[8:]) {
		t.Fatal("second page", second.result)
	}
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, first.result.ids...), second.result.ids...) {
		if seen[id] {
			t.Fatal("duplicate intent", id)
		}
		seen[id] = true
	}
	if len(seen) != len(all) {
		t.Fatal("intent coverage", len(seen), len(all))
	}
	if stored := mgReconcileStoredWork(t, w, v.rule); stored.phase != "idle" || stored.hasDue || stored.jobOwner != 0 {
		t.Fatal("second page work", stored)
	}
}

// A malformed persisted cursor is refused before any intent read.
func TestMGScheduledReconcileCursorShape(t *testing.T) {
	w, _, v := mgScheduledFixture(t)
	mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "discover", "bogus", 7, 1, v.now+10000, 1, 3, v.now)
	request := v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule))
	if _, err := newMGReconcileScheduledCommand(mgPersonalIdentity(1, 1), request); !errors.Is(err, ErrInvalid) {
		t.Fatal("malformed work cursor accepted", err)
	}
	if _, _, ok := mgScheduledCursorParse("bogus"); ok {
		t.Fatal("malformed cursor parsed")
	}
	if start, id, ok := mgScheduledCursorParse(mgScheduledCursor(v.now+1, strings.Repeat("a", 64))); !ok || start != v.now+1 || id != strings.Repeat("a", 64) {
		t.Fatal("cursor round-trip", start, id, ok)
	}
}

func mgScheduledCursorMatches(t *testing.T, cursor, id string) bool {
	t.Helper()
	_, parsed, ok := mgScheduledCursorParse(cursor)
	return ok && parsed == id
}

func mgReconcileResultIdsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

func TestMGScheduledReconcileAuthorityAndCandidateCells(t *testing.T) {
	denied := func(t *testing.T, name string, got mgReconcileAcceptance, err error, want error) {
		t.Helper()
		if !errors.Is(err, want) || !mgReconcileAcceptanceZero(got) {
			t.Fatal(name, got, err)
		}
	}
	reject := func(t *testing.T, w *executor, v mgReconcileValues, request mgReconcileScheduledRequest, witness *usOwnerAuthorityProof, want error) {
		t.Helper()
		turn := mgReconcileTurn(t, w, MGScheduledReconcile)
		reference := mgReconcileScheduledReference(t, mgPersonalIdentity(1, 1), request)
		got, err := acceptMGScheduledReconcile(t.Context(), turn, mgPersonalFake(t, v.ruleValues.personal), witness, t.Context(), reference, request)
		denied(t, "refusal", got, err, want)
		if _, rerr := turn.transaction.rollback(nil); rerr != nil {
			t.Fatal(rerr)
		}
	}

	t.Run("live-rule", func(t *testing.T) {
		w, _, v := mgReconcileFixture(t)
		mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "discover", "", 7, 1, v.now+10000, 1, 3, v.now)
		reject(t, w, v, v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule)), v.ruleValues.proof, ErrConflict)
	})
	t.Run("stale-source", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		request := v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule))
		request.sourceRevision++
		reject(t, w, v, request, v.ruleValues.proof, ErrConflict)
	})
	t.Run("stale-rule-revision", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		request := v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule))
		request.ruleRevision++
		reject(t, w, v, request, v.ruleValues.proof, ErrConflict)
	})
	t.Run("changed-generation", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		request := v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule))
		request.generation = strings.Repeat("c", 48)
		reject(t, w, v, request, v.ruleValues.proof, ErrConflict)
	})
	t.Run("stale-work-revision", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		request := v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule))
		request.before.jobRevision--
		reject(t, w, v, request, v.ruleValues.proof, ErrConflict)
	})
	t.Run("expired-lease", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "discover", "", 7, 1, v.now-1, 1, 3, v.now)
		reject(t, w, v, v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule)), v.ruleValues.proof, ErrConflict)
	})
	t.Run("foreign-owner", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		request := v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule))
		request.owner.profile = "another-profile"
		reject(t, w, v, request, v.ruleValues.proof, ErrDisclosureDenied)
	})
	t.Run("stale-member-revision", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		request := v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule))
		request.owner.memberRevision = 2
		reject(t, w, v, request, v.ruleValues.proof, ErrDisclosureDenied)
	})
	t.Run("forged-witness", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		forged := *v.ruleValues.proof
		forged.tokenExpiry = forged.tokenExpiry.Add(48 * time.Hour)
		reject(t, w, v, v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule)), &forged, ErrDisclosureDenied)
	})
	t.Run("cancelled-lifetime", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		lifetime, stop := context.WithCancel(t.Context())
		stop()
		request := v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule))
		turn := mgReconcileTurn(t, w, MGScheduledReconcile)
		got, err := acceptMGScheduledReconcile(t.Context(), turn, mgPersonalFake(t, v.ruleValues.personal), v.ruleValues.proof, lifetime,
			mgReconcileScheduledReference(t, mgPersonalIdentity(1, 1), request), request)
		denied(t, "cancelled lifetime", got, err, context.Canceled)
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("candidate-custody-is-skipped", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		// A launched occurrence is not a future intent candidate; neither is an
		// already cancelled row. Both keep their rule link and are never counted.
		launched := mgReconcileIntent(t, w, v, 1, v.now+3600000, "", 0)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET capture_launch_id=zeroblob(16) WHERE id=?`, launched); err != nil {
			t.Fatal(err)
		}
		cancelled := mgReconcileIntent(t, w, v, 2, v.now+7200000, "", 0)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='cancelled',cancel_generation=1 WHERE id=?`, cancelled); err != nil {
			t.Fatal(err)
		}
		live := mgReconcileIntent(t, w, v, 3, v.now+10800000, "", 0)
		out, _ := mgScheduledAccept(t, w, v, mgPersonalIdentity(1, 1), v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule)))
		if !mgReconcileResultIdsEqual(out.result.ids, []string{live}) {
			t.Fatal("candidate page", out.result.ids)
		}
		if _, rule, _, _, _, _ := mgReconcileIntentRow(t, w.db, launched); rule != v.rule {
			t.Fatal("launched custody was detached")
		}
		if _, rule, _, _, _, _ := mgReconcileIntentRow(t, w.db, cancelled); rule != v.rule {
			t.Fatal("cancelled row was detached")
		}
	})
	t.Run("full-frame", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		fake := mgPersonalFake(t, v.ruleValues.personal)
		fullBefore := mgReconcileStoredWork(t, w, v.rule)
		full, err := w.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = full.rollback(nil) })
		full.descriptor.ID = MGScheduledReconcile
		defs := mgReconcileTurnDefinitions()
		registry, err := stmttest.New(t, defs)
		if err != nil {
			t.Fatal(err)
		}
		var steps []statementStep
		for _, d := range defs {
			steps = append(steps, statementStep{d.ID, Work{64, 200000, 64, 4 << 20, time.Microsecond}})
		}
		fullTurn, err := newCommandTurn(full, registry, steps)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fullTurn.closeRows() })
		request := v.scheduledRequest("keep", 7, fullBefore)
		got, err := acceptMGScheduledReconcile(t.Context(), fullTurn, fake, v.ruleValues.proof, t.Context(),
			mgReconcileScheduledReference(t, mgPersonalIdentity(1, 1), request), request)
		denied(t, "FULL frame", got, err, ErrInvalid)
	})
}

// A lost COMMIT reply is never success: the frame is fenced, and a FRESH
// read-only connection shows either the whole before- or after-image.
func TestMGScheduledReconcileUnknownCommitFreshReadback(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_commit", true: "after_commit"}[after], func(t *testing.T) {
			w, path, v := mgScheduledFixture(t)
			intent := mgReconcileIntent(t, w, v, 1, v.now+3600000, "", 0)
			request := v.scheduledRequest("cancel", 7, mgReconcileStoredWork(t, w, v.rule))
			reference := mgReconcileScheduledReference(t, mgPersonalIdentity(1, 1), request)
			fake := mgPersonalFake(t, v.ruleValues.personal)
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			installFaultPool(w, &faultConnector{Connector: connector, replyError: true, afterCommit: after})

			turn := mgReconcileTurn(t, w, MGScheduledReconcile)
			if _, err := acceptMGScheduledReconcile(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), reference, request); err != nil {
				t.Fatal("fresh command did not reach COMMIT", err)
			}
			_ = turn.closeRows()
			if _, err := turn.transaction.commit(); !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCommitReply) {
				t.Fatal("uncertain COMMIT reported success", err)
			}
			if !w.ambiguous || !w.fenced {
				t.Fatal("uncertain writer was not fenced")
			}
			db := mgPersonalReadOnly(t, path)
			state, rule, ruleRevision, manual, revision, cancelGeneration := mgReconcileIntentRow(t, db, intent)
			var workRevision, receipts, sequence int64
			if err := db.QueryRowContext(context.Background(), `SELECT (SELECT job_revision FROM dvr_rule_work WHERE rule_id=?),(SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, v.rule, v.ruleValues.root, v.ruleValues.root).
				Scan(&workRevision, &receipts, &sequence); err != nil {
				t.Fatal(err)
			}
			if after {
				if state != "cancelled" || rule != v.rule || ruleRevision != 1 || manual != 0 || revision != 2 || cancelGeneration != 1 ||
					workRevision != 4 || receipts != 1 || sequence != 1 {
					t.Fatal("committed frame is not whole", state, rule, ruleRevision, manual, revision, cancelGeneration, workRevision, receipts, sequence)
				}
			} else if state != "scheduled" || rule != v.rule || revision != 1 || workRevision != 3 || receipts != 0 || sequence != 0 {
				t.Fatal("uncommitted frame leaked", state, rule, revision, workRevision, receipts, sequence)
			}
		})
	}
}

var _ = sql.ErrNoRows
