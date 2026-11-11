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
