package writer

import (
	"strings"
	"testing"
)

func TestUOPersonalBulkExactMembersOutcomeAndCursor(t *testing.T) {
	w, target := uoPersonalFixture(t)
	uoPersonalContainer(t, w)
	q := uoPersonalBulkBeginInput{policy: target.policy, job: "job-one", operation: "bulk-one", requestHash: strings.Repeat("a", 64), field: uoPersonalWatchlist, value: "1", subjects: []int64{2111, 2112}, nowMS: 1}
	turn := uoPersonalTurn(t, w)
	b, err := applyUOPersonalBulkBegin(t.Context(), turn, q)
	if err != nil || b.total != 2 || b.revision != 1 {
		t.Fatal(b, err)
	}
	uoFlagsCommit(t, turn)
	// COMPONENT ONLY parent lease-claim stand-in. Operational lease transitions
	// remain in parent's uo_jobs; no separate scheduler/worker is installed.
	wc008NativeExec(t, w, `UPDATE personal_jobs SET job_lease_owner=9,job_lease_generation=1,job_lease_expires_ms=1000 WHERE id='job-one'`)
	step := uoPersonalBulkStepInput{target: target, job: q.job, field: q.field, value: q.value, revision: 1, owner: 9, generation: 1, nowMS: 2}
	turn = uoPersonalTurn(t, w)
	one, err := applyUOPersonalBulkStep(t.Context(), turn, step)
	if err != nil || one.done != 1 || one.cursor != 1 || one.terminal {
		t.Fatal(one, err)
	}
	uoFlagsCommit(t, turn)
	turn = uoPersonalTurn(t, w)
	bad, err := applyUOPersonalBulkStep(t.Context(), turn, step)
	if err == nil || bad != (uoPersonalBulkEffect{}) {
		t.Fatal("stale cursor repeated effects", bad, err)
	}
	uoPersonalRollback(t, turn)
	wc008NativeExec(t, w, `UPDATE catalog_entities SET retired=1 WHERE id=2112`)
	step.revision = 2
	step.cursor = 1
	step.target.subjectID = 2112
	step.target.publicID[15] = 1
	step.target.container = true
	turn = uoPersonalTurn(t, w)
	two, err := applyUOPersonalBulkStep(t.Context(), turn, step)
	if err != nil || two.done != 1 || two.failed != 1 || two.cursor != 2 || !two.terminal {
		t.Fatal(two, err)
	}
	uoFlagsCommit(t, turn)
	var state string
	var outcomes, failures, members int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT state,(SELECT count(*) FROM personal_job_membership_outcomes),(SELECT count(*) FROM personal_job_failures),(SELECT count(*) FROM personal_job_items) FROM personal_jobs WHERE id='job-one'`).Scan(&state, &outcomes, &failures, &members); err != nil || state != "partial" || outcomes != 2 || failures != 1 || members != 2 {
		t.Fatal(state, outcomes, failures, members, err)
	}
}
func TestUOPersonalBulkRejectsDuplicateSelection(t *testing.T) {
	w, target := uoPersonalFixture(t)
	q := uoPersonalBulkBeginInput{policy: target.policy, job: "job-one", operation: "bulk-one", requestHash: strings.Repeat("a", 64), field: uoPersonalFavorite, value: "1", subjects: []int64{2111, 2111}}
	turn := uoPersonalTurn(t, w)
	e, err := applyUOPersonalBulkBegin(t.Context(), turn, q)
	if err == nil || e != (uoPersonalBulkEffect{}) {
		t.Fatal(e, err)
	}
	uoPersonalRollback(t, turn)
}
