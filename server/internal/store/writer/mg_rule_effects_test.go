package writer

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
	"strings"
	"testing"
	"time"
)

func mgRuleFixture(t *testing.T) (*executor, mgRuleRequest) {
	t.Helper()
	w, _, _ := testExecutor(t)
	o := mgRecordingOwner{"rule-account", "rule-profile", strings.Repeat("a", 32), strings.Repeat("b", 32), 7, 11, 13}
	r := mgRuleRequest{owner: o, sourceRevision: 3, generation: strings.Repeat("2", 48)}
	r.after = mgRuleFacts{id: strings.Repeat("f", 64), owner: o.key(), account: o.account, profile: o.profile, source: strings.Repeat("1", 48), series: "series-one", config: `{"description":"bounded canonical input"}`, revision: 1, enabled: 1, updated: 10}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,zeroblob(0),?,?)`, []any{o.account, o.account, o.profile, o.accountEpoch}},
		{`INSERT INTO direct_memberships(account_id,incarnation,role,revision) VALUES(?,?,'owner',?)`, []any{o.account, o.memberIncarnation, o.memberRevision}},
		{`INSERT INTO direct_profiles(id,account_id,name,lifetime,revision) VALUES(?,?,'Rule owner',?,?)`, []any{o.profile, o.account, o.profileLifetime, o.profileRevision}},
		{`INSERT INTO live_source_identities(id) VALUES(?)`, []any{r.after.source}},
		{`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES(?,?,'Guide',1,0,0,'2026-11-07T00:00:00Z')`, []any{r.generation, r.after.source}},
		{`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES(?,'Guide',3,'active',1,?,'2026-11-07T00:00:00Z')`, []any{r.after.source, r.generation}},
	} {
		if _, e := w.db.ExecContext(t.Context(), q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	return w, r
}
func TestMGRuleNativeSaveDelete(t *testing.T) {
	w, r := mgRuleFixture(t)
	turn := mgRuleTestTurn(t, w, mgRuleDefinitions())
	out, e := saveMGRule(t.Context(), turn, r)
	if e != nil || out != r.after {
		t.Fatal(out, e)
	}
	// Active lease debt survives both replacement and logical deletion.
	if _, e := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_rule_work SET job_lease_owner=7,job_lease_generation=9,job_lease_expires_ms=900,job_attempts=2 WHERE rule_id=?`, out.id); e != nil {
		t.Fatal(e)
	}
	r.before = out
	r.after.revision++
	r.after.updated++
	r.after.enabled = 0
	out, e = saveMGRule(t.Context(), turn, r)
	if e != nil {
		t.Fatal(e)
	}
	r.before = out
	r.after.revision++
	r.after.updated++
	r.after.deleted = 1
	turn.transaction.descriptor.ID = MGDeleteRule
	out, e = deleteMGRule(t.Context(), turn, r)
	if e != nil || out != r.after {
		t.Fatal(out, e)
	}
	var owner, generation, expiry, attempts, revision int64
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_revision FROM dvr_rule_work WHERE rule_id=?`, out.id).Scan(&owner, &generation, &expiry, &attempts, &revision); e != nil || owner != 7 || generation != 9 || expiry != 900 || attempts != 2 || revision != 3 {
		t.Fatal(owner, generation, expiry, attempts, revision, e)
	}
}
func TestMGRuleNativeControls(t *testing.T) {
	for _, control := range []string{"member", "profile", "source", "head", "stale-work", "cancel"} {
		t.Run(control, func(t *testing.T) {
			w, r := mgRuleFixture(t)
			switch control {
			case "member":
				_, _ = w.db.ExecContext(t.Context(), `UPDATE direct_memberships SET disabled=1`)
			case "profile":
				_, _ = w.db.ExecContext(t.Context(), `UPDATE direct_profiles SET revision=revision+1`)
			case "source":
				r.sourceRevision++
			case "head":
				r.generation = strings.Repeat("3", 48)
			case "stale-work":
				// Seed a native rule, then a conflicting revision in its reconciliation row.
				args := append(r.after.values(), "local")
				args[9] = int64(1)
				var q string
				for _, d := range mgRuleDefinitions() {
					if d.ID == mgRuleInsert {
						q = d.SQL
					}
				}
				if _, e := w.db.ExecContext(t.Context(), q, args...); e != nil {
					t.Fatal(e)
				}
				if _, e := w.db.ExecContext(t.Context(), `INSERT INTO dvr_rule_work(rule_id,rule_revision,generation,phase,updated_ms) VALUES(?,7,?,'existing',1)`, r.after.id, r.generation); e != nil {
					t.Fatal(e)
				}
				r.before = r.after
				r.after.revision = 2
				r.after.updated = 11
			}
			turn := mgRuleTestTurn(t, w, mgRuleDefinitions())
			ctx := t.Context()
			if control == "cancel" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			out, e := saveMGRule(ctx, turn, r)
			if e == nil || out != (mgRuleFacts{}) {
				t.Fatal("failure leaked result", out, e)
			}
			if control == "cancel" && !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			if _, e := turn.transaction.rollback(nil); e != nil {
				t.Fatal(e)
			}
			var count int
			if e := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_rules WHERE revision>1`).Scan(&count); e != nil || count != 0 {
				t.Fatal("rollback", count, e)
			}
		})
	}
}

func mgRuleTestTurn(t *testing.T, w *executor, defs ...[]stmt.Definition) *commandTurn {
	t.Helper()
	all := mgRuleDefinitions()
	if len(defs) > 0 {
		all = defs[0]
	}
	registry, err := stmttest.New(t, all)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic component budget only: several intent operations on one fixture
	// frame. Parent owns actual admission/profile and measured complete hold.
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), InventoryAccept, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range all {
		steps = append(steps, statementStep{d.ID, Work{16, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	turn.transaction.descriptor.ID = MGSaveRule
	return turn
}
