package workload

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// A synthetic population shaped like the canonical movie titles: eight phrases
// with a seven-digit ordinal. Pure data; no bank, snapshot or runtime is built.
func fullSearchTestTitle(ordinal int) string {
	phrases := [...]string{"The Amber River", "A Quiet Orbit", "An Autumn Map", "Blue Harbour", "Étoile", "9 Lanterns", "North & South", "The Glass Island"}
	if ordinal%11 == 0 {
		// The generator's explicit-title cohort.
		return fmt.Sprintf("Archive %07d", ordinal)
	}
	return fmt.Sprintf("%s %07d", phrases[ordinal%len(phrases)], ordinal)
}

func fullSearchTestOracle(t *testing.T, n int) *FullSearchOracle {
	t.Helper()
	o, err := NewFullSearchOracle([]string{fullSearchTestTitle(42), fullSearchTestTitle(1001)})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		o.Observe(int64(100000+i), fullSearchTestTitle(i))
	}
	return o
}

func TestFullSearchOracleClassesOnACanonicalShapedPopulation(t *testing.T) {
	o := fullSearchTestOracle(t, 40000)
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	counts := map[FullSearchClass]int{}
	for _, term := range o.Terms {
		counts[term.Class]++
		if term.Query.Mode() != search.ReadFull {
			t.Fatal("term is not a full-mode query", term.Text)
		}
	}
	if counts[FullSearchRare] != 2 || counts[FullSearchConjunction] != 4 || counts[FullSearchBroad] != len(broadWords) || counts[FullSearchNone] != 1 {
		t.Fatal("mix shape", counts)
	}
	// A population too small for the broad words is reported, not mislabelled.
	if err := fullSearchTestOracle(t, 100).Validate(); err == nil {
		t.Fatal("tiny population accepted as a broad mix")
	}
}

func fullSearchPage(ids []int64, o *FullSearchOracle, exhausted bool) read.FullSearchPage {
	p := read.FullSearchPage{Disposition: read.SearchResults, Exhausted: exhausted, Partial: !exhausted, CountStatus: read.SearchCountUnknown}
	for _, id := range ids {
		p.Hits = append(p.Hits, read.FullSearchHit{Subject: search.ManifestEntity, ID: id, Item: read.BrowseItem{ID: id, Title: fullSearchTestTitle(int(id - 100000))}})
	}
	return p
}

func TestFullSearchOracleChecksPages(t *testing.T) {
	o := fullSearchTestOracle(t, 40000)
	rare := 0 // term 0 is the rare token of ordinal 42
	if o.Terms[rare].Class != FullSearchRare {
		t.Fatal("unexpected term order")
	}
	if err := o.Check(rare, fullSearchPage([]int64{100042}, o, true)); err != nil {
		t.Fatal("exact rare page refused", err)
	}
	for name, page := range map[string]read.FullSearchPage{
		"missing hit":    fullSearchPage(nil, o, true),
		"wrong entity":   fullSearchPage([]int64{100043}, o, true),
		"extra entity":   fullSearchPage([]int64{100042, 100050}, o, true),
		"unavailable":    {Disposition: read.SearchUnavailable},
		"over budget":    {Disposition: read.SearchResults, NativeCandidates: read.NativePreviewCandidates + 1},
		"inflated bound": {Disposition: read.SearchResults, CountStatus: read.SearchCountLowerBound, Count: 5, Partial: true},
	} {
		if err := o.Check(rare, page); err == nil {
			t.Fatal("bad rare page accepted", name)
		}
	}
	if err := o.Check(rare, fullSearchPage(nil, o, false)); err != nil {
		t.Fatal("a pending partial poll is a legal intermediate page", err)
	}
	if err := o.Check(rare, read.FullSearchPage{Disposition: read.SearchUnavailable}); !errors.Is(err, ErrFullSearchBankAbsent) {
		t.Fatal("absent bank must be its own typed gap", err)
	}
	var broad, none int
	for i, term := range o.Terms {
		if term.Text == "the" {
			broad = i
		}
		if term.Class == FullSearchNone {
			none = i
		}
	}
	good := fullSearchPage([]int64{100007, 100008, 100015}, o, false) // "The ..." titles (see the phrase table)
	good.CountStatus, good.Count, good.NativeCandidates = read.SearchCountLowerBound, 3, 1
	if err := o.Check(broad, good); err != nil {
		t.Fatal("partial broad preview refused", err)
	}
	if err := o.Check(broad, fullSearchPage([]int64{100000}, o, true)); err == nil {
		t.Fatal("exact exhausted claim over a broad term accepted")
	}
	if err := o.Check(broad, fullSearchPage([]int64{100001}, o, false)); err == nil {
		t.Fatal("non-matching title accepted for a broad term")
	}
	if err := o.Check(none, fullSearchPage([]int64{100000}, o, true)); err == nil {
		t.Fatal("hits for a none term accepted")
	}
}
