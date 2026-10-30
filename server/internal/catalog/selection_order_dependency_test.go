package catalog

import (
	"fmt"
	"math"
	"testing"
)

// A complete small selection does not make ForYou's taste input candidate
// sized. An unshared taste facet changes the full-vector norm, which changes
// the balance between content and quality and can reverse two selected works.
// Exercise the production scorer with already-hydrated immutable inputs so
// this regression needs neither a database fixture nor a second scorer.
func TestSelectionForYouDependsOnUnsharedTasteNorm(t *testing.T) {
	score := func(outside float64) (float64, float64) {
		t.Helper()
		x := recSession{
			taste: recTaste{values: map[string]float64{"g:shared": 1, "g:outside": outside}},
			idf:   map[string]float64{"g:shared": 1, "g:other": 1, "g:outside": 1},
			hydration: &recHydration{works: map[int64]*recWorkMetadata{
				1: {quality: 6.3, hasQuality: true, facets: []string{"g:shared"}},
				2: {quality: 9.3, hasQuality: true, facets: []string{"g:other"}},
			}},
		}
		candidates := []*recScored{{work: 1}, {work: 2}}
		if err := x.recRescore(candidates, 0); err != nil {
			t.Fatal(err)
		}
		if !candidates[0].match || candidates[1].match {
			t.Fatal("fixture must retain exactly one positive candidate taste match")
		}
		return candidates[0].score, candidates[1].score
	}
	matched, quality := score(0)
	if matched <= quality {
		t.Fatalf("without outside taste, content match must win: %g <= %g", matched, quality)
	}
	matched, quality = score(10)
	if matched >= quality {
		t.Fatalf("outside taste must reverse selected order: %g >= %g", matched, quality)
	}
}

func TestSelectionForYouNormStableAcrossDetachedMaps(t *testing.T) {
	var expected uint64
	for turn := 0; turn < 31; turn++ {
		values, idf := map[string]float64{}, map[string]float64{}
		for i := 0; i < 200; i++ {
			index := (i + turn) % 200
			facet := fmt.Sprintf("g:%03d", index)
			values[facet] = float64(index+1) / 7
			idf[facet] = float64(index+3) / 11
		}
		got := math.Float64bits(recTasteNorm(values, idf))
		if turn == 0 {
			expected = got
		} else if got != expected {
			t.Fatal("identical detached taste changed exact norm bits")
		}
	}
}
