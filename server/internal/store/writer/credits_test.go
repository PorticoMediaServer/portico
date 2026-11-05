package writer

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func candidates(class CreditClass, n int, offset int64) []ScreenCreditCandidate {
	out := make([]ScreenCreditCandidate, n)
	for i := range out {
		out[i] = ScreenCreditCandidate{Class: class, Billing: int64(i), SourceOrdinal: offset + int64(i)}
	}
	return out
}

func TestCreditTrimLiteralBoundariesAndRetainedValidation(t *testing.T) {
	if MaxCastCredits != 100 || MaxCrewCredits != 50 {
		t.Fatal("frozen screen credit caps changed")
	}
	for _, tc := range []struct {
		class   CreditClass
		n, want int
	}{{Cast, 100, 100}, {Cast, 101, 100}, {Crew, 50, 50}, {Crew, 51, 50}} {
		input := candidates(tc.class, tc.n, 0)
		before := slices.Clone(input)
		ids, err := SelectScreenCredits(input)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != tc.want {
			t.Fatal(tc, len(ids))
		}
		if !reflect.DeepEqual(input, before) {
			t.Fatal("selector mutated input")
		}
		retained := make([]ScreenCreditCandidate, len(ids))
		for i, id := range ids {
			retained[i] = input[id]
		}
		if err := ValidateRetainedCreditShape(retained); err != nil {
			t.Fatal(err)
		}
		want := error(nil)
		if tc.n > tc.want {
			want = ErrCap
		}
		if err := ValidateRetainedCreditShape(input); !errors.Is(err, want) {
			t.Fatal(tc, err)
		}
	}
	full := append(candidates(Cast, 100, 0), candidates(Crew, 50, 100)...)
	if err := ValidateRetainedCreditShape(full); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRetainedCreditShape(append(full, ScreenCreditCandidate{Class: Crew, SourceOrdinal: 150})); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	for _, input := range [][]ScreenCreditCandidate{
		{{Class: "artist"}}, {{Class: Cast, Billing: -1}}, {{Class: Crew, SourceOrdinal: -1}},
		{{Class: Cast}, {Class: Crew}}, // duplicate source ordinal across classes
	} {
		if _, err := SelectScreenCredits(input); !errors.Is(err, ErrInvalid) {
			t.Fatal(input, err)
		}
		if err := ValidateRetainedCreditShape(input); !errors.Is(err, ErrInvalid) {
			t.Fatal(input, err)
		}
	}
	for _, input := range [][]ScreenCreditCandidate{
		{{Class: Crew, SourceOrdinal: 0}, {Class: Cast, SourceOrdinal: 1}},
		{{Class: Cast, Billing: 2, SourceOrdinal: 0}, {Class: Cast, Billing: 1, SourceOrdinal: 1}},
		{{Class: Crew, SourceOrdinal: 1}, {Class: Crew, SourceOrdinal: 0}},
	} {
		if err := ValidateRetainedCreditShape(input); !errors.Is(err, ErrInvalid) {
			t.Fatal(input, err)
		}
	}
}

func TestFrozenCrewPriorityAndSourceOrder(t *testing.T) {
	var raw []ScreenCreditCandidate
	for group, role := range []string{"Producer", "Composer", "Created by", "Writer", "Series Director"} {
		for i := range 51 {
			raw = append(raw, ScreenCreditCandidate{Class: Crew, Role: role, SourceOrdinal: int64(group*51 + i)})
		}
	}
	for start, want := range []int64{204, 153, 102, 51, 0} {
		input := raw[:255-start*51]
		ids, err := SelectScreenCredits(input)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 50 || input[ids[0]].SourceOrdinal != want || input[ids[49]].SourceOrdinal != want+49 {
			t.Fatal(start, ids)
		}
	}
	// Key crew win selection, but their accepted positions return to source order.
	input := []ScreenCreditCandidate{{Class: Crew, Role: "Producer", SourceOrdinal: 1}, {Class: Crew, Role: "Director", SourceOrdinal: 2}, {Class: Cast, Billing: 2, SourceOrdinal: 3}, {Class: Cast, Billing: 1, SourceOrdinal: 4}}
	ids, err := SelectScreenCredits(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []int{3, 2, 0, 1}) {
		t.Fatal(ids)
	}
	for _, tc := range []struct {
		role, department string
		want             int
	}{
		{" Director ", "", 0}, {"Series Director", "", 0}, {"", "Writing", 1}, {"Screenplay by", "", 1},
		{"", "Creator", 2}, {"Created by", "", 2}, {"Original Music Composer", "", 3}, {"Executive Producer", "", 4}, {"Grip", "", 5},
	} {
		if got := crewRank(ScreenCreditCandidate{Role: tc.role, Department: tc.department}); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
