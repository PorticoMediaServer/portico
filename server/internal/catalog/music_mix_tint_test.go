package catalog

import (
	"encoding/json"
	"os"
	"testing"
)

func TestMusicMixTintUsesCanonicalCrossPlatformChannelVectors(t *testing.T) {
	data, err := os.ReadFile("../../../packages/client-core/test/fixtures/channel-tint-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct{ Seed, Tint string }
	if err := json.Unmarshal(data, &vectors); err != nil || len(vectors) < 8 {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		if got := musicMixTint(vector.Seed); got != vector.Tint {
			t.Fatal(vector.Seed, got, vector.Tint)
		}
	}
}
