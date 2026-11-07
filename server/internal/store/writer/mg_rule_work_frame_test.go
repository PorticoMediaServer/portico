package writer

import (
	"errors"
	"testing"
)

func TestMGRuleWorkerPurposeFence(t *testing.T) {
	for _, mode := range []string{"purpose", "unverified-policy"} {
		t.Run(mode, func(t *testing.T) {
			turn, r := mgRuleWorkerFixture(t)
			if mode == "purpose" {
				turn.transaction.descriptor.ID = MGSaveRule
			} else {
				turn.transaction.policyVerified = false
			}
			out, err := claimMGRuleWork(t.Context(), turn, r)
			if !errors.Is(err, ErrInvalid) || out != (mgRuleWorkFacts{}) {
				t.Fatal("unaccepted work purpose", out, err)
			}
			actual, err := mgRuleWorkReadNative(t.Context(), turn, r.before.rule)
			if err != nil || actual != r.before {
				t.Fatal("refused frame changed work", actual, err)
			}
		})
	}
}
