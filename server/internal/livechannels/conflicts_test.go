package livechannels

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func reservationFixture() ([]Reservation, time.Time) {
	n := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	return []Reservation{{ID: "own", Start: n, End: n.Add(time.Hour), Priority: 0}, {ID: "private-owner-recording", Start: n.Add(10 * time.Minute), End: n.Add(20 * time.Minute), Priority: 1}}, n
}
func TestUnknownEstimateNeverProducesConflict(t *testing.T) {
	a, _ := reservationFixture()
	if got := Conflicts(a, "own", Capacity{PlanningEstimate: 1}); len(got) != 0 {
		t.Fatal(got)
	}
}
func TestConflictIsHalfOpenAndPrivacySafe(t *testing.T) {
	a, n := reservationFixture()
	a = append(a, Reservation{ID: "touching", Start: n.Add(time.Hour), End: n.Add(2 * time.Hour), Priority: 100})
	got := Conflicts(a, "own", Capacity{Known: true, Effective: 1})
	if len(got) != 1 || got[0].Start != n.Add(10*time.Minute).Format(time.RFC3339) || got[0].End != n.Add(20*time.Minute).Format(time.RFC3339) || got[0].Demand != 2 {
		t.Fatal(got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "private-owner") {
		t.Fatal("private recording identity escaped")
	}
}
func TestPriorityStartAndIDHaveStableTieBreaks(t *testing.T) {
	n := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	a := []Reservation{{ID: "b", Start: n, End: n.Add(time.Hour), Priority: 1}, {ID: "a", Start: n, End: n.Add(time.Hour), Priority: 1}}
	cap := Capacity{Known: true, Effective: 1}
	if len(Conflicts(a, "a", cap)) != 0 || len(Conflicts(a, "b", cap)) != 1 {
		t.Fatal("stable ID tie-break changed")
	}
	a[0].Start = n.Add(-time.Minute)
	if len(Conflicts(a, "a", cap)) != 1 || len(Conflicts(a, "b", cap)) != 0 {
		t.Fatal("earlier padded start must win")
	}
	a[1].Priority = 2
	if len(Conflicts(a, "a", cap)) != 0 || len(Conflicts(a, "b", cap)) != 1 {
		t.Fatal("priority must precede start")
	}
}
func TestOverlappingHigherPriorityDemandRespectsRealCapacity(t *testing.T) {
	a, n := reservationFixture()
	a = append(a, Reservation{ID: "other", Start: n.Add(15 * time.Minute), End: n.Add(25 * time.Minute), Priority: 2})
	got := Conflicts(a, "own", Capacity{Known: true, Effective: 2})
	if len(got) != 1 || got[0].Start != n.Add(15*time.Minute).Format(time.RFC3339) || got[0].End != n.Add(20*time.Minute).Format(time.RFC3339) {
		t.Fatal(got)
	}
	if len(Conflicts(a, "own", Capacity{Known: true, Effective: 3})) != 0 {
		t.Fatal("invented capacity conflict")
	}
}
func TestConflictIgnoresInputOrder(t *testing.T) {
	a, n := reservationFixture()
	a = append(a, Reservation{ID: "another", Start: n.Add(12 * time.Minute), End: n.Add(18 * time.Minute), Priority: 2})
	x, _ := json.Marshal(Conflicts(a, "own", Capacity{Known: true, Effective: 1}))
	a[0], a[2] = a[2], a[0]
	y, _ := json.Marshal(Conflicts(a, "own", Capacity{Known: true, Effective: 1}))
	if string(x) != string(y) {
		t.Fatal(string(x), string(y))
	}
}
