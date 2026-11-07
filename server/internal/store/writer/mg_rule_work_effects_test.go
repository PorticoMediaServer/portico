package writer

import (
	"context"
	"fmt"
	"testing"
)

func mgRuleWorkerFixture(t *testing.T) (*commandTurn, mgRuleWorkRequest) {
	t.Helper()
	w, r := mgRuleFixture(t)
	defs := mgRuleDefinitions()
	for _, d := range mgRuleWorkDefinitions() {
		if d.ID == mgRuleWorkerRead || d.ID == mgRuleWorkerCAS {
			defs = append(defs, d)
		}
	}
	turn := mgRecordingTestTurn(t, w, defs)
	if _, e := saveMGRule(t.Context(), turn, r); e != nil {
		t.Fatal(e)
	}
	b, e := mgRuleWorkReadNative(t.Context(), turn, r.after.id)
	if e != nil {
		t.Fatal(e)
	}
	// Private component frame only; accepting admission is parent-owned.
	turn.transaction.descriptor.ID = MGReconcileRule
	return turn, mgRuleWorkRequest{rule: r.after, sourceRevision: r.sourceRevision, before: b, worker: 7, leaseUntil: w.now().UnixMilli() + 10000}
}
func TestMGRuleWorkerProtocol(t *testing.T) {
	turn, r := mgRuleWorkerFixture(t)
	a, e := claimMGRuleWork(t.Context(), turn, r)
	if e != nil || a.owner != 7 || a.attempts != 1 || a.leaseGeneration != 1 {
		t.Fatal(a, e)
	}
	r.before = a
	r.leaseUntil += 1000
	a, e = renewMGRuleWork(t.Context(), turn, r)
	if e != nil {
		t.Fatal(e)
	}
	r.before = a
	a, e = advanceMGRuleWork(t.Context(), turn, r, mgRuleWorkResult{"existing", "record-8", 8})
	if e != nil || a.cursor != "record-8" || a.owner != 7 {
		t.Fatal(a, e)
	}
	r.before = a
	a, e = advanceMGRuleWork(t.Context(), turn, r, mgRuleWorkResult{"discover", "", 0})
	if e != nil || a.phase != "discover" {
		t.Fatal(a, e)
	}
	r.before = a
	a, e = advanceMGRuleWork(t.Context(), turn, r, mgRuleWorkResult{"idle", "", 0})
	if e != nil || a.phase != "idle" || a.due.Valid || a.owner != 0 || a.expires != 0 || a.leaseGeneration != r.before.leaseGeneration || a.attempts != r.before.attempts {
		t.Fatal("idle work retained due or changed claim history", a, e)
	}
}
func TestMGRuleWorkerControls(t *testing.T) {
	for _, control := range []string{"revision", "source", "generation", "disabled", "deleted", "stale", "expired", "retarget", "oversize", "cancel", "readback"} {
		t.Run(control, func(t *testing.T) {
			turn, r := mgRuleWorkerFixture(t)
			a, e := claimMGRuleWork(t.Context(), turn, r)
			if e != nil {
				t.Fatal(e)
			}
			r.before = a
			q := ""
			switch control {
			case "revision":
				q = `UPDATE dvr_rules SET revision=revision+1`
			case "source":
				r.sourceRevision++
			case "generation":
				q = `UPDATE dvr_rule_work SET generation='changed'`
			case "disabled":
				q = `UPDATE dvr_rules SET enabled=0`
			case "deleted":
				q = `UPDATE dvr_rules SET deleted=1,enabled=0`
			case "stale":
				r.before.jobRevision--
			case "expired":
				r.before.expires = turn.transaction.writer.now().UnixMilli()
				q = `UPDATE dvr_rule_work SET job_lease_expires_ms=` + fmt.Sprint(r.before.expires)
			case "retarget":
				r.worker++
			case "readback":
				q = `CREATE TEMP TRIGGER mg_worker_mismatch AFTER UPDATE ON dvr_rule_work BEGIN UPDATE dvr_rule_work SET diagnostic='changed' WHERE rule_id=NEW.rule_id; END`
			}
			if q != "" {
				if _, e := turn.transaction.conn.ExecContext(t.Context(), q); e != nil {
					t.Fatal(e)
				}
			}
			ctx := t.Context()
			if control == "cancel" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			units := 1
			if control == "oversize" {
				units = 9
			}
			out, e := advanceMGRuleWork(ctx, turn, r, mgRuleWorkResult{"existing", "next", units})
			if e == nil || out != (mgRuleWorkFacts{}) {
				t.Fatal(out, e)
			}
			if _, e := turn.transaction.rollback(nil); e != nil {
				t.Fatal(e)
			}
			var count int
			if e := turn.transaction.writer.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_rule_work`).Scan(&count); e != nil || count != 0 {
				t.Fatal(count, e)
			}
		})
	}
}

func TestMGRuleWorkerInvalidatedClaim(t *testing.T) {
	for _, q := range []string{
		`UPDATE dvr_rules SET revision=revision+1`,
		`UPDATE dvr_rules SET enabled=0`,
		`UPDATE dvr_rule_work SET job_lease_owner=99,job_lease_expires_ms=9223372036854775807`,
	} {
		t.Run(q, func(t *testing.T) {
			turn, r := mgRuleWorkerFixture(t)
			if _, e := turn.transaction.conn.ExecContext(t.Context(), q); e != nil {
				t.Fatal(e)
			}
			out, e := claimMGRuleWork(t.Context(), turn, r)
			if e == nil || out != (mgRuleWorkFacts{}) {
				t.Fatal(out, e)
			}
		})
	}
}
