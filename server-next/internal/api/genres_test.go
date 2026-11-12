package api

import "testing"

// Search before typing: the genres come at once without counts, and with counts=true each has
// one, for the profile; what the data layer holds back is tested there.
func TestGenresForSearch(t *testing.T) {
	s, token, limited := seeded(t)
	for _, who := range []string{token, limited} {
		plain := get[GenreList](t, s, who, "/v1/genres").Genres
		counted := get[GenreList](t, s, who, "/v1/genres?counts=true").Genres
		if len(plain) == 0 || len(plain) != len(counted) {
			t.Fatalf("%d genres, %d with counts", len(plain), len(counted))
		}
		for n, g := range plain {
			if g.Value == "" || g.Label == "" || g.Count != nil || counted[n].Value != g.Value || counted[n].Count == nil || *counted[n].Count < 1 {
				t.Fatalf("genre %d: %+v, with counts %+v", n, g, counted[n])
			}
		}
		// Opening a genre lists its titles.
		if page := get[ItemPage](t, s, who, "/v1/items?tag="+plain[0].Value+"&kind=movie,show&limit=1"); len(page.Items) == 0 {
			t.Fatalf("genre %s opens empty", plain[0].Label)
		}
	}
	if code := status(t, s, "GET", "/v1/genres?counts=perhaps", token, nil); code != 400 {
		t.Fatalf("counts=perhaps answered %d", code)
	}
}

// A music library's Genres tab: the genres of that one library, counted over its albums, for
// the profile. Each count is the number of albums the genre opens to in that library, for the
// owner and for a limited profile alike, and a genre of another library is not among them.
func TestGenresOfOneLibrary(t *testing.T) {
	s, token, limited := seeded(t)
	var music, films string
	for _, l := range get[Libraries](t, s, token, "/v1/libraries").Items {
		switch l.Kind {
		case "music":
			music = l.ID
		case "movies":
			films = l.ID
		}
	}
	everywhere := map[string]bool{}
	for _, g := range get[GenreList](t, s, token, "/v1/genres").Genres {
		everywhere[g.Value] = true
	}
	for _, who := range []string{token, limited} {
		counted := get[GenreList](t, s, who, "/v1/genres?library="+music).Genres
		albums := get[ItemPage](t, s, who, "/v1/items?library="+music+"&kind=album&total=true&limit=1")
		if (len(counted) == 0) != (*albums.Total == 0) {
			t.Fatalf("%d genres in a music library of %d albums", len(counted), *albums.Total)
		}
		for n, g := range counted {
			if g.Count == nil || n > 0 && counted[n-1].Label >= g.Label {
				t.Fatalf("genre %d: %+v after %+v", n, g, counted[max(n-1, 0)])
			}
			for _, by := range []string{"tag", "genre"} {
				albums := get[ItemPage](t, s, who, "/v1/items?library="+music+"&"+by+"="+g.Value+"&kind=album&total=true&limit=1")
				if albums.Total == nil {
					t.Fatalf("no total for %s", g.Label)
				}
				if *albums.Total != *g.Count || *g.Count < 1 {
					t.Errorf("%s counts %d albums; opened by %s= it lists %d", g.Label, *g.Count, by, *albums.Total)
				}
			}
		}
	}
	// The films' genres are the films': the two libraries' lists differ, and the server-wide
	// read (films and shows) is as it was.
	ofFilms := get[GenreList](t, s, token, "/v1/genres?library="+films).Genres
	if len(ofFilms) == 0 {
		t.Fatal("the film library has no genres of its own")
	}
	for _, g := range ofFilms {
		if !everywhere[g.Value] {
			t.Errorf("the film library's genre %s is not among the server's", g.Label)
		}
	}
	if code := status(t, s, "GET", "/v1/genres?library=lib_nothing", token, nil); code != 404 {
		t.Errorf("an unknown library's genres answered %d, want 404", code)
	}
}
