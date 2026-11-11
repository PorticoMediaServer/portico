package api

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The owner puts the libraries in order for everyone, and picks a folder in the explorer:
// folders only, by name, 200 at a time, hidden ones on request, and never the places that
// are not for files. Nobody else may do either.
func TestTheOwnerOrdersLibrariesAndExploresFolders(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	root := t.TempDir()
	for n := 0; n < 205; n++ {
		if err := os.MkdirAll(filepath.Join(root, "media", "Show "+strconv.Itoa(1000+n)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, folder := range []string{"media/.hidden", "media/@eaDir", "films", "Music"} {
		if err := os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "films"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	explore := func(query string) (int, FolderList) {
		t.Helper()
		var out FolderList
		w := call(t, s, "GET", "/v1/admin/filesystem?"+query, owner, nil, &out)
		return w.Code, out
	}
	names := func(list FolderList) (out []string) {
		for _, f := range list.Folders {
			out = append(out, f.Name)
		}
		return out
	}

	code, top := explore("")
	if code != 200 || len(top.Folders) == 0 || top.Parent != nil {
		t.Fatalf("the top: %d %+v", code, top)
	}
	code, here := explore("path=" + url.QueryEscape(root) + "&writable=true")
	if got := names(here); code != 200 || len(got) != 4 || got[0] != "films" || got[1] != "linked" || got[2] != "media" || got[3] != "Music" ||
		here.Parent == nil || *here.Parent != filepath.Dir(root) || here.Folders[0].Writable == nil || !*here.Folders[0].Writable {
		t.Fatalf("the folder: %d %+v", code, here)
	}
	code, media := explore("path=" + url.QueryEscape(filepath.Join(root, "media")))
	if code != 200 || len(media.Folders) != 200 || media.NextCursor == "" || media.Folders[0].Name != "Show 1000" {
		t.Fatalf("the first 200: %d, %d folders, next %q", code, len(media.Folders), media.NextCursor)
	}
	code, rest := explore("path=" + url.QueryEscape(filepath.Join(root, "media")) + "&cursor=" + media.NextCursor)
	if code != 200 || len(rest.Folders) != 5 || rest.NextCursor != "" {
		t.Fatalf("the rest: %d, %d folders", code, len(rest.Folders))
	}
	if _, all := explore("path=" + url.QueryEscape(filepath.Join(root, "media")) + "&hidden=true&cursor=200"); len(all.Folders) != 7 {
		t.Fatalf("with hidden folders asked for: %d after the first 200; want 7", len(all.Folders))
	}
	for _, bad := range []string{"path=/proc", "path=/sys/kernel", "path=media", "path=" + url.QueryEscape(filepath.Join(root, "nowhere")),
		"path=" + url.QueryEscape(filepath.Join(root, "notes.txt"))} {
		if code, _ := explore(bad); code != 400 {
			t.Errorf("%s answered %d; want 400", bad, code)
		}
	}

	// The order of libraries.
	var ids []string
	for _, name := range []string{"Films", "Music"} {
		var lib AdminLibrary
		kind := map[string]string{"Films": "movies", "Music": "music"}[name]
		folder := filepath.Join(root, map[string]string{"Films": "films", "Music": "Music"}[name])
		if w := call(t, s, "POST", "/v1/admin/libraries", owner, LibraryCreate{Name: name, Kind: kind, Folders: []string{folder}}, &lib); w.Code != 200 {
			t.Fatalf("creating %s answered %d: %s", name, w.Code, w.Body)
		}
		ids = append(ids, lib.ID)
	}
	order := func() []string {
		t.Helper()
		var page Page[Library]
		if w := call(t, s, "GET", "/v1/libraries", owner, nil, &page); w.Code != 200 {
			t.Fatalf("listing libraries answered %d", w.Code)
		}
		var out []string
		for _, lib := range page.Items {
			out = append(out, lib.Name)
		}
		return out
	}
	if code := status(t, s, "PUT", "/v1/admin/libraries/order", owner, LibraryOrder{IDs: []string{ids[1], ids[0]}}); code != 204 {
		t.Fatalf("ordering answered %d", code)
	}
	if got := order(); len(got) != 2 || got[0] != "Music" || got[1] != "Films" {
		t.Fatalf("the order everyone sees: %v", got)
	}
	for _, bad := range [][]string{{ids[0]}, {ids[0], ids[0]}, {ids[0], "lib_nowhere"}, nil} {
		if code := status(t, s, "PUT", "/v1/admin/libraries/order", owner, LibraryOrder{IDs: bad}); code != 400 {
			t.Errorf("the order %v answered %d; want 400", bad, code)
		}
	}
	// The explorer says which folders are a library's already and which cannot be chosen:
	// the server's own data folder, what is inside it, and what holds it.
	_, again := explore("path=" + url.QueryEscape(root))
	marks := map[string]FolderChoice{}
	for _, f := range again.Folders {
		marks[f.Name] = f
	}
	if marks["films"].Library != "Films" || marks["Music"].Library != "Music" || marks["media"].Library != "" || marks["media"].CannotBeChosen != "" {
		t.Fatalf("folders that are libraries: %+v", again.Folders)
	}
	_, above := explore("path=" + url.QueryEscape(filepath.Dir(s.dataDir)))
	found := false
	for _, f := range above.Folders {
		if f.Path == s.dataDir {
			found = f.CannotBeChosen != ""
		}
	}
	if _, top := explore(""); !found || top.Folders[0].CannotBeChosen == "" {
		t.Fatalf("the data folder and the whole disk should say they cannot be chosen: %+v, %+v", above.Folders, top.Folders)
	}

	member := memberToken(t, s, owner)
	if code := status(t, s, "GET", "/v1/admin/filesystem", member, nil); code != 403 {
		t.Errorf("a member exploring folders answered %d", code)
	}
	if code := status(t, s, "PUT", "/v1/admin/libraries/order", member, LibraryOrder{IDs: ids}); code != 403 {
		t.Errorf("a member ordering libraries answered %d", code)
	}
}
