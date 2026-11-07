package facettext

import (
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSearchFieldNormalizedBoundary(t *testing.T) {
	// Fold contraction happens BEFORE the limit. This term is past 64KiB raw,
	// but inside the normalized field and must be discoverable and confirmable.
	raw := strings.Repeat("é ", 22000) + "東京"
	folded := SearchFold(raw)
	if !strings.HasSuffix(folded, "東京") || len(folded) > SearchFieldBytes || !strings.Contains(SearchDocument(raw), "東京") {
		t.Fatal("discovery and normalized contraction disagree")
	}
	// Never bisect a normalized UTF-8 scalar or index its outside partner.
	raw = strings.Repeat("界 ", 16383) + "中国"
	folded = SearchFold(raw)
	if len(folded) != 65535 || !utf8.ValidString(folded) || !strings.HasSuffix(folded, "中") || strings.Contains(SearchDocument(raw), "中国") {
		t.Fatal("normalized scalar boundary", len(folded))
	}
	if SearchFold("WALL·E Spider-Man") != "walle spiderman" || !strings.Contains(SearchDocument("WALL·E Spider-Man"), "wall e spider man") {
		t.Fatal("ordinary component tokens changed")
	}
}

func TestSearchFieldStreamingUnicodeParity(t *testing.T) {
	// Force the streaming path with normalizing, composing and punctuation
	// sequences on both sides of transformer buffer boundaries.
	for _, unit := range []string{"e\u0301 東·京 ", "가 Ø Ł æ œ Straße İ ", "α\u0313\u0301 β—γ\t"} {
		source := strings.Repeat(unit, 500) + "tail"
		want := sorttext.Fold(source)
		var b strings.Builder
		for _, r := range want {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				b.WriteRune(r)
			} else if unicode.IsSpace(r) {
				b.WriteByte(' ')
			}
		}
		want = strings.Join(strings.Fields(b.String()), " ")
		if got := SearchFold(source); got != want {
			t.Fatal("streaming normalization differs from whole-string fold")
		}
	}
}
