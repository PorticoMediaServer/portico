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

	// A show selected together with one of its episodes: the episode is counted once, under
	// the show, in the number the owner is shown before confirming.
	shows := filepath.Join(t.TempDir(), "shows")
	for n := 1; n <= 3; n++ {
		writeFile(t, filepath.Join(shows, "Lost", "Season 1", "Lost - S01E0"+strconv.Itoa(n)+".mkv"), 30+n)
	}
	var tv AdminLibrary
	create = LibraryCreate{Name: "Shows", Kind: "shows", Folders: []string{shows},
		Options: &LibraryOptions{Depth: "basic", Trash: TrashRule{Mode: "manual"}, AllowDelete: true}}
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, create, &tv); w.Code != 200 {
		t.Fatalf("creating the show library answered %d: %s", w.Code, w.Body)
	}
	scanNow(t, s, owner, tv.ID)
	listed := func(kind string) []string {
		t.Helper()
		var page struct {
			Items []struct{ ID string }
		}
		if w := call(t, s, "GET", "/v1/items?kind="+kind+"&library="+tv.ID, owner, nil, &page); w.Code != 200 {
			t.Fatalf("listing %ss answered %d: %s", kind, w.Code, w.Body)
		}
		var ids []string
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		return ids
	}
	show, episodes := listed("show"), listed("episode")
	if len(show) != 1 || len(episodes) != 3 {
		t.Fatalf("the show library: %d shows, %d episodes", len(show), len(episodes))
	}
	var whole, nested SelectionDeletion
	call(t, s, "POST", "/v1/admin/items/deletion", owner, SelectionDelete{IDs: show}, &whole)
	if w := call(t, s, "POST", "/v1/admin/items/deletion", owner, SelectionDelete{IDs: []string{episodes[0], show[0]}}, &nested); w.Code != 200 ||
		nested.Count != 2 || nested.Titles != whole.Titles || nested.Files != 3 || whole.Titles != 5 {
		t.Fatalf("a show with one of its episodes: %s; the show alone is %d titles", w.Body, whole.Titles)
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
