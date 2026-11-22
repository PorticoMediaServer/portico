package searchtext

import (
	"slices"
	"testing"
)

// Each word of a long text is folded as Words folds it, and its place is counted in UTF-16
// units, as a reader counts: a character outside the basic plane counts two.
func TestSpansFoldAndPlaceEachWord(t *testing.T) {
	got := Spans("Éclair, naïve 𝒳x — 東京 Spider-Man")
	want := []Span{{"eclair", 0, 6}, {"naive", 8, 13}, {"𝒳x", 14, 17}, {"東", 20, 21}, {"京", 21, 22}, {"spider", 23, 29}, {"man", 30, 33}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	split, _ := Words("Éclair, naïve Spider-Man")
	var words []string
	for _, s := range Spans("Éclair, naïve Spider-Man") {
		words = append(words, s.Word)
	}
	if !slices.Equal(words, split) {
		t.Fatalf("Spans %v and Words %v disagree", words, split)
	}
}
