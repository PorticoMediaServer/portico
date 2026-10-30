package facettext

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestShortPrefixesAreUniqueAndRankTitles(t *testing.T) {
	var got []SearchPrefix
	document := SearchDocument("Harbor Harbor Lights Context 000000000001")
	if err := json.Unmarshal([]byte(SearchPrefixes(document, "harbor lights")), &got); err != nil {
		t.Fatal(err)
	}
	values := map[string]int{}
	for _, p := range got {
		if _, duplicate := values[p.Prefix]; duplicate {
			t.Fatal(p)
		}
		values[p.Prefix] = p.Tier
	}
	for prefix, tier := range map[string]int{"harbor": 2, "light": 1, "conte": 0, "000000": 0} {
		if values[prefix] != tier {
			t.Fatalf("%s tier=%d", prefix, values[prefix])
		}
	}
	if values["0"] != 0 || values["000"] != 0 {
		t.Fatal("numeric prefixes are not posted")
	}
	if _, ok := values["0"]; !ok {
		t.Fatal("a typed digit has no posting")
	}
	if _, tooLong := values["0000000"]; tooLong {
		t.Fatal("long prefix indexed")
	}
	var exact []SearchPrefix
	json.Unmarshal([]byte(SearchPrefixes(SearchDocument("Harbor"), "harbor")), &exact)
	if exact[len(exact)-1].Tier != 2 {
		t.Fatal(exact)
	}
	if _, ok := IndexedSearchTerms("東京"); ok {
		t.Fatal("CJK path lost")
	}
	if _, ok := IndexedSearchTerms("Harborsharbors"); ok {
		t.Fatal("long token path lost")
	}
	if _, ok := IndexedSearchTerms("Longprefix"); !ok {
		t.Fatal("ordinary long text prefix fell back")
	}
	if _, ok := IndexedSearchTerms("0000000"); ok {
		t.Fatal("numeric terminal bound lost")
	}
	terms, ok := IndexedSearchTerms("Title 000000")
	if !ok || !reflect.DeepEqual(terms, []string{"title", "000000"}) {
		t.Fatal(terms, ok)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Prefix >= got[i].Prefix {
			t.Fatal("prefixes are not sorted")
		}
	}
}
