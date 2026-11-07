package writer

import (
	"strings"
	"testing"
)

func TestMGRuleNativeDeleteAfterSourceRetirement(t *testing.T) {
	for _, mode := range []string{"delete", "stale-source", "stale-rule", "member", "create", "enable"} {
		t.Run(mode, func(t *testing.T) {
			w, r := mgRuleFixture(t)
			r.after.enabled = 0
			turn := mgRuleTestTurn(t, w)
			saved, err := saveMGRule(t.Context(), turn, r)
			if err != nil {
				t.Fatal(err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{
				`UPDATE live_sources SET revision=revision+1,state='disabled'`,
				`UPDATE live_source_identities SET has_live_source_removals=1`,
				`UPDATE dvr_rule_work SET job_lease_owner=7,job_lease_generation=9,job_lease_expires_ms=900,job_attempts=2`,
			} {
				if _, err := w.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			r.before, r.after = saved, saved
			r.after.revision++
			r.after.updated++
			r.after.deleted = 1
			r.sourceRevision++
			if mode == "stale-source" {
				r.sourceRevision--
			} else if mode == "member" {
				r.owner.memberRevision++
			} else if mode == "stale-rule" {
				if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_rules SET revision=revision+1`); err != nil {
					t.Fatal(err)
				}
			} else if mode == "create" {
				r.before = mgRuleFacts{}
				r.after.id = strings.Repeat("e", 64)
				r.after.series = "another-series"
				r.after.revision, r.after.deleted = 1, 0
			} else if mode == "enable" {
				r.after.enabled, r.after.deleted = 1, 0
			}
			turn = mgRuleTestTurn(t, w)
			var out mgRuleFacts
			if mode == "create" || mode == "enable" {
				out, err = saveMGRule(t.Context(), turn, r)
			} else {
				turn.transaction.descriptor.ID = MGDeleteRule
				out, err = deleteMGRule(t.Context(), turn, r)
			}
			if mode == "delete" {
				if err != nil || out != r.after {
					t.Fatal("owned retired-source rule cannot be deleted", out, err)
				}
			} else if err == nil || out != (mgRuleFacts{}) {
				t.Fatal("retirement bypassed native fences", mode, out, err)
			}
			var owner, generation, expires, attempts int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts FROM dvr_rule_work WHERE rule_id=?`, saved.id).Scan(&owner, &generation, &expires, &attempts); err != nil || owner != 7 || generation != 9 || expires != 900 || attempts != 2 {
				t.Fatal("worker custody changed", owner, generation, expires, attempts, err)
			}
		})
	}
}
