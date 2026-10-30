package listeningrules

import (
	"errors"
	"testing"
)

func TestResumeUsesOnlySelectedRewindAndClamps(t *testing.T) {
	for _, ms := range []int{0, 5000, 15000, 30000} {
		for _, position := range []float64{0, 2, 100} {
			want := position - float64(ms)/1000
			if want < 0 {
				want = 0
			}
			got, err := Resume(position, ms)
			if err != nil || got != want {
				t.Fatalf("position=%v choice=%d: got %v, %v; want %v", position, ms, got, err, want)
			}
		}
	}
	for _, ms := range []int{-1, 5, 10000, 60000} {
		if _, err := Resume(100, ms); !errors.Is(err, ErrResumeRewind) {
			t.Fatalf("invalid choice %d admitted: %v", ms, err)
		}
	}
}
