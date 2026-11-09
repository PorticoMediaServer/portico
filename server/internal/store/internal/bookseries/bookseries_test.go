package bookseries

import (
	"errors"
	"strings"
	"testing"
)

func TestCohortExactOrderAndBytes(t *testing.T) {
	pairs := []Pair{{Name: "Saga", Position: "1"}, {Name: "Émile", Position: ""}, {Name: "Ünïcode", Position: "2.5"}}
	count, retained, digest, err := Cohort(pairs)
	if err != nil || count != 3 || retained != 24+int64(len("Saga")+len("1")+len("Émile")+len("Ünïcode")+len("2.5")) || digest == ([32]byte{}) {
		t.Fatal(count, retained, digest, err)
	}
	// Order is part of the identity: a reordered complete set is a different head.
	_, _, reordered, err := Cohort([]Pair{pairs[1], pairs[0], pairs[2]})
	if err != nil || reordered == digest {
		t.Fatal("reordered cohort kept the digest", err)
	}
	// An empty position is exact and differs from a missing pair or "0".
	_, _, empty, err := Cohort([]Pair{{Name: "Saga", Position: ""}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, zero, err := Cohort([]Pair{{Name: "Saga", Position: "0"}})
	if err != nil || zero == empty {
		t.Fatal("empty and zero position collapsed", err)
	}
}

func TestCohortEmptySet(t *testing.T) {
	count, retained, digest, err := Cohort(nil)
	if err != nil || count != 0 || retained != 0 || digest == ([32]byte{}) {
		t.Fatal(count, retained, digest, err)
	}
	if digest != EmptyDigest() {
		t.Fatal("empty digest is not the canonical empty cohort")
	}
}

func TestValidateBoundsAndExactness(t *testing.T) {
	if err := Validate(nil); err != nil {
		t.Fatal(err)
	}
	tooMany := make([]Pair, MaxPairs+1)
	for i := range tooMany {
		tooMany[i] = Pair{Name: string(rune('a' + i))}
	}
	for name, tc := range map[string]struct {
		pairs []Pair
		field string
		limit int
	}{
		"too_many":       {tooMany, "pairs", MaxPairs},
		"blank_name":     {[]Pair{{Name: "  "}}, "name", 0},
		"nul_name":       {[]Pair{{Name: "Sa\x00ga"}}, "name", 0},
		"invalid_utf8":   {[]Pair{{Name: "Sa\xffga"}}, "name", 0},
		"overlong_name":  {[]Pair{{Name: strings.Repeat("a", MaxNameRunes+1)}}, "name", MaxNameRunes},
		"nul_position":   {[]Pair{{Name: "Saga", Position: "1\x00"}}, "position", 0},
		"long_position":  {[]Pair{{Name: "Saga", Position: strings.Repeat("1", MaxPositionRunes+1)}}, "position", MaxPositionRunes},
		"duplicate_name": {[]Pair{{Name: "Saga"}, {Name: "Saga", Position: "2"}}, "duplicate", 0},
	} {
		t.Run(name, func(t *testing.T) {
			err := Validate(tc.pairs)
			var got *Error
			if !errors.As(err, &got) || got.Field != tc.field || got.Limit != tc.limit {
				t.Fatal(err)
			}
		})
	}
	// Exactly at both bounds is accepted; the bytes are never trimmed.
	maxName := strings.Repeat("a", MaxNameRunes)
	maxPosition := strings.Repeat("1", MaxPositionRunes)
	if err := Validate([]Pair{{Name: maxName, Position: maxPosition}}); err != nil {
		t.Fatal(err)
	}
}
