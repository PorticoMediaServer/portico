package api

import (
	"os"
	"path/filepath"
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

// Films of one series are grouped once two are in the library, named without "Collection", in
// release order; with Group films from the same series off they are not; a collection the
// owner deletes does not come back through a refresh or a change of the library's options.
func TestAutomaticCollections(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv")
	if got := sh.collections(); len(got) != 0 {
		t.Fatalf("one film of a series made %v", got)
	}
	sh.setGroupSeries(false)
	if err := os.WriteFile(filepath.Join(sh.root, "The Matrix Reloaded (2003).mkv"), make([]byte, 77), 0o644); err != nil {
		t.Fatal(err)
	}
	sh.startJob(JobStart{Kind: "scan", Target: JobTarget{Library: sh.library}})
	sh.startJob(JobStart{Kind: "match_library", Target: JobTarget{Library: sh.library}}) // after the scan's own lookup
	if got := sh.collections(); len(got) != 0 {
		t.Fatalf("with grouping off: %v", got)
	}
	sh.setGroupSeries(true)
	sh.startJob(JobStart{Kind: "refresh_library", Target: JobTarget{Library: sh.library}})
	got := sh.collections()
	var id string
	for name, films := range got {
		if len(got) != 1 || name[len(name)-len(" The Matrix"):] != " The Matrix" || len(films) != 2 || films[0] != "The Matrix" || films[1] != "The Matrix Reloaded" {
			t.Fatalf("with grouping on: %v", got)
		}
		id = name[:len(name)-len(" The Matrix")]
	}
	sh.do("DELETE", "/v1/admin/collections/"+id, nil, nil, 204)
	sh.setGroupSeries(true) // saving the options keeps what the owner removed
	sh.startJob(JobStart{Kind: "refresh_library", Target: JobTarget{Library: sh.library}})
	if got := sh.collections(); len(got) != 0 {
		t.Fatalf("a deleted collection came back: %v", got)
	}
}
