package db_test

import (
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The one-library form of the genres read, for a film library: for every seeded profile (none,
// rating limit, library limit, blocked label), each genre shows at most two posters, each of a
// different film that profile may see in that genre, the same on two reads.
func TestOneLibrarysGenrePostersAreOfTitlesTheProfileMaySee(t *testing.T) {
	l := newLibrary(t, 1500)
	labels, err := l.d.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) == 0 {
		t.Fatalf("labels: %v %v", labels, err)
	}
	libraries, err := l.d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var films string
	for _, lib := range libraries {
		if lib.Kind == db.LibraryMovies {
			films = lib.PublicID
		}
	}
	// The films whose posters the owner is shown get the label one profile is kept from.
	genres, err := l.d.Genres(ctx, l.all, films, false)
	if err != nil || len(genres) == 0 {
		t.Fatalf("the film library's genres: %d %v", len(genres), err)
	}
	labelled := 0
	for _, g := range genres {
		if len(g.Posters) < 2 || labelled > 0 {
			continue
		}
		for _, it := range everything(t, l.d, l.all, db.ItemQuery{Library: films, Tag: g.PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200}) {
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
		t.Fatalf("labelled %d films, want 2", labelled)
	}
	checked := 0
	for n, v := range l.viewers {
		genres, err := l.d.Genres(ctx, v, films, false)
		if err != nil {
			continue // a profile kept from the film library is refused it
		}
		again, _ := l.d.Genres(ctx, v, films, false)
		for i, g := range genres {
			if !slices.Equal(g.Posters, again[i].Posters) {
				t.Fatalf("viewer %d, genre %s: posters %v, then %v", n, g.Name, g.Posters, again[i].Posters)
			}
			poster := map[string]string{}
			for _, it := range everything(t, l.d, v, db.ItemQuery{Library: films, Tag: g.PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200}) {
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
					t.Fatalf("viewer %d, genre %s: poster %s is not of a different film it may see", n, g.Name, hash)
				}
				seen[title] = true
				checked++
			}
			if len(g.Posters) > 2 {
				t.Fatalf("viewer %d, genre %s: %d posters", n, g.Name, len(g.Posters))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no poster was checked")
	}
	t.Logf("%d posters checked over %d profiles", checked, len(l.viewers))
}
