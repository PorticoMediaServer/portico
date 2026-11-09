package writer

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// MG-12 reuses MG-11's real embedded A/B/G+013 fixture (US owner session,
// lifetime root, one live source head) and adds only this task's native rule,
// rule-work and scheduled-intent rows. No draft overlay, no dropped table.
type mgReconcileValues struct {
	rule       string
	now        int64
	revision   int64
	ruleValues mgRuleCommandValues
}

func (v mgReconcileValues) owner() mgRecordingOwner { return v.ruleValues.owner }

// mgReconcileFixture writes one live native rule plus its work row exactly as
// MG-11's save does (revision 1, phase existing, cursor empty, due now).
func mgReconcileFixture(t *testing.T) (*executor, string, mgReconcileValues) {
	t.Helper()
	w, path, ruleValues := mgRuleCommandFixture(t)
	now := w.now().UnixMilli()
	rule := strings.Repeat("e", 64)
	config := mgRuleValuesConfig(ruleValues, nil)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_rules(id,owner_key,authority,account_id,profile_id,source_id,series_id,match_title,match_channel,revision,config_json,enabled,deleted,updated_ms) VALUES(?,?,'local',?,?,?,'series-one','','',1,?,1,0,?)`,
		rule, ruleValues.owner.key(), ruleValues.owner.account, ruleValues.owner.profile, ruleValues.source, mgRulePreparedWire(config), now); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_rule_work(rule_id,rule_revision,generation,phase,cursor,updated_ms,diagnostic,job_next_attempt_ms) VALUES(?,1,?,'existing','',?,'',?)`,
		rule, ruleValues.generation, now, now); err != nil {
		t.Fatal(err)
	}
	return w, path, mgReconcileValues{rule: rule, now: now, revision: 1, ruleValues: ruleValues}
}

// retireMGReconcileRule marks the native rule disabled and deleted at revision
// two, exactly as MG-11's accepted delete leaves it (work reset to existing,
// live lease/attempt history preserved).
func retireMGReconcileRule(t *testing.T, w *executor, v *mgReconcileValues) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_rules SET enabled=0,deleted=1,revision=2 WHERE id=?`, v.rule); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_rule_work SET rule_revision=2 WHERE rule_id=?`, v.rule); err != nil {
		t.Fatal(err)
	}
	v.revision = 2
}

// mgReconcileSeedWork writes one exact work row version. Values are those a
// real claim/renew/advance would have produced; tests that need a held lease
// start here instead of re-running 826.
func mgReconcileSeedWork(t *testing.T, w *executor, rule, generation, phase, cursor string, owner, leaseGeneration, expires, attempts, jobRevision int64, due any) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_rule_work SET generation=?,phase=?,cursor=?,job_lease_owner=?,job_lease_generation=?,job_lease_expires_ms=?,job_attempts=?,job_next_attempt_ms=?,job_revision=? WHERE rule_id=?`,
		generation, phase, cursor, owner, leaseGeneration, expires, attempts, due, jobRevision, rule); err != nil {
		t.Fatal(err)
	}
}

func mgReconcileStoredWork(t *testing.T, w *executor, rule string) mgReconcileWorkFacts {
	t.Helper()
	var f mgReconcileWorkFacts
	var due sql.NullInt64
	if err := w.db.QueryRowContext(t.Context(), `SELECT rule_id,rule_revision,generation,phase,cursor,updated_ms,diagnostic,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision FROM dvr_rule_work WHERE rule_id=?`, rule).
		Scan(&f.rule, &f.ruleRevision, &f.generation, &f.phase, &f.cursor, &f.updated, &f.diagnostic, &f.jobOwner, &f.leaseGeneration, &f.expires, &f.attempts, &due, &f.jobRevision); err != nil {
		t.Fatal(err)
	}
	if due.Valid {
		f.dueMS, f.hasDue = due.Int64, true
	}
	return f
}

func mgReconcileTurnDefinitions() []stmt.Definition {
	defs := mgPersonalRootDefinitions()
	defs = append(defs, mgRuleDefinitions()...)
	defs = append(defs, mgReconcileWorkDefinitions()...)
	return append(defs, mgScheduledReconcileDefinitions()...)
}

func mgReconcileTurn(t *testing.T, w *executor, purpose CommandID) *commandTurn {
	t.Helper()
	defs := mgReconcileTurnDefinitions()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{
		limit:    Work{4096, 10_000_000, 4096, 256 << 20, 25 * time.Millisecond},
		setup:    Work{8, 1000, 8, 8192, time.Millisecond},
		terminal: Work{8, 1000, 8, 8192, time.Millisecond},
	}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{64, 200000, 64, 4 << 20, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_ = turn.closeRows()
			_, _ = tx.rollback(nil)
		}
	})
	return turn
}

func (v mgReconcileValues) ruleRequest(mode string, worker, leaseUntil int64, before mgReconcileWorkFacts, page mgReconcileWorkPage) mgReconcileRuleRequest {
	return mgReconcileRuleRequest{
		owner: v.owner(), rule: v.rule, ruleRevision: v.revision, sourceRevision: v.ruleValues.sourceRevision,
		generation: v.ruleValues.generation, mode: mode, worker: worker, leaseUntil: leaseUntil, before: before, page: page,
	}
}

func (v mgReconcileValues) scheduledRequest(policy string, worker int64, before mgReconcileWorkFacts) mgReconcileScheduledRequest {
	return mgReconcileScheduledRequest{
		owner: v.owner(), rule: v.rule, ruleRevision: v.revision, sourceRevision: v.ruleValues.sourceRevision,
		generation: v.ruleValues.generation, policy: policy, worker: worker, before: before,
	}
}

func mgReconcileResultEqual(a, b mgReconcileResult) bool {
	if a.variant != b.variant || a.rule != b.rule || a.owner != b.owner || a.generation != b.generation ||
		a.phase != b.phase || a.cursor != b.cursor || a.ruleRevision != b.ruleRevision ||
		a.workRevision != b.workRevision || a.jobOwner != b.jobOwner || a.jobGeneration != b.jobGeneration ||
		a.jobExpires != b.jobExpires || a.attempts != b.attempts || a.dueMS != b.dueMS || a.hasDue != b.hasDue ||
		a.pending != b.pending || a.units != b.units || a.ownerRevision != b.ownerRevision || len(a.ids) != len(b.ids) {
		return false
	}
	for n := range a.ids {
		if a.ids[n] != b.ids[n] {
			return false
		}
	}
	return true
}

func mgScheduledFactsZero(f mgScheduledReconcileFacts) bool {
	return f.work == (mgReconcileWorkFacts{}) && len(f.ids) == 0 && f.ownerRevision == 0 && !f.pending
}

func mgReconcileAcceptanceZero(a mgReconcileAcceptance) bool {
	return a.digest == (ResultDigest{}) && mgReconcileResultEqual(a.result, mgReconcileResult{})
}

func ackMGReconcile(t *testing.T, w *executor, root string, sequence int64) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=? WHERE root_id=? AND last_command_sequence=?`, sequence, root, sequence); err != nil {
		t.Fatal(err)
	}
}

func mgReconcileRuleReference(t *testing.T, id CommandIdentity, r mgReconcileRuleRequest) ReceiptReference {
	t.Helper()
	c, err := newMGReconcileRuleCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

func mgReconcileScheduledReference(t *testing.T, id CommandIdentity, r mgReconcileScheduledRequest) ReceiptReference {
	t.Helper()
	c, err := newMGReconcileScheduledCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

// mgReconcileIntent inserts one future scheduled intent of the rule plus its
// reservation and recording dependency. A non-empty artifact keeps that
// dependency on cancel (physical/logical debt is never orphaned here).
func mgReconcileIntent(t *testing.T, w *executor, v mgReconcileValues, n int, startMS int64, artifact string, item int64) string {
	t.Helper()
	id := fmt.Sprintf("%064x", n)
	channel, programme := fmt.Sprintf("%064x", 0xc000+n), fmt.Sprintf("%064x", 0xd000+n)
	begin, end := startMS, startMS+3600000
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,rule_id,rule_revision,manual,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,artifact_id,item_id,guide_pin_generation)
VALUES(?,?,'local',?,?,?,?,?,?,'{}',?,1,0,'{}',?,?,5,1,'scheduled',?,?,zeroblob(16),zeroblob(16),zeroblob(32),?,?,?,?,?)`,
		id, v.owner().key(), v.owner().account, v.owner().profile, v.ruleValues.source, channel, programme, v.ruleValues.generation,
		v.rule, begin, end, begin, begin, v.owner().memberIncarnation, v.owner().profileLifetime, artifact, item, v.ruleValues.generation); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_reservations(id,source_id,start_ms,end_ms,priority,enabled) VALUES(?,?,?,?,5,1)`,
		id, v.ruleValues.source, begin, end); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_source_dependencies(source_id,kind,id) VALUES(?,'recording',?)`, v.ruleValues.source, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mgReconcileIntentRow(t *testing.T, db *sql.DB, id string) (state, rule string, ruleRevision, manual, revision, cancelGeneration int64) {
	t.Helper()
	if err := db.QueryRowContext(t.Context(), `SELECT state,rule_id,rule_revision,manual,revision,cancel_generation FROM dvr_recordings WHERE id=?`, id).
		Scan(&state, &rule, &ruleRevision, &manual, &revision, &cancelGeneration); err != nil {
		t.Fatal(err)
	}
	return
}

func mgReconcileReservationEnabled(t *testing.T, db *sql.DB, id string) int64 {
	t.Helper()
	var enabled int64
	if err := db.QueryRowContext(t.Context(), `SELECT enabled FROM live_reservations WHERE id=?`, id).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	return enabled
}

func mgReconcileDependencies(t *testing.T, db *sql.DB, source string) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_source_dependencies WHERE source_id=? AND kind='recording'`, source).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
