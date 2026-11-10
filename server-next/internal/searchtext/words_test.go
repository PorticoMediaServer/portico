package searchtext

import (
	"strings"
	"testing"
)

func TestWords(t *testing.T) {
	for _, c := range []struct{ text, split, joined string }{
		{"The Godfather", "the godfather", ""},
		{"AMÉLIE", "amelie", ""},
		{"Spider-Man: Homecoming", "spider man homecoming", "spiderman homecoming"},
		{"WALL·E", "wall e", "walle"},
		{"Don't Look Up", "don t look up", "dont look up"},
		{"M*A*S*H", "m a s h", "mash"},
		{"  ¡¿ ...  ", "", ""},
		{"Zoë Saldaña", "zoe saldana", ""},
		{"Straße 9½", "strasse 9½", ""},
		{"千と千尋 2001", "千 と 千 尋 2001", ""},
		{"नाम", "नम", ""},
	} {
		split, joined := Words(c.text)
		if got := strings.Join(split, " "); got != c.split {
			t.Errorf("%q split into %q, want %q", c.text, got, c.split)
		}
		if got := strings.Join(joined, " "); got != c.joined {
			t.Errorf("%q joined into %q, want %q", c.text, got, c.joined)
		}
	}
	if split, _ := Words(strings.Repeat("word ", 500)); len(split) != maxWords {
		t.Errorf("a very long text gave %d words, want %d", len(split), maxWords)
	}
}
