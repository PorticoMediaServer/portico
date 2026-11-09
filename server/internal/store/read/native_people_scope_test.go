package read

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
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

func nativeAddScopePerson(t *testing.T, f *browseFixture, id int64, name, token string, publication int64) {
	t.Helper()
	f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,search_incarnation) VALUES(?,?,?,?,?,zeroblob(16),zeroblob(16))`, id, token, "local:"+token, name, strings.ToLower(name))
	nativeFixtureImage(t, f, search.ManifestPerson, id, publication, []nativeFixtureField{{1, name}})
}

func nativeAddScopeCredit(t *testing.T, f *browseFixture, entity, image, person, ordinal int64, credited string) {
	t.Helper()
	f.exec(t, `INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) SELECT ?,id,metadata_incarnation,1,1,1,1,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16),1,zeroblob(32) FROM catalog_entities WHERE id=?`, image, entity)
	f.exec(t, `UPDATE catalog_entities SET credit_image_id=? WHERE id=?`, image, entity)
	f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(?,?,?,?,1,2,?,'test','credit',0)`, image, entity, ordinal, person, credited)
}

func nativePersonScopeKeys(page FullSearchPage) []fullResultKey {
	var keys []fullResultKey
	for _, hit := range page.Hits {
		if hit.Subject == search.ManifestPerson {
			keys = append(keys, hitRank(hit).key)
		}
	}
	return keys
}

func nativeScopeAgreement(t *testing.T, f *browseFixture, r *NativeFullSearchReader, text string) (FullSearchPage, FullSearchPage) {
	t.Helper()
	all := nativeScopeWalk(t, f, r, text, false)
	people := nativeScopeWalk(t, f, r, text, true)
	if got, want := nativePersonScopeKeys(people), nativePersonScopeKeys(all); !reflect.DeepEqual(got, want) {
		t.Fatalf("%q: person cards differ people=%+v all=%+v", text, got, want)
	}
	return all, people
}

// TestNativeSearchPeopleCursorIsBoundToItsScope replays a continuation across
// the two subject scopes in both directions. The native window refuses the
// mix-up before any cursor-held position is consumed, and boundTo refuses it
// again, so removing either check alone still fails this test.
func TestNativeSearchPeopleCursorIsBoundToItsScope(t *testing.T) {
	f, r := newNativeFixture(t)
	f.exec(t, "BEGIN")
	f.exec(t, `PRAGMA defer_foreign_keys=ON`)
	nativeAddScopePerson(t, f, 2, "Keanu Alt", "keanu-alt", 1002)
	nativeAddScopeCredit(t, f, 200, 2, 2, 0, "Alt")
	nativeFixtureImage(t, f, search.ManifestEntity, 200, 2000, []nativeFixtureField{{1, "Alt work"}})
	f.exec(t, "COMMIT")
	f.input.Limit = 1

	all, err := nativeScopeRead(t, f, r, "keanu", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Next == nil || all.Next.people {
		t.Fatalf("all-subject walk did not issue an all-subject continuation: %+v", all)
	}
	people, err := nativeScopeRead(t, f, r, "keanu", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if people.Next == nil || !people.Next.people {
		t.Fatalf("People walk did not issue a People continuation: %+v", people)
	}
	if _, err = nativeScopeRead(t, f, r, "keanu", false, people.Next); !errors.Is(err, ErrStale) {
		t.Fatalf("People cursor replayed into the all-subject walk: %v", err)
	}
	if _, err = nativeScopeRead(t, f, r, "keanu", true, all.Next); !errors.Is(err, ErrStale) {
		t.Fatalf("all-subject cursor replayed into the People walk: %v", err)
	}

	// Defence in depth: boundTo alone refuses the same replay even when the
	// native-window guard is removed.
	q, err := search.PlanQuery("keanu", false)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := newFullQueryScope(q, 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	scope.peopleOnly = true
	bound := &FullSearchCursor{query: q.Fold(), bank: 1, revision: 1, input: 0, people: true}
	if !bound.boundTo(scope) {
		t.Fatal("a People cursor must bind to a People scope")
	}
	scope.peopleOnly = false
	if bound.boundTo(scope) {
		t.Fatal("a People cursor bound to an all-subject scope")
	}
}

// TestNativeSearchPeopleScopeExcludesUncreditedAndInvisiblePersons holds the
// People scope to the same visibility proof as the all-subject walk. An orphan
// person, a person whose only credit is on another library's work, and a person
// whose only credit is on a restricted work are each absent from both scopes,
// and every other person card is identical across the two scopes.
func TestNativeSearchPeopleScopeExcludesUncreditedAndInvisiblePersons(t *testing.T) {
	f, r := newNativeFixture(t)
	f.exec(t, "BEGIN")
	f.exec(t, `PRAGMA defer_foreign_keys=ON`)
	nativeAddScopePerson(t, f, 2, "Orphan Otto", "orphan-otto", 1002)
	nativeAddScopePerson(t, f, 3, "Other Olive", "other-olive", 1003)
	nativeAddScopeCredit(t, f, 1040, 2, 3, 0, "Elsewhere")
	nativeFixtureImage(t, f, search.ManifestEntity, 1040, 2040, []nativeFixtureField{{1, "Elsewhere credit"}})
	nativeAddScopePerson(t, f, 4, "Restricted Rita", "restricted-rita", 1004)
	nativeAddScopeCredit(t, f, 202, 3, 4, 0, "Restricted")
	nativeFixtureImage(t, f, search.ManifestEntity, 202, 2042, []nativeFixtureField{{1, "Restricted credit"}})
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=202`)
	f.exec(t, "COMMIT")

	// A visible person must still agree, so the empty cases below cannot pass
	// merely because both scopes returned nothing.
	all, people := nativeScopeAgreement(t, f, r, "keanu")
	if want := nativePersonScopeKeys(people); !reflect.DeepEqual(want, []fullResultKey{{search.ManifestPerson, 1, 0}}) {
		t.Fatalf("visible control person %+v all %+v", want, all)
	}

	for _, c := range []struct {
		name   string
		text   string
		person int64
	}{
		{"orphan", "orphan otto", 2},
		{"other library only", "other olive", 3},
	} {
		all, people := nativeScopeAgreement(t, f, r, c.text)
		for _, page := range []FullSearchPage{all, people} {
			for _, hit := range page.Hits {
				if hit.Subject == search.ManifestPerson && hit.ID == c.person {
					t.Fatalf("%s: ineligible person selected %+v", c.name, page.Hits)
				}
			}
		}
	}

	// A restricted work denies the person in BOTH scopes exactly as it denies
	// the catalogue card; the People scope is a filter, not a bypass.
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	all, people = nativeScopeAgreement(t, f, r, "restricted rita")
	for _, page := range []FullSearchPage{all, people} {
		for _, hit := range page.Hits {
			if hit.Subject == search.ManifestPerson && hit.ID == 4 {
				t.Fatalf("restricted person selected %+v", page.Hits)
			}
		}
	}

	// The copy-limit choice and its last charge are unit-level: a People window
	// uses the 4 MiB grant while the catalogue window refuses above 2 MiB, and
	// dropping the stamp metadata charge would make the 4 MiB case pass.
	t.Run("copy limits", func(t *testing.T) {
		if got := nativeCopyLimit(true); got != 4<<20 {
			t.Fatalf("People copy cap %d", got)
		}
		if got := nativeCopyLimit(false); got != 2<<20 {
			t.Fatalf("catalogue copy cap %d", got)
		}
		for _, c := range []struct {
			name     string
			people   bool
			copies   int64
			metadata int64
			refuse   bool
		}{
			{"catalogue above 2 MiB", false, 2<<20 - 128<<10 + 1, 128 << 10, true},
			{"people fits where catalogue refuses", true, 2<<20 - 128<<10 + 1, 128 << 10, false},
			{"people above 4 MiB", true, 4<<20 - 128<<10 + 1, 128 << 10, true},
			{"metadata above stamp cap", true, 0, MaxNativeStampBytes + 1, true},
		} {
			_, err := nativeStampCharge(c.copies, c.metadata, nativeCopyLimit(c.people))
			if (err != nil) != c.refuse {
				t.Fatalf("%s: %v", c.name, err)
			}
			if err != nil && !errors.Is(err, governor.ErrSnapshotBudget) {
				t.Fatalf("%s: %v", c.name, err)
			}
		}
	})
}
