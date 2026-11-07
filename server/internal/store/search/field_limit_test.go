package search_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestCJKFieldMatcherClipsOverlappingWindows(t *testing.T) {
	key := search.FieldKey{Subject: search.ManifestEntity, SubjectID: 1, PublicationID: 1, BankID: 1, InputGeneration: 1, Field: 4, Revision: "revision"}
	// Supplied windows deliberately include a legacy full normalized tail. The
	// reader must not confirm a term that the common 64KiB postings cannot find.
	for _, v := range []struct {
		name, value string
		want        bool
	}{
		{"inside", strings.Repeat("界 ", 16382) + "東京" + strings.Repeat("界", 400), true},
		{"crossing", strings.Repeat("界 ", 16383) + "東京" + strings.Repeat("界", 400), false},
		{"outside", strings.Repeat("界 ", 16384) + "東京", false},
	} {
		t.Run(v.name, func(t *testing.T) {
			m, err := search.NewCJKFieldMatcher(key, "東京", 4096)
			if err != nil {
				t.Fatal(err)
			}
			runes := []rune(v.value)
			for at := 0; at < len(runes); at += search.NormalizedCoreRunes {
				end := min(len(runes), at+search.NormalizedCoreRunes+search.NormalizedOverlapRunes)
				if err = m.Observe(context.Background(), key, string(runes[at:end])); err != nil {
					t.Fatal(err)
				}
			}
			_, got, err := m.Result(context.Background())
			if err != nil || got != v.want {
				t.Fatal(got, err)
			}
		})
	}
}
func TestPreparedFieldsShareNormalizedClip(t *testing.T) {
	raw := strings.Repeat("界 ", 17000) + "東京"
	plan, err := search.PrepareFragments(raw, search.DefaultFragmentBudget())
	if err != nil {
		t.Fatal(err)
	}
	var normalized strings.Builder
	for i := 0; i < plan.Len(); i++ {
		row := plan.At(i)
		if strings.Contains(row.LexicalTokens(), "東京") || strings.Contains(row.Normalized(), "東京") {
			t.Fatal("outside token retained")
		}
		runes := []rune(row.Normalized())
		if len(runes) > 0 {
			normalized.WriteString(string(runes[:min(search.NormalizedCoreRunes, len(runes))]))
		}
	}
	if normalized.String() != facettext.SearchFold(raw) || len(normalized.String()) > facettext.SearchFieldBytes || !utf8.ValidString(normalized.String()) {
		t.Fatal("fragment normalized prefix differs from native document")
	}
}
