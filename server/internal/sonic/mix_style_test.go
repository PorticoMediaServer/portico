package sonic

import (
	"math"
	"testing"
)

func TestMixStyleUsesOnlyOriginalHeadAllowlistAndDistinctStrongestStyle(t *testing.T) {
	model := Identity{ID: ModelID, Version: ModelVersion, SHA256: ModelSHA256}
	scores := make([]float64, TagCount)
	scores[0], scores[7], scores[10] = .99, .7, .5 // guitar cannot override rock.
	if tag, ok := MixStyle(model, scores, nil); !ok || tag != "rock" {
		t.Fatal(tag, ok)
	}
	if tag, ok := MixStyle(model, scores, map[string]bool{"rock": true}); ok || tag != "" {
		t.Fatal("duplicate style renamed to weaker alternative", tag, ok)
	}
	for _, index := range []int{0, 2, 4, 8, 15, 21, 23, 24, 27, 34, 49} {
		instrumentOnly := make([]float64, TagCount)
		instrumentOnly[index] = 1
		if tag, ok := MixStyle(model, instrumentOnly, nil); ok || tag != "" {
			t.Fatal("non-allowed style named mix", index, tag)
		}
	}
	for _, style := range mixStyles {
		vector := make([]float64, TagCount)
		vector[style.index] = MixStyleMinimumScore
		if tag, ok := MixStyle(model, vector, nil); !ok || tag != style.tag {
			t.Fatal("allowed style index differs from original MTT head", style, tag, ok)
		}
		vector[style.index] = MixStyleMinimumScore - .001
		if _, ok := MixStyle(model, vector, nil); ok {
			t.Fatal("weak score named mix", style)
		}
	}
	scores[2] = math.NaN()
	if _, ok := MixStyle(model, scores, nil); ok {
		t.Fatal("invalid head named mix")
	}
	scores[2] = 0
	model.Version = "different-head"
	if _, ok := MixStyle(model, scores, nil); ok {
		t.Fatal("different model version reused allowlist")
	}
	if _, ok := MixStyle(model, nil, nil); ok {
		t.Fatal("missing head named mix")
	}
}
