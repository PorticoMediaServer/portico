package facettext

import (
	"strings"
	"testing"
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
