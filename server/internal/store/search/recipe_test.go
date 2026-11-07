package search_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// Expected words/folds are literal recipe witnesses, independent of facettext
// and the implementation under test. Encode with fmt rather than its encoder.
func scalarHex(words string) string {
	var tokens []string
	for _, word := range strings.Fields(words) {
		var b strings.Builder
		for _, r := range word {
			fmt.Fprintf(&b, "%06x", r)
		}
		tokens = append(tokens, b.String())
	}
	return strings.Join(tokens, " ")
}

func TestRecipeGolden(t *testing.T) {
	for _, tt := range []struct{ input, fold, lexical, prefix, initial string }{
		{"", "", "", "", ""},
		{"  Amélie  ", "amelie", "amelie", "amelie", "amelie"},
		{"Spider-Man", "spiderman", "spiderman spider man", "spiderman spider man", "spiderman"},
		{"WALL·E", "walle", "walle wall e", "walle wall e", "walle"},
		{"Straße ﬃ ÆŒ ØŁĐ Ð", "strasse ffi aeoe old d", "strasse ffi aeoe old d", "strasse ffi aeoe old d", "strasse"},
		{"東京物語", "東京物語", "東京物語 東 東京 京 京物 物 物語 語", "東 東京 京 京物 物 物語 語", "東京物語"},
		{"王菲", "王菲", "王菲 王 菲", "王菲 王 菲", "王菲"},
		{"王菲Live", "王菲live", "王菲live 王 王菲 菲", "王 王菲 菲", "王菲live"},
		{"東 京", "東 京", "東 京", "東 京", "東"},
		{"東-京", "東京", "東京 東 京", "東京 東 京", "東京"},
		{"𠀀山", "𠀀山", "𠀀山 𠀀 山", "𠀀山 𠀀 山", "𠀀山"},
		{"ひらがな", "ひらかな", "ひらかな ひ ひら ら らか か かな な", "ひ ひら ら らか か かな な", "ひらかな"},
		{"박지은", "박지은", "박지은 박 박지 지 지은 은", "박 박지 지 지은 은", "박지은"},
		{"1234567890 abc123456789012", "1234567890 abc123456789012", "1234567890 abc123456789012", "123456 abc123456789", "123456"},
		{"１２３４５６７", "１２３４５６７", "１２３４５６７", "１２３４５６", "１２３４５６"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got := search.PrepareTitle(tt.input)
			if got.Title() != tt.input || got.Fold() != tt.fold || got.PrefixTokens() != scalarHex(tt.prefix) || got.InitialTokens() != scalarHex(tt.initial) {
				t.Fatalf("image mismatch: title=%q fold=%q prefix=%q initial=%q", got.Title(), got.Fold(), got.PrefixTokens(), got.InitialTokens())
			}
			if names := search.PreparePersonName(tt.input); names != tt.lexical {
				t.Fatalf("People tokens=%q want %q", names, tt.lexical)
			}
		})
	}
}

func TestRecipeNormalizedFieldClip(t *testing.T) {
	value := strings.Repeat("x ", 32767) + "東 omega"
	if got := search.PreparePersonName(value); got != "x" {
		t.Fatalf("outside field token was indexed: %q", got)
	}
	folded := search.PrepareTitle(value).Fold()
	if len(folded) > 64<<10 || strings.Contains(folded, "東") || strings.Contains(folded, "omega") {
		t.Fatal("title confirmation retained outside field text")
	}
}
