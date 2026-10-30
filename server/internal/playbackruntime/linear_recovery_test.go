package playbackruntime

import (
	"testing"
	"time"
)

// A channel that plays for a long stretch and then hiccups is a new fault, not
// one more step toward giving up: only failures with no sustained output in
// between count against the recovery budget. Output is what the producer
// added, however little of it the rolling buffer still holds.
func TestLinearRecoveryBudgetResetsAfterSustainedOutput(t *testing.T) {
	// Long healthy runs, each ending in a failure, never exhaust recovery.
	attempts := 0
	for run := 0; run < linearRecoveryBudget*3; run++ {
		if attempts = linearFailureCount(attempts, linearHealthyRun); attempts != 1 {
			t.Fatalf("run %d: a failure after sustained output counted as failure %d", run, attempts)
		}
	}
	// Hours of output count the same as a minute of it.
	if got := linearFailureCount(linearRecoveryBudget-1, 588*time.Second); got != 1 {
		t.Fatalf("a long run was not recognised as healthy: %d", got)
	}
	// A source that never plays for long still runs out of retries.
	attempts = 0
	for run := 1; run <= linearRecoveryBudget; run++ {
		if attempts = linearFailureCount(attempts, 5*time.Second); attempts != run {
			t.Fatalf("a short run reset the count: %d after %d failures", attempts, run)
		}
	}
	if attempts < linearRecoveryBudget {
		t.Fatal("continuous failure no longer exhausts recovery")
	}
	// A producer that added nothing, or just under the threshold, leaves the count growing.
	if got := linearFailureCount(3, 0); got != 4 {
		t.Fatalf("a producer with no output reset the count: %d", got)
	}
	if got := linearFailureCount(3, linearHealthyRun-time.Millisecond); got != 4 {
		t.Fatalf("a run short of the threshold reset the count: %d", got)
	}
}
