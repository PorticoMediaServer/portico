package writer

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func mgLCDispatchTurn(t *testing.T, w *executor, purpose CommandID) *commandTurn {
	t.Helper()
	defs := mgLCDispatchDefinitions()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{10000, 10000000, 10000, 64 << 20, 25 * time.Millisecond},
		setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_, _ = tx.rollback(nil)
		}
	})
	steps := make([]statementStep, 0, len(defs))
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 64, 1 << 20, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

// mgLCNow reads the immediate native continuation from the retained owner. The
// dispatch frame itself always re-reads this row; tests never pass it in.
func mgLCNow(t *testing.T, w *executor, b mgLCBuild) mgLCProgress {
	t.Helper()
	turn := mgLCTurn(t, w)
	p, err := readMGLCOwner(t.Context(), turn, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	return p
}

// One dispatch effect, commit, ACK and the next native continuation.
func mgLCDispatchApply(t *testing.T, w *executor, b mgLCBuild, p mgLCProgress, purpose CommandID, in mgLCDispatchInput) (mgLCSaved, mgLCProgress) {
	t.Helper()
	turn := mgLCDispatchTurn(t, w, purpose)
	saved, decision, err := mgLCDispatch(t.Context(), turn, b, mgLCNext(b, p), purpose, in)
	if err != nil {
		t.Fatal(purpose, err)
	}
	if decision != receiptFresh {
		t.Fatal("fresh command did not resolve fresh", decision)
	}
	mgLCCommit(t, turn)
	p = mgLCNow(t, w, b)
	turn = mgLCTurn(t, w)
	ack, err := acknowledgeMGLC(t.Context(), turn, b, saved.command, saved.kind, saved.digest)
	if err != nil {
		t.Fatal(err)
	}
	mgLCCommit(t, turn)
	p.ack = ack
	return saved, p
}

func mgLCDispatchCandidates(t *testing.T, w *executor, b mgLCBuild, p mgLCProgress) mgLCProgress {
	t.Helper()
	_, p = mgLCDispatchApply(t, w, b, p, MGLCStageCandidates, mgLCDispatchInput{
		candidates: []mgLCCandidate{mgLCCandidateFixture()}, next: "id:101", scanned: 1})
	_, p = mgLCDispatchApply(t, w, b, p, MGLCFinishCandidates, mgLCDispatchInput{end: true})
	return p
}

func mgLCDispatchEntries(t *testing.T, w *executor, b mgLCBuild, p mgLCProgress) mgLCProgress {
	t.Helper()
	_, p = mgLCDispatchApply(t, w, b, p, MGLCStageEntries, mgLCDispatchInput{
		slots: []mgLCDispatchSlot{
			{occurrence: "first", rule: "rule", block: "default", item: 101, start: 1000, end: 21000, offset: 5000},
			{occurrence: "second", rule: "rule", block: "default", item: 0, start: 21000, end: 61000},
		},
		rules: []mgLCRuleState{{rule: "rule", draw: 2, cycle: 1, lastSort: "title:101", lastBlock: "default"}},
		used:  []mgLCUsed{{rule: "rule", item: 101, cycle: 0}},
	})
	return p
}

// The complete producer path accepts a continued source offset, proves the
// stored interval chain, seals and publishes under the old-head+config CAS, and
// replays the ORIGINAL receipt with zero new effects.
func TestMGLCDispatchPublishesOffsetScheduleAndReplays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b := mgLCFixture(t)
		p := mgLCDispatchCandidates(t, w, b, mgLCBeginFixture(t, w, b))
		p = mgLCDispatchEntries(t, w, b, p)
		for step := 0; step < 4 && p.phase == mgLCCompletePhase; step++ {
			_, p = mgLCDispatchApply(t, w, b, p, MGLCVerifyEntries, mgLCDispatchInput{})
		}
		if p.phase != mgLCVerifiedPhase || p.verified != 2 {
			t.Fatal("offset verification", p)
		}
		_, p = mgLCDispatchApply(t, w, b, p, MGLCSeal, mgLCDispatchInput{})
		turn := mgLCDispatchTurn(t, w, MGLCPublish)
		saved, decision, err := mgLCDispatch(t.Context(), turn, b, mgLCNext(b, p), MGLCPublish, mgLCDispatchInput{})
		if err != nil || decision != receiptFresh {
			t.Fatal("publish", decision, err)
		}
		mgLCCommit(t, turn)
		p = mgLCNow(t, w, b)
		if !saved.result.sealed || saved.result.status != "published" || saved.result.published <= 0 || p.phase != "published" {
			t.Fatal("published native result", saved.result, p)
		}
		var head string
		var oldEntries, pins int
		if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation,(SELECT count(*) FROM lc_entries WHERE generation_id='lc-old'),(SELECT count(*) FROM lc_playback_refs WHERE generation_id='lc-old') FROM lc_channels WHERE id='lc-channel'`).Scan(&head, &oldEntries, &pins); err != nil || head != b.generation || oldEntries != 1 || pins != 1 {
			t.Fatal("old head or retained reference moved", head, oldEntries, pins, err)
		}
		var offset int64
		var title string
		if err := w.db.QueryRowContext(t.Context(), `SELECT source_offset_ms,title FROM lc_entries WHERE generation_id=? AND occurrence_id='first'`, b.generation).Scan(&offset, &title); err != nil || offset != 5000 || title != "Immutable title" {
			t.Fatal("continued entry", offset, title, err)
		}
		var draw, cycle int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT draw,cycle FROM lc_rule_state WHERE generation_id=? AND rule_id='rule'`, b.generation).Scan(&draw, &cycle); err != nil || draw != 2 || cycle != 1 {
			t.Fatal("native rule state", draw, cycle, err)
		}
		var usedCycle int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT cycle FROM lc_used WHERE generation_id=? AND rule_id='rule' AND item_id=101`, b.generation).Scan(&usedCycle); err != nil || usedCycle != 0 {
			t.Fatal("native used mark", usedCycle, err)
		}
		// Current head/config changes cannot replace the original saved scalar.
		if _, err := w.db.ExecContext(t.Context(), `UPDATE lc_channels SET revision=8,enabled=0,active_generation='lc-old' WHERE id='lc-channel'`); err != nil {
			t.Fatal(err)
		}
		turn = mgLCDispatchTurn(t, w, MGLCPublish)
		var changes int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
			t.Fatal(err)
		}
		replayed, decision, err := mgLCDispatch(t.Context(), turn, b, saved.command, MGLCPublish, mgLCDispatchInput{})
		if err != nil || decision != receiptReplay || replayed != saved {
			t.Fatal("original replay", decision, replayed, err)
		}
		var after int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		if after != changes {
			t.Fatal("replay made effects", changes, after)
		}
		// ACK precedes retirement; the same command can never become success again.
		turn = mgLCTurn(t, w)
		ack, err := acknowledgeMGLC(t.Context(), turn, b, saved.command, saved.kind, saved.digest)
		if err != nil || ack != saved.command.identity.Sequence {
			t.Fatal("publish ACK", ack, err)
		}
		mgLCCommit(t, turn)
		turn = mgLCDispatchTurn(t, w, MGLCPublish)
		if got, _, err := mgLCDispatch(t.Context(), turn, b, saved.command, MGLCPublish, mgLCDispatchInput{}); !errors.Is(err, ErrDisclosureDenied) || got != (mgLCSaved{}) {
			t.Fatal("retired replay", got, err)
		}
	})
}

// A deleted middle occurrence cannot hide behind unchanged counters, and stale
// config, library or history authority refuses before any fresh effect.
func TestMGLCDispatchRejectsStoredHoleAndStaleAuthority(t *testing.T) {
	t.Run("missing-middle", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			w, b := mgLCFixture(t)
			p := mgLCDispatchCandidates(t, w, b, mgLCBeginFixture(t, w, b))
			p = mgLCDispatchEntries(t, w, b, p)
			if _, err := w.db.ExecContext(t.Context(), `DELETE FROM lc_entries WHERE generation_id='lc-new' AND occurrence_id='first'`); err != nil {
				t.Fatal(err)
			}
			turn := mgLCDispatchTurn(t, w, MGLCVerifyEntries)
			if got, _, err := mgLCDispatch(t.Context(), turn, b, mgLCNext(b, p), MGLCVerifyEntries, mgLCDispatchInput{}); !errors.Is(err, ErrConflict) || got != (mgLCSaved{}) {
				t.Fatal("counters hid a hole", got, err)
			}
		})
	})
	for name, mutate := range map[string]func(t *testing.T, w *executor, b mgLCBuild) mgLCProgress{
		"config": func(t *testing.T, w *executor, b mgLCBuild) mgLCProgress {
			if _, err := w.db.ExecContext(t.Context(), `UPDATE lc_channels SET revision=8 WHERE id='lc-channel'`); err != nil {
				t.Fatal(err)
			}
			return mgLCProgress{}
		},
		"catalogue": func(t *testing.T, w *executor, b mgLCBuild) mgLCProgress {
			p := mgLCBeginFixture(t, w, b)
			if _, err := w.db.ExecContext(t.Context(), `UPDATE libraries SET data_revision=12 WHERE id='lc-lib'`); err != nil {
				t.Fatal(err)
			}
			return p
		},
		"history": func(t *testing.T, w *executor, b mgLCBuild) mgLCProgress {
			return mgLCBeginFixture(t, w, b)
		},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w, b := mgLCFixture(t)
				p := mutate(t, w, b)
				build := b
				if name == "history" {
					build.history = 5
				}
				purpose := MGLCStageCandidates
				in := mgLCDispatchInput{candidates: []mgLCCandidate{mgLCCandidateFixture()}, next: "id:101", scanned: 1}
				if name == "config" {
					purpose = MGLCBegin
					in = mgLCDispatchInput{}
				}
				turn := mgLCDispatchTurn(t, w, purpose)
				if got, _, err := mgLCDispatch(t.Context(), turn, build, mgLCNext(build, p), purpose, in); !errors.Is(err, ErrConflict) && !errors.Is(err, ErrResolveRequired) || got != (mgLCSaved{}) {
					t.Fatal("stale authority accepted", name, got, err)
				}
			})
		})
	}
}

// A cancelled lifetime and a forged purpose/issuer never perform an effect.
func TestMGLCDispatchCancelledLifetimeAndForgedIssuer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b := mgLCFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		turn := mgLCDispatchTurn(t, w, MGLCBegin)
		if _, _, err := mgLCDispatch(ctx, turn, b, mgLCNext(b, mgLCProgress{}), MGLCBegin, mgLCDispatchInput{}); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled lifetime", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var owners int
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM lc_generations WHERE id='lc-new'`).Scan(&owners); err != nil || owners != 0 {
			t.Fatal("cancelled frame wrote", owners, err)
		}
		turn = mgLCDispatchTurn(t, w, MGLCBegin)
		forged := mgLCNext(b, mgLCProgress{})
		forged.identity.Incarnation[0] ^= 1
		if _, _, err := mgLCDispatch(t.Context(), turn, b, forged, MGLCBegin, mgLCDispatchInput{}); !errors.Is(err, ErrConflict) && !errors.Is(err, ErrInvalid) {
			t.Fatal("forged issuer", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		turn = mgLCDispatchTurn(t, w, MGLCStageCandidates)
		if _, _, err := mgLCDispatch(t.Context(), turn, b, mgLCNext(b, mgLCProgress{}), MGLCBegin, mgLCDispatchInput{}); !errors.Is(err, ErrInvalid) {
			t.Fatal("foreign purpose borrowed begin", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		turn = mgLCDispatchTurn(t, w, MGLCBegin)
		if _, _, err := mgLCDispatch(t.Context(), turn, b, mgLCNext(b, mgLCProgress{}), MGLCBegin, mgLCDispatchInput{slots: []mgLCDispatchSlot{{occurrence: "x", rule: "rule", block: "default", item: 1, start: 1, end: 2}}}); !errors.Is(err, ErrInvalid) {
			t.Fatal("begin accepted foreign input", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})
}

// A lost response is never success in memory: an independent fresh read-only
// connection sees the committed receipt, and the replay is effect-free.
func TestMGLCDispatchLostCommitObservedFresh(t *testing.T) {
	w, b := mgLCFixture(t)
	path := mgGuideDatabasePath(t, w)
	turn := mgLCDispatchTurn(t, w, MGLCBegin)
	c := mgLCNext(b, mgLCProgress{})
	saved, decision, err := mgLCDispatch(t.Context(), turn, b, c, MGLCBegin, mgLCDispatchInput{})
	if err != nil || decision != receiptFresh {
		t.Fatal(saved, decision, err)
	}
	mgLCCommit(t, turn)
	reader := mgGuideFreshReader(t, path)
	var kind string
	var digest []byte
	var sequence int64
	if err := reader.QueryRowContext(t.Context(), `SELECT command_kind,result_digest,command_sequence FROM lc_generation_command_receipts WHERE generation_id=? AND command_sequence=1`, b.generation).Scan(&kind, &digest, &sequence); err != nil {
		t.Fatal(err)
	}
	if kind != string(mgLCBeginKind) || len(digest) != 32 || sequence != 1 {
		t.Fatal("fresh receipt read", kind, len(digest), sequence)
	}
	var want ResultDigest
	copy(want[:], digest)
	if want != saved.digest {
		t.Fatal("fresh digest disagrees with the original result", want, saved.digest)
	}
	var incarnation []byte
	if err := reader.QueryRowContext(t.Context(), `SELECT command_incarnation FROM lc_generations WHERE id=?`, b.generation).Scan(&incarnation); err != nil || len(incarnation) != 16 {
		t.Fatal("fresh owner read", incarnation, err)
	}
	turn = mgLCDispatchTurn(t, w, MGLCBegin)
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	replay, decision, err := mgLCDispatch(t.Context(), turn, b, c, MGLCBegin, mgLCDispatchInput{})
	if err != nil || decision != receiptReplay || replay != saved {
		t.Fatal("lost response replay", replay, decision, err)
	}
	var after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if after != changes {
		t.Fatal("lost response replay made effects", changes, after)
	}
}

func mgLCDispatchProbeInput(purpose CommandID) mgLCDispatchInput {
	switch purpose {
	case MGLCStageCandidates:
		return mgLCDispatchInput{candidates: []mgLCCandidate{mgLCCandidateFixture()}, next: "id:101", scanned: 1}
	case MGLCStageEntries:
		return mgLCDispatchInput{slots: []mgLCDispatchSlot{{occurrence: "probe", rule: "rule", block: "default", item: 101, start: 1000, end: 2000}}}
	case MGLCFinishCandidates:
		return mgLCDispatchInput{end: true}
	}
	return mgLCDispatchInput{}
}

// Every accepting purpose retires its original saved result after ACK, and a
// cancelled lifetime refuses before an effect for every purpose. These are the
// replay/lifetime cells for all seven commands, not just publication.
func TestMGLCDispatchEveryAcceptedPurposeRetires(t *testing.T) {
	type acceptedStep struct {
		saved   mgLCSaved
		purpose CommandID
	}
	synctest.Test(t, func(t *testing.T) {
		w, b := mgLCFixture(t)
		var accepted []acceptedStep
		p := mgLCProgress{}
		for _, step := range []struct {
			purpose CommandID
			in      mgLCDispatchInput
		}{
			{MGLCBegin, mgLCDispatchInput{}},
			{MGLCStageCandidates, mgLCDispatchProbeInput(MGLCStageCandidates)},
			{MGLCFinishCandidates, mgLCDispatchProbeInput(MGLCFinishCandidates)},
			{MGLCStageEntries, mgLCDispatchInput{slots: []mgLCDispatchSlot{
				{occurrence: "first", rule: "rule", block: "default", item: 101, start: 1000, end: 31000},
				{occurrence: "second", rule: "rule", block: "default", item: 0, start: 31000, end: 61000},
			}}},
		} {
			saved, next := mgLCDispatchApply(t, w, b, p, step.purpose, step.in)
			accepted = append(accepted, acceptedStep{saved, step.purpose})
			p = next
		}
		for n := 0; n < 4 && p.phase == mgLCCompletePhase; n++ {
			saved, next := mgLCDispatchApply(t, w, b, p, MGLCVerifyEntries, mgLCDispatchInput{})
			accepted = append(accepted, acceptedStep{saved, MGLCVerifyEntries})
			p = next
		}
		for _, step := range []CommandID{MGLCSeal, MGLCPublish} {
			saved, next := mgLCDispatchApply(t, w, b, p, step, mgLCDispatchInput{})
			accepted = append(accepted, acceptedStep{saved, step})
			p = next
		}
		if p.phase != "published" || len(accepted) < 8 {
			t.Fatal("flow did not publish", p, len(accepted))
		}
		for index, step := range accepted {
			turn := mgLCDispatchTurn(t, w, step.purpose)
			if got, _, err := mgLCDispatch(t.Context(), turn, b, step.saved.command, step.purpose, mgLCDispatchProbeInput(step.purpose)); !errors.Is(err, ErrDisclosureDenied) || got != (mgLCSaved{}) {
				t.Fatal("retired purpose", index, step.purpose, got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			turn = mgLCDispatchTurn(t, w, step.purpose)
			if _, _, err := mgLCDispatch(ctx, turn, b, step.saved.command, step.purpose, mgLCDispatchProbeInput(step.purpose)); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled lifetime", index, step.purpose, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestMGLCDispatchDescriptorInventory(t *testing.T) {
	ds := mgLCDispatchDescriptors()
	if len(ds) != 7 {
		t.Fatal("dispatch descriptors", len(ds))
	}
	for _, d := range ds {
		got, err := Describe(d.ID)
		if err != nil || got != d {
			t.Fatal("registered purpose", d, got, err)
		}
		if d.ID < MGLCBegin || d.ID > MGLCSeal || d.Durability != Normal {
			t.Fatal("purpose range", d)
		}
	}
	if _, err := Describe(MGLCAcknowledge); err == nil {
		t.Fatal("ACK descriptor borrowed by the producer frame")
	}
}
