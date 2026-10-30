package ingestion

import (
	"testing"
	"time"
)

func TestDeepAnalysisBudgetFollowsTheFile(t *testing.T) {
	if got := deepAnalysisBudget(0, 0); got != deepAnalysisFloor {
		t.Fatalf("unknown file budget = %v", got)
	}
	small := deepAnalysisBudget(5<<20, 180)
	film := deepAnalysisBudget(30<<30, 2*3600)
	if small >= film || film <= 6*time.Hour || small > 20*time.Minute {
		t.Fatalf("budgets do not follow the file: small %v, film %v", small, film)
	}
	// A large remux on slow storage is bounded by its bytes, not its running time.
	remux := deepAnalysisBudget(120<<30, 3600)
	if remux <= deepAnalysisBudget(1<<30, 3600) {
		t.Fatalf("size did not lengthen the budget: %v", remux)
	}
	if got := deepAnalysisBudget(1<<50, 1e12); got != deepAnalysisCeiling {
		t.Fatalf("hostile size escaped the ceiling: %v", got)
	}
}
