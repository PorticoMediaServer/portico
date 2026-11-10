package naming

import (
	"reflect"
	"testing"
)

// These are generated/blank naming vectors from Plex's documented music
// layout, never real media or evidence that a file was probed or played.

func TestSplitPackedTrackNumber(t *testing.T) {
	for _, row := range []struct {
		raw         int
		disc, track int
		ok          bool
	}{
		{101, 1, 1, true},
		{113, 1, 13, true},
		{201, 2, 1, true},
		{302, 3, 2, true},
		{999, 9, 99, true},
		{99, 0, 0, false},
		{100, 0, 0, false},
		{200, 0, 0, false},
		{900, 0, 0, false},
		{1000, 0, 0, false},
		{0, 0, 0, false},
		{-302, 0, 0, false},
	} {
		got, ok := SplitPackedTrackNumber(row.raw)
		if ok != row.ok || ok && (got.Disc != row.disc || got.Track != row.track) {
			t.Fatalf("split %d: %+v ok=%v, want %d/%d ok=%v", row.raw, got, ok, row.disc, row.track, row.ok)
		}
	}
}

func TestPackedTrackCohortTheWallAndPackedExample(t *testing.T) {
	wall := []int{101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113,
		201, 202, 203, 204, 205, 206, 207, 208, 209, 210, 211, 212, 213}
	got, ok := PackedTrackCohort(wall, false)
	if !ok {
		t.Fatal("The Wall 101-113/201-213 cohort refused")
	}
	for _, row := range []struct {
		raw         int
		disc, track int
	}{
		{101, 1, 1},
		{113, 1, 13},
		{201, 2, 1},
		{213, 2, 13},
	} {
		if got[row.raw] != (PackedTrackNumber{Disc: row.disc, Track: row.track}) {
			t.Fatalf("The Wall %d: %+v", row.raw, got[row.raw])
		}
	}

	// The Plex addendum's literal example: 302 is disc 3, track 2, inside a
	// cohort whose other discs start at track 1.
	got, ok = PackedTrackCohort([]int{101, 102, 201, 301, 302}, false)
	if !ok {
		t.Fatal("101/102/201/301/302 cohort refused")
	}
	if got[302] != (PackedTrackNumber{Disc: 3, Track: 2}) {
		t.Fatalf("302 split %+v", got[302])
	}
}

func TestPackedTrackCohortRefusesSparseMixedAndAmbiguous(t *testing.T) {
	for _, test := range []struct {
		name    string
		numbers []int
	}{
		{"nil", nil},
		{"empty", []int{}},
		{"one number", []int{302}},
		{"one disc", []int{101, 102, 103}},
		{"isolated per-disc numbers", []int{101, 201, 301}},
		{"missing disc one", []int{201, 202, 301, 302}},
		{"missing middle disc", []int{101, 102, 301, 302}},
		{"missing first track", []int{102, 103, 201, 202}},
		{"missing middle track", []int{101, 102, 104, 201, 202}},
		{"missing last disc track", []int{101, 102, 201, 203}},
		{"duplicate number", []int{101, 102, 201, 201}},
		{"zero track present", []int{101, 102, 201, 200}},
		{"four digit number", []int{101, 102, 201, 1002}},
		{"mixed numbering width", []int{1, 2, 101, 201}},
		{"plain disc and track", []int{11, 12, 21, 22}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, ok := PackedTrackCohort(test.numbers, false); ok || got != nil {
				t.Fatalf("ambiguous cohort accepted: %+v", got)
			}
		})
	}
}

func TestPackedTrackCohortDiscEvidenceWins(t *testing.T) {
	numbers := []int{101, 102, 201, 202}
	if got, ok := PackedTrackCohort(numbers, true); ok || got != nil {
		t.Fatalf("disc-tagged album still inferred: %+v", got)
	}
}

func TestPackedTrackCohortIsBoundedAndDoesNotMutate(t *testing.T) {
	numbers := make([]int, MaxPackedTrackCohort+1)
	for i := range numbers {
		numbers[i] = 101 + i
	}
	if got, ok := PackedTrackCohort(numbers, false); ok || got != nil {
		t.Fatal("oversized cohort accepted")
	}

	valid := []int{101, 102, 201, 202}
	before := append([]int(nil), valid...)
	if _, ok := PackedTrackCohort(valid, false); !ok {
		t.Fatal("valid cohort refused")
	}
	if !reflect.DeepEqual(valid, before) {
		t.Fatalf("cohort input mutated: %v", valid)
	}
}
