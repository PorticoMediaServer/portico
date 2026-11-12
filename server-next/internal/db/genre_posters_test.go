package db_test

import (
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A phone's genre tile draws two posters. For every way a profile can be limited, each genre's
// posters are at most two, each the poster of a different title that profile may see in that
// genre, the same on every read; and a genre with titles that have posters gets two.
func TestGenrePostersAreOfTitlesTheProfileMaySee(t *testing.T) {
	l := newLibrary(t, 1500)
	video := []db.ItemKind{db.KindMovie, db.KindShow}
	// The two titles whose posters a genre shows everyone get the label one profile is kept
	// from: that profile must be shown two others.
	labels, err := l.d.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) == 0 {
		t.Fatalf("labels: %v %v", labels, err)
	}
	genres, err := l.d.Genres(ctx, l.all, "", false)
	if err != nil {
		t.Fatal(err)
	}
	labelled := 0
	for _, g := range genres {
		if len(g.Posters) < 2 || labelled > 0 {
			continue
		}
		for _, it := range everything(t, l.d, l.all, db.ItemQuery{Tag: g.PublicID, Kinds: video, Limit: 200}) {
			for _, im := range it.Images {
				if im.Role == db.ImagePoster && slices.Contains(g.Posters, im.FileHash) {
					if err := db.PutLabel(l.d, it.PublicID, labels[0].Name, false); err != nil {
						t.Fatal(err)
					}
					labelled++
				}
			}
		}
	}
	if labelled != 2 {
		t.Fatalf("labelled %d titles, want 2", labelled)
	}
	full := 0
	for n, v := range l.viewers {
		genres, err := l.d.Genres(ctx, v, "", false)
		if err != nil {
			t.Fatal(err)
		}
		again, err := l.d.Genres(ctx, v, "", false)
		if err != nil {
			t.Fatal(err)
		}
		for i, g := range genres {
			if !slices.Equal(g.Posters, again[i].Posters) {
				t.Fatalf("viewer %d, genre %s: posters %v, then %v", n, g.Name, g.Posters, again[i].Posters)
			}
			poster := map[string]string{} // a poster's hash to its title, among the titles this profile may see
			for _, it := range everything(t, l.d, v, db.ItemQuery{Tag: g.PublicID, Kinds: video, Limit: 200}) {
				for _, im := range it.Images {
					if im.Role == db.ImagePoster {
						poster[im.FileHash] = it.PublicID
					}
				}
			}
			seen := map[string]bool{}
			for _, hash := range g.Posters {
				title, ok := poster[hash]
				if !ok || seen[title] {
					t.Fatalf("viewer %d, genre %s: poster %s is not of a different title it may see", n, g.Name, hash)
				}
				seen[title] = true
			}
			if len(g.Posters) > 2 || len(poster) >= 2 && len(g.Posters) != 2 {
				t.Fatalf("viewer %d, genre %s: %d posters of %d it could have", n, g.Name, len(g.Posters), len(poster))
			}
			if n == 0 && len(g.Posters) == 2 {
				full++
			}
		}
	}
	if full == 0 {
		t.Fatal("no genre has two posters for the owner; the test checks nothing")
	}
}
