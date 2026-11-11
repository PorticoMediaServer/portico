package api

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The owner deletes a selection: the preview says what goes, the count typed confirms it,
// more than 200 at once is refused and never split, and nobody else may. An edit of a library
// made on an out-of-date copy is refused.
func TestTheOwnerDeletesASelectionAndEditsAreNotLost(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	root := filepath.Join(t.TempDir(), "films")
	for n, film := range []string{"Heat (1995).mkv", "Alien (1979).mkv", "Ronin (1998).mkv"} {
		writeFile(t, filepath.Join(root, film), 10+n)
	}
	var lib AdminLibrary
	create := LibraryCreate{Name: "Films", Kind: "movies", Folders: []string{root},
		Options: &LibraryOptions{Depth: "basic", Trash: TrashRule{Mode: "manual"}, AllowDelete: true}}
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, create, &lib); w.Code != 200 {
		t.Fatalf("creating the library answered %d: %s", w.Code, w.Body)
	}
	scanNow(t, s, owner, lib.ID)
	heat, alien := missingItemID(t, s, owner, lib.ID, "Heat"), missingItemID(t, s, owner, lib.ID, "Alien")
	picked := SelectionDelete{IDs: []string{heat, alien}}

	var preview SelectionDeletion
	if w := call(t, s, "POST", "/v1/admin/items/deletion", owner, picked, &preview); w.Code != 200 || preview.Count != 2 || preview.Titles != 2 ||
		preview.Files != 2 || preview.Bytes != 2*int64(len(sampleFilm.data))+21 {
		t.Fatalf("the preview: %d %s", w.Code, w.Body)
	}
	many := SelectionDelete{}
	for n := 0; n < 201; n++ {
		many.IDs = append(many.IDs, "itm_"+strconv.Itoa(n))
	}
	for name, bad := range map[string]SelectionDelete{"none": {}, "201": many, "twice": {IDs: []string{heat, heat}}, "gone": {IDs: []string{heat, "itm_nowhere"}}} {
		if code := status(t, s, "POST", "/v1/admin/items/deletion", owner, bad); code != 400 {
			t.Errorf("a selection of %s answered %d; want 400", name, code)
		}
	}
	for _, confirm := range []string{"", "?confirm=1", "?confirm=Heat"} {
		if code := status(t, s, "POST", "/v1/admin/items/delete"+confirm, owner, picked); code != 400 {
			t.Errorf("deleting with %q answered %d; want 400", confirm, code)
		}
	}
	member := memberToken(t, s, owner)
	if code := status(t, s, "POST", "/v1/admin/items/delete?confirm=2", member, picked); code != 403 {
		t.Errorf("a member deleting a selection answered %d", code)
	}
	var done SelectionDeleted
	if w := call(t, s, "POST", "/v1/admin/items/delete?confirm=2", owner, picked, &done); w.Code != 200 || done.Removed != 2 || done.FilesDeleted != 2 ||
		len(done.Kept) != 0 {
		t.Fatalf("the delete: %d %s", w.Code, w.Body)
	}
	for film, there := range map[string]bool{"Heat (1995).mkv": false, "Alien (1979).mkv": false, "Ronin (1998).mkv": true} {
		if _, err := os.Stat(filepath.Join(root, film)); (err == nil) != there {
			t.Errorf("%s on disk after the delete: %v", film, err)
		}
	}
	missingItemID(t, s, owner, lib.ID, "Ronin")
	// The same request again finds its titles gone and deletes nothing.
	if code := status(t, s, "POST", "/v1/admin/items/delete?confirm=2", owner, picked); code != 400 {
		t.Errorf("the delete sent again answered %d; want 400", code)
	}

	// Two edits made on the same copy of the library: the second is refused.
	base := "/v1/admin/libraries/" + lib.ID
	r := newRequest("GET", base, owner, nil)
	w := serve(s, r)
	var read AdminLibrary
	if w.Code != 200 || jsonInto(w, &read) != nil || read.Revision == "" || w.Header().Get("ETag") != `"`+read.Revision+`"` {
		t.Fatalf("reading the library: %d ETag %q revision %q", w.Code, w.Header().Get("ETag"), read.Revision)
	}
	edit := func(name, revision string) (int, AdminLibrary) {
		t.Helper()
		r := newRequest("PATCH", base, owner, LibraryEdit{Name: &name})
		if revision != "" {
			r.Header.Set("If-Match", `"`+revision+`"`)
		}
		w := serve(s, r)
		var out AdminLibrary
		jsonInto(w, &out)
		return w.Code, out
	}
	code, renamed := edit("Movies", read.Revision)
	if code != 200 || renamed.Name != "Movies" || renamed.Revision == read.Revision {
		t.Fatalf("an edit on the current copy answered %d, revision %q after %q", code, renamed.Revision, read.Revision)
	}
	if code, _ := edit("Cinema", read.Revision); code != 412 {
		t.Errorf("an edit on an out-of-date copy answered %d; want 412", code)
	}
	if code, _ := edit("Pictures", ""); code != 200 {
		t.Errorf("an edit that names no copy answered %d; want 200", code)
	}
}
