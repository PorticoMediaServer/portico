package mediasource

import (
	"errors"
	"math"
	"testing"
)

func tm(n, d int64) Time {
	v, err := NewTime(n, d)
	if err != nil {
		panic(err)
	}
	return v
}
func fraction(t *testing.T, v Time, n, d int64) {
	t.Helper()
	a, b := v.Fraction()
	if a != n || b != d {
		t.Fatalf("got %d/%d want %d/%d", a, b, n, d)
	}
}

func TestRationalCadenceAndSamples(t *testing.T) {
	frame, err := FromTicks(24000, 1001, 24000)
	if err != nil {
		t.Fatal(err)
	}
	fraction(t, frame, 1001, 1)
	sample, err := FromTicks(1, 1, 44100)
	if err != nil {
		t.Fatal(err)
	}
	fraction(t, sample, 1, 44100)
	for _, tc := range []struct {
		mode Rounding
		want int64
	}{{Floor, 22}, {Ceil, 23}} {
		got, err := sample.Microseconds(tc.mode)
		if err != nil || got != tc.want {
			t.Fatalf("got %d %v", got, err)
		}
	}
	if _, err := sample.Microseconds(Exact); !errors.Is(err, ErrTime) {
		t.Fatal("fractional microsecond called exact")
	}
	negative := tm(-1, 44100)
	got, _ := negative.Microseconds(Floor)
	if got != -23 {
		t.Fatalf("negative floor %d", got)
	}
	got, _ = negative.Microseconds(Ceil)
	if got != -22 {
		t.Fatalf("negative ceil %d", got)
	}
	got, err = tm(-3, 2).Microseconds(Exact)
	if err != nil || got != -1500000 {
		t.Fatal("negative origin lost")
	}
	fraction(t, Time{}, 0, 1)
}

func TestRationalOverflowAfterReduction(t *testing.T) {
	v, err := FromTicks(math.MaxInt64, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	fraction(t, v, math.MaxInt64, 1)
	if _, err := FromTicks(math.MaxInt64, 2, 1); !errors.Is(err, ErrTimeOverflow) {
		t.Fatal("overflow accepted")
	}
	if _, err := tm(math.MaxInt64, 1).Add(tm(1, 1)); !errors.Is(err, ErrTimeOverflow) {
		t.Fatal("addition overflow accepted")
	}
	if _, err := tm(math.MaxInt64, 1).Microseconds(Exact); !errors.Is(err, ErrTimeOverflow) {
		t.Fatal("microsecond overflow accepted")
	}
	if _, err := NewTime(1, 0); !errors.Is(err, ErrTime) {
		t.Fatal("zero denominator accepted")
	}
	if _, err := NewTime(1, -1); !errors.Is(err, ErrTime) {
		t.Fatal("negative denominator accepted")
	}
	fraction(t, tm(math.MinInt64, 2), math.MinInt64/2, 1)
}

func TestTimelineMultipartTrimGapAndBoundary(t *testing.T) {
	spans := []Span{
		{Part: "one", Version: "v1", SourceStart: tm(-1, 1), SourceEnd: tm(9, 1), PresentationStart: tm(0, 1)},
		{Part: "two", Version: "v2", SourceStart: tm(2, 1), SourceEnd: tm(12, 1), PresentationStart: tm(12, 1)},
	}
	timeline, err := NewTimeline(spans)
	if err != nil {
		t.Fatal(err)
	}
	spans[0].SourceStart = tm(5, 1) // caller slice cannot mutate the accepted mapping
	out, err := timeline.MapSource("one", "v1", tm(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	fraction(t, out, 1, 1)
	part, version, source, err := timeline.MapPresentation(tm(12, 1))
	if err != nil || part != "two" || version != "v2" {
		t.Fatal("wrong multipart boundary")
	}
	fraction(t, source, 2, 1)
	for _, at := range []int64{-1, 10, 11, 22} {
		if _, _, _, err := timeline.MapPresentation(tm(at, 1)); !errors.Is(err, ErrUnmapped) {
			t.Fatalf("gap/terminal %d mapped", at)
		}
	}
	if _, err := timeline.MapSource("one", "v1", tm(9, 1)); !errors.Is(err, ErrUnmapped) {
		t.Fatal("source end mapped as a sample")
	}
	if _, err := timeline.MapSource("one", "v2", tm(0, 1)); !errors.Is(err, ErrUnmapped) {
		t.Fatal("source version ignored")
	}
}

func TestTimelineRejectsAmbiguity(t *testing.T) {
	a := Span{Part: "part", Version: "version", SourceStart: tm(0, 1), SourceEnd: tm(10, 1), PresentationStart: tm(0, 1)}
	b := a
	b.PresentationStart = tm(20, 1)
	if _, err := NewTimeline([]Span{a, b}); !errors.Is(err, ErrTimeline) {
		t.Fatal("overlapping source accepted")
	}
	b.Part = "other"
	b.PresentationStart = tm(9, 1)
	if _, err := NewTimeline([]Span{a, b}); !errors.Is(err, ErrTimeline) {
		t.Fatal("overlapping presentation accepted")
	}
	b.PresentationStart = tm(-1, 1)
	if _, err := NewTimeline([]Span{b}); !errors.Is(err, ErrTimeline) {
		t.Fatal("negative presentation accepted")
	}
	a.SourceEnd = a.SourceStart
	if _, err := NewTimeline([]Span{a}); !errors.Is(err, ErrTimeline) {
		t.Fatal("empty span accepted")
	}
}

func TestTimelineInSpanArithmeticFailureIsNotGap(t *testing.T) {
	timeline, err := NewTimeline([]Span{{Part: "p", Version: "v", SourceStart: tm(0, 1), SourceEnd: tm(1, 2), PresentationStart: tm(1, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	// This is 1/2 + 1/18446744073709551614, inside the span. Its
	// source-relative delta has a denominator larger than signed 64 bits.
	position := tm(4611686018427387904, 9223372036854775807)
	if _, _, _, err := timeline.MapPresentation(position); !errors.Is(err, ErrTimeOverflow) {
		t.Fatalf("in-span arithmetic failure became %v", err)
	}
}
