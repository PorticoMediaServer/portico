package ingestion

import (
	"testing"
	"time"
)

// A scan's page budget follows the writer hold it measures (scanner M2): cut
// in proportion past the 50 ms target, doubled only when a full page held
// under half of it, and kept within the storage batch limits.
func TestPageBudgetFollowsMeasuredHold(t *testing.T) {
	for _, tc := range []struct {
		name               string
		budget, used, want int
		held               time.Duration
	}{
		{"slow page shrinks in proportion", 64, 64, 16, 200 * time.Millisecond},
		{"never below one name", 4, 4, 1, 2 * time.Second},
		{"fast full page doubles", 16, 16, 32, 10 * time.Millisecond},
		{"fast page that did not fill keeps its budget", 16, 3, 16, 5 * time.Millisecond},
		{"near the target keeps its budget", 16, 16, 16, 40 * time.Millisecond},
		{"growth stops at the storage limit", inventorySourceNames, inventorySourceNames, inventorySourceNames, time.Millisecond},
	} {
		if got := nextPageNames(tc.budget, tc.used, tc.held); got != tc.want {
			t.Errorf("%s: nextPageNames(%d, %d, %s) = %d, want %d", tc.name, tc.budget, tc.used, tc.held, got, tc.want)
		}
	}
	s := &Service{}
	if got := s.pageNames("job"); got != pageNamesStart {
		t.Fatalf("a new scan starts at %d names, got %d", pageNamesStart, got)
	}
	s.notePageHold("job", pageNamesStart, pageNamesStart, time.Millisecond)
	if got := s.pageNames("job"); got != 2*pageNamesStart {
		t.Fatalf("after a fast full page: %d", got)
	}
	s.forgetPageBudget("job")
	if got := s.pageNames("job"); got != pageNamesStart {
		t.Fatalf("a finished scan's budget must not carry over: %d", got)
	}
}
