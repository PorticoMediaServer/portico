package sonic

import (
	"reflect"
	"testing"
)

func TestSmoothShuffleKeepsAnchorAndOccurrencesAndReducesAbruptChanges(t *testing.T) {
	input := []SmoothTrack{}
	for n := 0; n < 6; n++ {
		x, y := float32(1), float32(0)
		if n%2 == 1 {
			x, y = 0, 1
		}
		point := clusterFixture(t, int64(n+1), x, y, 1)
		input = append(input, SmoothTrack{Occurrence: int64(n), ID: point.ID, Vector: point.Vector, Artists: []int64{int64(n + 10)}})
	}
	order, err := SmoothWindow(input, nil)
	if err != nil || !reflect.DeepEqual(order, []int64{0, 2, 4, 1, 3, 5}) {
		t.Fatal(order, err)
	}
	changes := func(order []int64) float64 {
		var sum float64
		for n := 1; n < len(order); n++ {
			cosine, err := Cosine(input[order[n-1]].Vector, input[order[n]].Vector)
			if err != nil {
				t.Fatal(err)
			}
			sum += 1 - cosine
		}
		return sum
	}
	if changes(order) >= changes([]int64{0, 1, 2, 3, 4, 5}) {
		t.Fatal("source sequence was not smoothed")
	}
	again, err := SmoothWindow(input, nil)
	if err != nil || !reflect.DeepEqual(order, again) {
		t.Fatal("same seeded evidence changed order", order, again, err)
	}
}
func TestSmoothShuffleUsesActualSharedCreditsAndKeepsRepeatedOccurrences(t *testing.T) {
	p := clusterFixture(t, 1, 1, 0, 1)
	previous := SmoothTrack{ID: 1, Vector: p.Vector, Artists: []int64{10, 11}}
	input := []SmoothTrack{{Occurrence: 0, ID: 1, Vector: p.Vector, Artists: []int64{10, 11}}, {Occurrence: 1, ID: 2, Vector: p.Vector, Artists: []int64{11}}, {Occurrence: 2, ID: 3, Vector: p.Vector, Artists: []int64{12}}}
	order, err := SmoothWindow(input, &previous)
	if err != nil || len(order) != 3 || order[0] != 2 {
		t.Fatal("shared credit or repeated occurrence was ignored", order, err)
	}
	seen := map[int64]bool{}
	for _, id := range order {
		if seen[id] {
			t.Fatal("duplicate occurrence", order)
		}
		seen[id] = true
	}
	input[2].Vector = Embedding{}
	if _, err := SmoothWindow(input, &previous); err == nil {
		t.Fatal("missing actual numeric facts were fabricated")
	}
}
