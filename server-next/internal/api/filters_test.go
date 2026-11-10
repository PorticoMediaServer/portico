package api

import (
	"fmt"
	"strings"
	"testing"
)

// A filter takes several values that combine as any, all or none; filters combine as all;
// a choice's count is what the list then holds; a restricted profile is shown no more through
// a filter than without one; and a filter that is too long is refused.
func TestFiltersTakeSeveralValues(t *testing.T) {
	s, token, limited := seeded(t)
	library := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	list := "/v1/items?library=" + library + "&kind=movie&total=true&limit=200"
	total := func(token, filter string) int {
		t.Helper()
		return *get[ItemPage](t, s, token, list+filter).Total
	}
	all := total(token, "")
	genres := get[Facets](t, s, token, "/v1/facets?facet=genre&library="+library+"&kind=movie").Values
	if len(genres) < 3 {
		t.Fatalf("%d genres", len(genres))
	}
	// Two genres that some film carries both of.
	a, b, both := genres[0], genres[1], 0
	for _, other := range genres[1:] {
		if n := total(token, "&genre=all:"+a.Value+","+other.Value); n > 0 {
			b, both = other, n
			break
		}
	}
	if either := total(token, "&genre="+a.Value+","+b.Value); either != a.Count+b.Count-both || both == 0 || both >= a.Count {
		t.Fatalf("any of two genres is %d, with %d and %d of which %d have both", either, a.Count, b.Count, both)
	}
	if none := total(token, "&genre=none:"+a.Value); none != all-a.Count {
		t.Fatalf("none of a genre is %d of %d, and the genre has %d", none, all, a.Count)
	}
	// The same filter sent twice has both to pass, and a genre goes by name as well as by id.
	if twice := total(token, "&genre="+a.Value+"&genre="+b.Value); twice != both {
		t.Fatalf("two genre filters leave %d, all of the two leaves %d", twice, both)
	}
	if named := total(token, "&genre="+strings.ReplaceAll(a.Label, " ", "%20")); named != a.Count {
		t.Fatalf("the genre by name leaves %d, by id %d", named, a.Count)
	}
	// Choosing a genre does not empty the list of genres, and the other counts follow it.
	if still := get[Facets](t, s, token, "/v1/facets?facet=genre&library="+library+"&kind=movie&genre="+a.Value).Values; len(still) != len(genres) {
		t.Fatalf("with a genre chosen the genre choices went from %d to %d", len(genres), len(still))
	}
	for _, rating := range get[Facets](t, s, token, "/v1/facets?facet=contentRating&library="+library+"&kind=movie&genre="+a.Value).Values[:2] {
		if got := total(token, "&genre="+a.Value+"&contentRating="+rating.Value); got != rating.Count {
			t.Fatalf("the count beside %s says %d and the list holds %d", rating.Value, rating.Count, got)
		}
	}
	decade := get[Facets](t, s, token, "/v1/facets?facet=decade&library="+library+"&kind=movie").Values[0]
	if got := total(token, "&year="+decade.Value); got != decade.Count {
		t.Fatalf("the %s hold %d films and the year range %d", decade.Label, decade.Count, got)
	}

	// Numbers, days and people.
	page := get[ItemPage](t, s, token, list+"&communityRating=7..")
	if len(page.Items) == 0 || *page.Total >= all {
		t.Fatalf("rated 7 and over: %d of %d", *page.Total, all)
	}
	for _, film := range page.Items {
		if film.Ratings == nil || film.Ratings.Community < 7 {
			t.Fatalf("%q is in the list of films rated 7 and over with %+v", film.Title, film.Ratings)
		}
	}
	if low := total(token, "&communityRating=..6.9"); low+*page.Total != total(token, "&communityRating=0..") {
		t.Fatalf("under 7 (%d) and 7 and over (%d) do not add up to the rated films", low, *page.Total)
	}
	if recent, never := total(token, "&added=2000-01-01.."), total(token, "&added=..1999-12-31"); recent != all || never != 0 {
		t.Fatalf("added since 2000: %d of %d, before: %d", recent, all, never)
	}
	film := get[ItemPage](t, s, token, list).Items[0]
	actor := get[CreditPage](t, s, token, "/v1/items/"+film.ID+"/credits?part=cast&limit=1").Items[0]
	cast := get[ItemPage](t, s, token, list+"&actor="+actor.PersonID)
	if *cast.Total == 0 || *cast.Total > *get[ItemPage](t, s, token, "/v1/items?total=true&person="+actor.PersonID).Total {
		t.Fatalf("an actor's films: %d", *cast.Total)
	}
	if without := total(token, "&actor=none:"+actor.PersonID); without != all-*cast.Total {
		t.Fatalf("films without the actor: %d of %d, with: %d", without, all, *cast.Total)
	}
	// My own state: nothing is in progress until something is.
	if total(token, "&inProgress=true") != 0 {
		t.Fatal("a film is in progress before anything was played")
	}
	call(t, s, "PUT", "/v1/items/"+film.ID+"/state", token, map[string]any{"positionMs": 60_000}, nil)
	if total(token, "&inProgress=true") != 1 || total(limited, "&inProgress=true") != 0 || total(token, "&inProgress=false") != all-1 {
		t.Fatal("the film in progress is not the one film in progress, for its own profile only")
	}

	// The same list by POST, for a filter too long for an address.
	var posted ItemPage
	body := ItemQueryRequest{Library: library, Kind: []string{"movie"}, Total: true,
		Filter: []Condition{{Field: "genre", Mode: "all", Values: []string{a.Value, b.Value}}, {Field: "communityRating", Values: []string{"7.."}}}}
	if w := call(t, s, "POST", "/v1/items/query", token, body, &posted); w.Code != 200 || *posted.Total != total(token, "&genre=all:"+a.Value+","+b.Value+"&communityRating=7..") {
		t.Fatalf("the list by POST: %d, total %v", w.Code, posted.Total)
	}

	// A profile limited by rating is shown fewer through every filter, and nothing it may not open.
	for _, filter := range []string{"&genre=" + a.Value, "&genre=none:" + a.Value, "&communityRating=7..", "&actor=" + actor.PersonID} {
		theirs := get[ItemPage](t, s, limited, list+filter)
		if *theirs.Total > total(token, filter) || *theirs.Total != len(theirs.Items) {
			t.Fatalf("%s: the limited profile is shown %d, everyone %d", filter, *theirs.Total, total(token, filter))
		}
		for _, item := range theirs.Items {
			if code := status(t, s, "GET", "/v1/items/"+item.ID, limited, nil); code != 200 {
				t.Fatalf("%s showed the limited profile %q, which it may not open", filter, item.Title)
			}
		}
	}

	long := make([]Condition, 41)
	for n := range long {
		long[n] = Condition{Field: "year", Values: []string{fmt.Sprint(1950 + n)}}
	}
	var refused errorBody
	if w := call(t, s, "POST", "/v1/items/query", token, ItemQueryRequest{Library: library, Filter: long}, &refused); w.Code != 400 || refused.Error.Message != "This filter is too long." {
		t.Fatalf("41 conditions: %d %q", w.Code, refused.Error.Message)
	}
	if code := status(t, s, "POST", "/v1/items/query", token, ItemQueryRequest{Library: library,
		Filter: []Condition{{Field: "year", Values: make([]string, 101)}}}); code != 400 {
		t.Fatalf("101 values answered %d", code)
	}
	for _, bad := range []string{"&communityRating=high", "&added=yesterday", "&inProgress=maybe", "&resolution=8k", "&year=19x"} {
		if code := status(t, s, "GET", list+bad, token, nil); code != 400 {
			t.Errorf("%s answered %d, want 400", bad, code)
		}
	}
}
