package setupflow

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

// allowed is B-DESIGN.md §3.2, written out independently of table: "from event to".
// Changing a transition means changing this list too.
const allowed = `
open start open
expired start open
open initialize_hosted started
started initialize_hosted started
awaiting_approval initialize_hosted awaiting_approval
open initialize_local completed
started request_approval awaiting_approval
awaiting_approval request_approval awaiting_approval
installed_lapsed request_approval installed
installed request_approval installed
started abandon open
awaiting_approval abandon open
expired abandon open
awaiting_approval hosted_approved approved
awaiting_approval hosted_denied started
started window_passed expired
awaiting_approval window_passed expired
approved delivery_progress finishing
finishing delivery_progress finishing
approved delivery_transient approved
finishing delivery_transient finishing
approved delivery_stopped exhausted
finishing delivery_stopped exhausted
exhausted retry finishing
approved delivery_refused started
finishing delivery_refused started
exhausted delivery_refused started
finishing installed installed
installed exchange completed
installed admission_window_passed installed_lapsed
open start_over open
started start_over open
awaiting_approval start_over open
expired start_over open
approved start_over open
finishing start_over open
exhausted start_over open
installed start_over open
installed_lapsed start_over open
`

func TestEveryPhaseAndEventHasExactlyTheDesignedOutcome(t *testing.T) {
	want := map[[2]string]string{}
	for _, line := range strings.Split(strings.TrimSpace(allowed), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("bad line %q", line)
		}
		want[[2]string{f[0], f[1]}] = f[2]
	}
	seen := 0
	for _, from := range Phases {
		for _, event := range Events {
			to, err := Next(from, event)
			key := [2]string{string(from), string(event)}
			if expected, ok := want[key]; ok {
				seen++
				if err != nil || string(to) != expected {
					t.Errorf("%s on %s: got %s, %v; want %s", from, event, to, err, expected)
				}
				continue
			}
			if err == nil {
				t.Errorf("%s on %s: moved to %s; want a refusal", from, event, to)
				continue
			}
			if to != from {
				t.Errorf("%s on %s: a refusal must leave the phase, got %s", from, event, to)
			}
			var expected error
			switch {
			case from == Completed:
				expected = ErrAlreadyComplete
			case event == EventStart:
				expected = ErrInProgress
			case event == EventInitializeLocal:
				expected = ErrPorticoSetup
			default:
				expected = ErrStateChanged
			}
			if !errors.Is(err, expected) {
				t.Errorf("%s on %s: refused with %v; want %v", from, event, err, expected)
			}
		}
	}
	if seen != len(want) {
		t.Errorf("checked %d designed transitions; the list has %d (an unknown phase or event in it?)", seen, len(want))
	}
}

func TestEveryPhaseAndEventIsListedOnceAndReachable(t *testing.T) {
	phases := map[Phase]bool{}
	for _, p := range Phases {
		if phases[p] {
			t.Errorf("phase %s listed twice", p)
		}
		phases[p] = true
	}
	events := map[Event]bool{}
	for _, e := range Events {
		if events[e] {
			t.Errorf("event %s listed twice", e)
		}
		events[e] = true
	}
	reached := map[Phase]bool{Open: true}
	for event, edges := range table {
		if !events[event] {
			t.Errorf("table event %s is not in Events", event)
		}
		froms := map[Phase]bool{}
		for _, e := range edges {
			if !phases[e.from] || !phases[e.to] {
				t.Errorf("%s: unknown phase in %v", event, e)
			}
			if froms[e.from] {
				t.Errorf("%s: two edges from %s", event, e.from)
			}
			froms[e.from] = true
			reached[e.to] = true
		}
	}
	var missing []string
	for _, p := range Phases {
		if !reached[p] {
			missing = append(missing, string(p))
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Errorf("unreachable phases: %v", missing)
	}
}

func TestCoarseShowsOnlyThreeValues(t *testing.T) {
	want := map[Phase]Coarse{Open: CoarseReady, Expired: CoarseReady, Completed: CoarseComplete}
	for _, p := range Phases {
		expected, ok := want[p]
		if !ok {
			expected = CoarseInProgress
		}
		if got := p.Coarse(); got != expected {
			t.Errorf("%s: coarse %s, want %s", p, got, expected)
		}
	}
}
