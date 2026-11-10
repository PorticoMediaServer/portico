package api

import "testing"

// A list of a library, a title or a person the profile may not see answers "not found",
// exactly like one that does not exist and like opening that thing on its own.
func TestAListOfSomethingHiddenIsNotFound(t *testing.T) {
	s, owner, members := seededWith(t, 1500, 4)
	labelled, libraryLimited := members[1], members[2] // the seeder's viewer-1 and viewer-2
	notFound := func(token, path string) {
		t.Helper()
		if code := status(t, s, "GET", path, token, nil); code != 404 {
			t.Errorf("GET %s answered %d, want 404", path, code)
		}
	}
	var shows, music string
	for _, l := range get[Libraries](t, s, owner, "/v1/libraries").Items {
		switch l.Kind {
		case "shows":
			shows = l.ID
		case "music":
			music = l.ID
		}
	}
	for _, path := range []string{"/v1/items?library=" + shows, "/v1/items?library=" + shows + "&kind=show&total=true",
		"/v1/facets?facet=genre&library=" + shows} {
		notFound(libraryLimited, path)
	}
	for _, path := range []string{"/v1/items?library=lib_nothing", "/v1/items?parent=itm_nothing", "/v1/items?person=psn_nothing"} {
		notFound(owner, path)
	}
	// An album viewer-1 may not see, because of its label, is not found as a list either.
	hidden := ""
	for _, album := range get[ItemPage](t, s, owner, "/v1/items?library="+music+"&kind=album&limit=200").Items {
		if status(t, s, "GET", "/v1/items/"+album.ID, labelled, nil) == 404 {
			hidden = album.ID
			break
		}
	}
	if hidden == "" {
		t.Fatal("the seeded library has no album hidden from viewer-1")
	}
	notFound(labelled, "/v1/items?parent="+hidden)
	if tracks := get[ItemPage](t, s, owner, "/v1/items?parent="+hidden); len(tracks.Items) == 0 {
		t.Fatal("the owner sees no tracks on the album")
	}
}
