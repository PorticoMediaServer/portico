package apikit

import (
	"strings"
	"testing"
)

func TestFileInfoQueryPathGrammarIsBoundedAndRouteLocal(t *testing.T) {
	valid := []string{"open." + strings.Repeat("a", 43), strings.Repeat("a", 183) + "." + strings.Repeat("b", 154)}
	for _, id := range valid {
		if _, _, ok := matchRoute("/v1/file-info-queries/{queryId}", "/v1/file-info-queries/"+id); !ok {
			t.Fatal("valid preparation rejected")
		}
		if _, _, ok := matchRoute("/v1/items/{id}", "/v1/items/"+id); ok {
			t.Fatal("grammar escaped private route")
		}
	}
	for _, id := range []string{"open.", "open." + strings.Repeat("a", 44), strings.Repeat("a", 338), strings.Repeat("a", 183) + "." + strings.Repeat("b", 153), strings.Repeat("a", 183) + "." + strings.Repeat("b", 153) + ".", strings.Repeat("a", 183) + "." + strings.Repeat("b", 155)} {
		if _, _, ok := matchRoute("/v1/file-info-queries/{queryId}", "/v1/file-info-queries/"+id); ok {
			t.Fatal("malformed preparation accepted", len(id))
		}
	}
}
