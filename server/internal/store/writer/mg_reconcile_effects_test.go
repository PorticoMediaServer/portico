package writer

import (
	"errors"
	"strings"
	"testing"
)

// The private effects refuse a foreign, FULL, unverified or finished frame even
// when called directly; the accepting parent's purpose check is not the only
// fence.
func TestMGReconcileEffectPurposeFence(t *testing.T) {
	w, _, v := mgReconcileFixture(t)
	before := mgReconcileStoredWork(t, w, v.rule)
	request := v.ruleRequest("claim", 7, v.now+10000, before, mgReconcileWorkPage{})
	for _, mode := range []string{"wrong-purpose", "unverified-policy"} {
		t.Run(mode, func(t *testing.T) {
			turn := mgReconcileTurn(t, w, MGRuleWorkReconcile)
			if mode == "wrong-purpose" {
				turn.transaction.descriptor.ID = MGSaveRule
			} else {
				turn.transaction.policyVerified = false
			}
			out, err := mgReconcileRuleWork(t.Context(), turn, request)
			if !errors.Is(err, ErrInvalid) || out != (mgReconcileWorkFacts{}) {
				t.Fatal(out, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if stored := mgReconcileStoredWork(t, w, v.rule); stored != before {
				t.Fatal("refused effect changed work")
			}
		})
	}

	w2, _, v2 := mgScheduledFixture(t)
	intent := mgReconcileIntent(t, w2, v2, 1, v2.now+3600000, "", 0)
	scheduledBefore := mgReconcileStoredWork(t, w2, v2.rule)
	scheduledRequest := v2.scheduledRequest("keep", 7, scheduledBefore)
	for _, mode := range []string{"wrong-purpose", "unverified-policy"} {
		t.Run("scheduled/"+mode, func(t *testing.T) {
			turn := mgReconcileTurn(t, w2, MGScheduledReconcile)
			if mode == "wrong-purpose" {
				turn.transaction.descriptor.ID = MGRuleWorkReconcile
			} else {
				turn.transaction.policyVerified = false
			}
			out, err := mgScheduledReconcile(t.Context(), turn, scheduledRequest)
			if !errors.Is(err, ErrInvalid) || !mgScheduledFactsZero(out) {
				t.Fatal(out, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if stored := mgReconcileStoredWork(t, w2, v2.rule); stored != scheduledBefore {
				t.Fatal("refused effect changed work")
			}
			if _, rule, _, _, revision, _ := mgReconcileIntentRow(t, w2.db, intent); rule != v2.rule || revision != 1 {
				t.Fatal("refused effect changed intent")
			}
		})
	}
}

// A divergent readback after the work CAS refuses the whole frame; the exact
// 13-column CAS is not success on its own.
func TestMGReconcileWorkReadbackDivergence(t *testing.T) {
	w, _, v := mgReconcileFixture(t)
	before := mgReconcileStoredWork(t, w, v.rule)
	request := v.ruleRequest("claim", 7, v.now+10000, before, mgReconcileWorkPage{})
	turn := mgReconcileTurn(t, w, MGRuleWorkReconcile)
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `CREATE TEMP TRIGGER mg_reconcile_work_mismatch AFTER UPDATE ON dvr_rule_work BEGIN UPDATE dvr_rule_work SET diagnostic='changed' WHERE rule_id=NEW.rule_id; END`); err != nil {
		t.Fatal(err)
	}
	out, err := mgReconcileRuleWork(t.Context(), turn, request)
	if err == nil || out != (mgReconcileWorkFacts{}) {
		t.Fatal("divergent readback accepted", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if stored := mgReconcileStoredWork(t, w, v.rule); stored != before {
		t.Fatal("divergent frame leaked", stored)
	}
}

// A readback that disagrees with the exact expected edited intent also refuses
// the whole page: the detach CAS is not its own success.
func TestMGScheduledReconcileReadbackDivergence(t *testing.T) {
	w, _, v := mgScheduledFixture(t)
	intent := mgReconcileIntent(t, w, v, 1, v.now+3600000, "", 0)
	before := mgReconcileStoredWork(t, w, v.rule)
	request := v.scheduledRequest("keep", 7, before)
	turn := mgReconcileTurn(t, w, MGScheduledReconcile)
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `CREATE TEMP TRIGGER mg_reconcile_intent_mismatch AFTER UPDATE ON dvr_recordings BEGIN UPDATE dvr_recordings SET updated_ms=updated_ms+1 WHERE id=NEW.id; END`); err != nil {
		t.Fatal(err)
	}
	out, err := mgScheduledReconcile(t.Context(), turn, request)
	if err == nil || !mgScheduledFactsZero(out) {
		t.Fatal("divergent readback accepted", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if stored := mgReconcileStoredWork(t, w, v.rule); stored != before {
		t.Fatal("divergent frame leaked work")
	}
	if _, rule, _, _, revision, _ := mgReconcileIntentRow(t, w.db, intent); rule != v.rule || revision != 1 {
		t.Fatal("divergent frame leaked intent")
	}
}

// Candidate matching is exact: a shared occurrence is not a future intent, a
// row that claims the rule but belongs to another owner refuses, and a
// reservation that is not the exact enabled window refuses the cancel page.
func TestMGScheduledReconcileCandidateMatching(t *testing.T) {
	t.Run("shared-row-skipped", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		leader := mgReconcileIntent(t, w, v, 1, v.now+3600000, "", 0)
		follower := mgReconcileIntent(t, w, v, 2, v.now+7200000, "", 0)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET has_dvr_recording_shares=1,share_capture_recording_id=?,share_capture_claim_generation=1,share_capture_launch_id=zeroblob(16) WHERE id=?`, leader, follower); err != nil {
			t.Fatal(err)
		}
		live := mgReconcileIntent(t, w, v, 3, v.now+10800000, "", 0)
		out, _ := mgScheduledAccept(t, w, v, mgPersonalIdentity(1, 1), v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule)))
		if !mgReconcileResultIdsEqual(out.result.ids, []string{leader, live}) {
			t.Fatal("shared candidate page", out.result.ids)
		}
		if _, rule, _, _, _, _ := mgReconcileIntentRow(t, w.db, follower); rule != v.rule {
			t.Fatal("shared follower was detached")
		}
	})

	t.Run("foreign-candidate-refused", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		id := mgReconcileIntent(t, w, v, 1, v.now+3600000, "", 0)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET owner_key=? WHERE id=?`, strings.Repeat("9", 64), id); err != nil {
			t.Fatal(err)
		}
		request := v.scheduledRequest("keep", 7, mgReconcileStoredWork(t, w, v.rule))
		turn := mgReconcileTurn(t, w, MGScheduledReconcile)
		got, err := acceptMGScheduledReconcile(t.Context(), turn, mgPersonalFake(t, v.ruleValues.personal), v.ruleValues.proof, t.Context(),
			mgReconcileScheduledReference(t, mgPersonalIdentity(1, 1), request), request)
		if !errors.Is(err, ErrConflict) || got.decision != 0 {
			t.Fatal("foreign candidate accepted", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("reservation-window-refused", func(t *testing.T) {
		w, _, v := mgScheduledFixture(t)
		id := mgReconcileIntent(t, w, v, 1, v.now+3600000, "", 0)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE live_reservations SET end_ms=end_ms+1 WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		request := v.scheduledRequest("cancel", 7, mgReconcileStoredWork(t, w, v.rule))
		turn := mgReconcileTurn(t, w, MGScheduledReconcile)
		got, err := acceptMGScheduledReconcile(t.Context(), turn, mgPersonalFake(t, v.ruleValues.personal), v.ruleValues.proof, t.Context(),
			mgReconcileScheduledReference(t, mgPersonalIdentity(1, 1), request), request)
		if !errors.Is(err, ErrConflict) || got.decision != 0 {
			t.Fatal("foreign reservation window accepted", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})
}

// The saved scalar codec is closed: every inconsistent shape is refused before
// a payload exists, and a tampered payload never decodes to the original.
func TestMGReconcileResultCodec(t *testing.T) {
	id, request := mgPersonalIdentity(1, 1), RequestDigest{1}
	hexID, hexOwner, hexGeneration := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 48)
	rule := mgReconcileResult{
		variant: mgReconcileResultVariant, rule: hexID, owner: hexOwner, generation: hexGeneration,
		phase: "discover", cursor: "c1", ruleRevision: 1, workRevision: 2, jobOwner: 7, jobGeneration: 1,
		jobExpires: 1000, attempts: 1, dueMS: 500, hasDue: true, pending: true, units: 3,
	}
	scheduled := mgReconcileResult{
		variant: mgScheduledResultVariant, rule: hexID, owner: hexOwner, generation: hexGeneration,
		phase: "idle", ruleRevision: 2, workRevision: 4, jobGeneration: 1, attempts: 1,
		units: 2, ownerRevision: 1, ids: []string{strings.Repeat("d", 64), strings.Repeat("e", 64)},
	}
	for _, want := range []mgReconcileResult{rule, scheduled} {
		payload, digest, err := mgReconcilePayload(id, request, want)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeMGReconcile(id, request, payload, digest)
		if err != nil || !mgReconcileResultEqual(got, want) {
			t.Fatal(got, err)
		}
		tampered := append([]byte{}, payload...)
		tampered[len(tampered)-1] ^= 0xff
		if _, err := decodeMGReconcile(id, request, tampered, digest); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("tampered payload decoded", err)
		}
		if _, err := decodeMGReconcile(id, request, payload, ResultDigest{1}); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("foreign digest decoded", err)
		}
	}
	mutate := func(base mgReconcileResult, edit func(*mgReconcileResult)) mgReconcileResult {
		out := base
		out.ids = append([]string{}, base.ids...)
		edit(&out)
		return out
	}
	for _, bad := range []mgReconcileResult{
		mutate(rule, func(v *mgReconcileResult) { v.variant = "other" }),
		mutate(rule, func(v *mgReconcileResult) { v.phase, v.pending = "idle", false }),
		mutate(rule, func(v *mgReconcileResult) { v.hasDue = false }),
		mutate(rule, func(v *mgReconcileResult) { v.ownerRevision, v.ids = 1, []string{hexID} }),
		mutate(rule, func(v *mgReconcileResult) { v.units = 9 }),
		mutate(rule, func(v *mgReconcileResult) { v.workRevision = 0 }),
		mutate(scheduled, func(v *mgReconcileResult) { v.units = 1 }),
		mutate(scheduled, func(v *mgReconcileResult) { v.ownerRevision = 0 }),
		mutate(scheduled, func(v *mgReconcileResult) { v.phase, v.pending, v.cursor = "discover", true, "" }),
		mutate(scheduled, func(v *mgReconcileResult) { v.ids[0] = "short" }),
	} {
		if _, _, err := mgReconcilePayload(id, request, bad); err == nil {
			t.Fatal("invalid result accepted", bad)
		}
	}
}
