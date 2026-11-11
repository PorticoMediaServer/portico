package api

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// collections lists a library's collections with the titles each holds, in order.
func (sh *shelf) collections() map[string][]string {
	sh.t.Helper()
	var page ItemPage
	sh.do("GET", "/v1/items?kind=collection&library="+sh.library, nil, &page, 200)
	out := map[string][]string{}
	for _, c := range page.Items {
		out[c.ID+" "+c.Title] = []string{}
		var related Related
		sh.do("GET", "/v1/items/"+c.ID+"/related", nil, &related, 200)
		for _, g := range related.Groups {
			for _, it := range g.Items {
				if g.Relation == "collection_member" {
					out[c.ID+" "+c.Title] = append(out[c.ID+" "+c.Title], it.Title)
				}
			}
		}
	}
	return out
}

func (sh *shelf) setGroupSeries(on bool) {
	sh.t.Helper()
	var lib AdminLibrary
	sh.do("GET", "/v1/admin/libraries/"+sh.library, nil, &lib, 200)
	options := lib.Options
	options.GroupSeries = &on
	sh.do("PATCH", "/v1/admin/libraries/"+sh.library, LibraryEdit{Options: &options}, nil, 200)
}

// matrix is the one automatic collection's id and films, failing the test unless there is
// exactly one collection named The Matrix.
func (sh *shelf) matrix() (string, []string) {
	sh.t.Helper()
	got := sh.collections()
	for name, films := range got {
		if len(got) == 1 && strings.HasSuffix(name, " The Matrix") {
			return strings.TrimSuffix(name, " The Matrix"), films
		}
	}
	sh.t.Fatalf("collections: %v", got)
	return "", nil
}

func (sh *shelf) addFilm(name string) {
	sh.t.Helper()
	if err := os.WriteFile(filepath.Join(sh.root, name), make([]byte, 50+len(name)), 0o644); err != nil {
		sh.t.Fatal(err)
	}
	sh.startJob(JobStart{Kind: "scan", Target: JobTarget{Library: sh.library}})
	sh.startJob(JobStart{Kind: "match_library", Target: JobTarget{Library: sh.library}}) // after the scan's own lookup
}

// Films of one series are grouped once two are in the library, named without "Collection", in
// release order, and later films join. A film the owner takes out stays out, and only that
// one. With grouping turned off a collection nobody changed goes; one the owner changed stays
// as an ordinary collection and is not made again beside it. A deleted one does not come back.
func TestAutomaticCollections(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv")
	if got := sh.collections(); len(got) != 0 {
		t.Fatalf("one film of a series made %v", got)
	}
	sh.addFilm("The Matrix Reloaded (2003).mkv")
	id, films := sh.matrix()
	if !slices.Equal(films, []string{"The Matrix", "The Matrix Reloaded"}) {
		t.Fatalf("the series: %v", films)
	}

	// Grouping off: nobody changed it, so it goes.
	sh.setGroupSeries(false)
	if got := sh.collections(); len(got) != 0 {
		t.Fatalf("with grouping off: %v", got)
	}
	sh.setGroupSeries(true)
	sh.startJob(JobStart{Kind: "refresh_library", Target: JobTarget{Library: sh.library}})
	id, _ = sh.matrix()

	// Take Reloaded out: it stays out through a refresh, and a new film still joins.
	var page ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &page, 200)
	var reloaded string
	for _, film := range page.Items {
		if film.Title == "The Matrix Reloaded" {
			reloaded = film.ID
		}
	}
	sh.do("PATCH", "/v1/admin/collections/"+id, CollectionEdit{Remove: []string{reloaded}}, nil, 204)
	sh.addFilm("The Matrix Revolutions (2003).mkv")
	sh.startJob(JobStart{Kind: "refresh_library", Target: JobTarget{Library: sh.library}})
	if _, films := sh.matrix(); !slices.Equal(films, []string{"The Matrix", "The Matrix Revolutions"}) {
		t.Fatalf("after taking one film out and adding another: %v", films)
	}

	// Grouping off again: the owner changed this one, so it stays, as an ordinary collection,
	// and turning grouping back on does not make a second.
	sh.setGroupSeries(false)
	if kept, _ := sh.matrix(); kept != id {
		t.Fatalf("a collection the owner changed went when grouping was turned off")
	}
	sh.setGroupSeries(true)
	sh.startJob(JobStart{Kind: "refresh_library", Target: JobTarget{Library: sh.library}})
	if kept, films := sh.matrix(); kept != id || len(films) != 2 {
		t.Fatalf("after grouping came back: %s %v", kept, films)
	}

	// Deleting it: it does not come back through an options save or a refresh.
	sh.do("DELETE", "/v1/admin/collections/"+id, nil, nil, 204)
	sh.setGroupSeries(true)
	sh.startJob(JobStart{Kind: "refresh_library", Target: JobTarget{Library: sh.library}})
	if got := sh.collections(); len(got) != 0 {
		t.Fatalf("a deleted collection came back: %v", got)
	}
}
