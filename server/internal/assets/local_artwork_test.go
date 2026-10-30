package assets

import (
	"strings"
	"testing"
)

func TestLocalArtworkSourceOrderRetainsUnknownAndActualBasenames(t *testing.T) {
	for _, rank := range []int64{0, LocalArtworkUnknownRank} {
		name, got, err := NormalizeLocalArtworkSource("", rank)
		if err != nil || name != "" || got != LocalArtworkUnknownRank {
			t.Fatal("legacy source fabricated canonical order", name, got, err)
		}
	}
	for _, name := range []string{"Poster.JPG", "Ártwork-2.png", strings.Repeat("x", MaxArtworkSourceNameBytes)} {
		got, rank, err := NormalizeLocalArtworkSource(name, 0)
		if err != nil || got != name || rank != 0 {
			t.Fatal("actual source basename changed", rank, err)
		}
	}
	for _, name := range []string{".", "..", "/poster.jpg", `dir\poster.jpg`, "poster\x00.jpg", string([]byte{0xff}), strings.Repeat("x", MaxArtworkSourceNameBytes+1)} {
		if _, _, err := NormalizeLocalArtworkSource(name, LocalArtworkUnknownRank); err == nil {
			t.Fatal("invalid basename accepted")
		}
	}
	if _, _, err := NormalizeLocalArtworkSource("", 1); err == nil {
		t.Fatal("missing filename carried a known rank")
	}
	if _, _, err := NormalizeLocalArtworkSource("poster.jpg", -1); err == nil {
		t.Fatal("negative accepted rank")
	}
}
