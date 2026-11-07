package writer

import (
	"errors"
	"testing"
	"testing/synctest"
)

func TestMGRuleNativePurposeFence(t *testing.T) {
	for _, mode := range []string{"purpose", "unverified-policy"} {
		t.Run(mode, func(t *testing.T) {
			w, r := mgRuleFixture(t)
			turn := mgRuleTestTurn(t, w)
			if mode == "purpose" {
				turn.transaction.descriptor.ID = MGDeleteRule
			} else {
				turn.transaction.policyVerified = false
			}
			out, err := saveMGRule(t.Context(), turn, r)
			if !errors.Is(err, ErrInvalid) || out != (mgRuleFacts{}) {
				t.Fatal("unaccepted rule purpose", out, err)
			}
		})
	}
}

func TestMGRuleNativeReadbackRollback(t *testing.T) {
	for _, table := range []string{"dvr_rules", "dvr_rule_work"} {
		t.Run(table, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w, r := mgRuleFixture(t)
				column, key := "config_json", "id"
				if table == "dvr_rule_work" {
					column, key = "cursor", "rule_id"
				}
				// Fixture DDL precedes the protected command turn. Rollback
				// exercises only its row effects, including under the race build.
				if _, err := w.db.ExecContext(t.Context(), `CREATE TRIGGER mg_rule_readback AFTER INSERT ON `+table+` BEGIN UPDATE `+table+` SET `+column+`='corrupt' WHERE `+key+`=NEW.`+key+`; END`); err != nil {
					t.Fatal(err)
				}
				turn := mgRuleTestTurn(t, w)
				out, err := saveMGRule(t.Context(), turn, r)
				if !errors.Is(err, ErrConflict) || out != (mgRuleFacts{}) {
					t.Fatal("changed native readback accepted", out, err)
				}
				var actual string
				if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT `+column+` FROM `+table+` WHERE `+key+`=?`, r.after.id).Scan(&actual); err != nil || actual != "corrupt" {
					t.Fatal("mutation did not reach the native row", actual, err)
				}
				if _, err := turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_rules`).Scan(&count); err != nil || count != 0 {
					t.Fatal("failed frame retained rule", count, err)
				}
			})
		})
	}
}
