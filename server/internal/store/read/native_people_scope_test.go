package read

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// nativeScopeRead drives one native read with an explicit subject scope. The
// zero value of PeopleOnly must keep the all-subject walk; only a People action
// sets it.
func nativeScopeRead(t *testing.T, f *browseFixture, r *NativeFullSearchReader, text string, peopleOnly bool, after *FullSearchCursor) (FullSearchPage, error) {
	t.Helper()
	q, err := search.PlanQuery(text, false)
	if err != nil {
		t.Fatal(err)
	}
	live, request, output := liveRequests("native-people-scope")
	request.Budget.RawRows = 4096
	var page FullSearchPage
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, _, e = r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input, After: after, PeopleOnly: peopleOnly}, q)
		return e
	})
	return page, err
}

func nativeScopeWalk(t *testing.T, f *browseFixture, r *NativeFullSearchReader, text string, peopleOnly bool) FullSearchPage {
	t.Helper()
	var after *FullSearchCursor
	for n := 0; n < 100; n++ {
		page, err := nativeScopeRead(t, f, r, text, peopleOnly, after)
		if err != nil {
			t.Fatalf("%s step%d: %v", text, n, err)
		}
		if page.Disposition == SearchPending {
			if page.Next == nil {
				t.Fatal("pending scope walk without a continuation", text)
			}
			after = page.Next
			continue
		}
		return page
	}
	t.Fatal("scope walk did not finish", text)
	return FullSearchPage{}
}

// TestNativeSearchPeopleScopeIsPersonOnly holds both halves of the scope: the
// zero value still proves the credited-work entity for "keanu", and a People
// scope returns only the person candidate, so a People action never pays for
// entity proofs its page cannot show.
func TestNativeSearchPeopleScopeIsPersonOnly(t *testing.T) {
	f, r := newNativeFixture(t)
	all := nativeScopeWalk(t, f, r, "keanu", false)
	var allKeys []fullResultKey
	for _, hit := range all.Hits {
		allKeys = append(allKeys, hitRank(hit).key)
	}
	want := []fullResultKey{{search.ManifestPerson, 1, 0}, {search.ManifestEntity, 2, 1}}
	if !reflect.DeepEqual(allKeys, want) {
		t.Fatalf("default all-subject walk %+v", all.Hits)
	}
	people := nativeScopeWalk(t, f, r, "keanu", true)
	if people.Disposition != SearchResults || !people.Exhausted || people.Partial || len(people.Hits) != 1 || people.Hits[0].Subject != search.ManifestPerson || people.Hits[0].ID != 1 {
		t.Fatalf("people scope %+v", people)
	}
	// "original appearance" names the Arrival credit only. The all-subject walk
	// returns that entity; the People scope returns an empty complete page.
	entity := nativeScopeWalk(t, f, r, "original appearance", false)
	if entity.Disposition != SearchResults || !entity.Exhausted || entity.Partial || len(entity.Hits) != 1 || entity.Hits[0].Subject != search.ManifestEntity || entity.Hits[0].ID != 2 {
		t.Fatalf("default entity hit %+v", entity)
	}
	if empty := nativeScopeWalk(t, f, r, "original appearance", true); !empty.Exhausted || empty.Partial || len(empty.Hits) != 0 {
		t.Fatalf("people scope leaked an entity %+v", empty)
	}
}

// TestNativeSearchPeopleScopeNeverWidensVisibility proves the scope is a filter,
// not a substitute for the person visibility proof: retiring the only credited
// work leaves the person with no current visible credit, so neither scope may
// return a card for them.
func TestNativeSearchPeopleScopeNeverWidensVisibility(t *testing.T) {
	f, r := newNativeFixture(t)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2`)
	for _, peopleOnly := range []bool{false, true} {
		page := nativeScopeWalk(t, f, r, "keanu", peopleOnly)
		if page.Disposition != SearchResults || !page.Exhausted || page.Partial || len(page.Hits) != 0 {
			t.Fatalf("retired work leaked peopleOnly=%v %+v", peopleOnly, page)
		}
	}
}
