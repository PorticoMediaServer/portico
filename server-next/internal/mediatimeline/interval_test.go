package mediatimeline

import (
	"errors"
	"math"
	"testing"
)

func TestClockMappingPreservesOffsetsAndRoundsOnce(t *testing.T) {
	start, end := Rational{1, 3}, Rational{2, 3}
	got, err := Map(Interval{StartTicks: -90_000, DurationTicks: 180_000, TimeBase: Rational{1, 90_000}, TrimStart: &start, TrimEnd: &end})
	if err != nil || got.SourceStart != (Rational{-2, 3}) || got.SourceEnd != (Rational{-1, 3}) || got.DurationMicroseconds != 333_333 {
		t.Fatalf("mapping: %+v %v", got, err)
	}
	// 30,000 frames at 30000/1001 fps must remain exactly 1,001 seconds.
	got, err = Map(Interval{StartTicks: 0, DurationTicks: 30_000, TimeBase: Rational{1001, 30_000}})
	if err != nil || got.DurationMicroseconds != 1_001_000_000 || got.SourceEnd != (Rational{1001, 1}) {
		t.Fatalf("fractional clock: %+v %v", got, err)
	}
}

func TestClockMappingRejectsUnknownInvalidAndOverflow(t *testing.T) {
	negative, past, equal := Rational{-1, 1}, Rational{3, 1}, Rational{1, 1}
	cases := []Interval{
		{},
		{DurationTicks: 1, TimeBase: Rational{0, 1}},
		{DurationTicks: 1, TimeBase: Rational{2, 2}},
		{DurationTicks: 1, TimeBase: Rational{1, -1}},
		{DurationTicks: 2, TimeBase: Rational{1, 1}, TrimStart: &negative},
		{DurationTicks: 2, TimeBase: Rational{1, 1}, TrimEnd: &past},
		{DurationTicks: 2, TimeBase: Rational{1, 1}, TrimStart: &equal, TrimEnd: &equal},
		{DurationTicks: 1, TimeBase: Rational{1, 2_000_000}},
		{DurationTicks: math.MaxInt64, TimeBase: Rational{1, 1}},
		{StartTicks: math.MaxInt64, DurationTicks: 1, TimeBase: Rational{2, 1}},
	}
	for i, v := range cases {
		if _, err := Map(v); !errors.Is(err, ErrInterval) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}
