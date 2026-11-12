package api

import (
	"os"
	"path/filepath"
	"testing"
)

// Settings › Troubleshooting lists every file a scan could not use, of every library, in one
// list paged by cursor, each with its library and why; a file that is gone or now fine leaves
// the list, and administrators see only their own libraries.
func TestTheOwnerSeesEveryLibrarysSkippedFilesInOneList(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	films, shows := filepath.Join(t.TempDir(), "films"), filepath.Join(t.TempDir(), "shows")
	writeFile(t, filepath.Join(films, "Heat (1995).mkv"), 101)
	writeFile(t, filepath.Join(films, "Bad \xff one.mkv"), 102)
	writeFile(t, filepath.Join(films, "Bad \xfe two.mkv"), 103)
	writeFile(t, filepath.Join(shows, "Ronin (1998).mkv"), 104)
	writeFile(t, filepath.Join(shows, "Bad \xfd three.mkv"), 105)
	var made []AdminLibrary
	for _, c := range []LibraryCreate{{Name: "Films", Kind: "movies", Folders: []string{films}}, {Name: "Shows", Kind: "movies", Folders: []string{shows}}} {
		var lib AdminLibrary
		if w := call(t, s, "POST", "/v1/admin/libraries", owner, c, &lib); w.Code != 200 {
			t.Fatalf("creating %s answered %d: %s", c.Name, w.Code, w.Body)
		}
		scanNow(t, s, owner, lib.ID)
		made = append(made, lib)
	}

	serverSkippedPage = 2
	t.Cleanup(func() { serverSkippedPage = skippedPage })
	read := func(cursor string) Page[ServerSkippedFile] {
		t.Helper()
		var page Page[ServerSkippedFile]
		if w := call(t, s, "GET", "/v1/admin/skipped?cursor="+cursor, owner, nil, &page); w.Code != 200 {
			t.Fatalf("the list answered %d: %s", w.Code, w.Body)
		}
		return page
	}
	first := read("")
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("the first page: %+v", first)
	}
	second := read(first.NextCursor)
	if len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("the second page: %+v", second)
	}
	in := map[string]int{}
	for _, f := range append(first.Items, second.Items...) {
		if f.Reason != "unreadable_name" || f.Message == "" || f.LibraryName == "" {
			t.Errorf("a skipped file: %+v", f)
		}
		in[f.LibraryID]++
	}
	if in[made[0].ID] != 2 || in[made[1].ID] != 1 {
		t.Fatalf("by library: %v", in)
	}

	admin := addMember(t, s, owner, "ada", "admin")
	limited := signInAs(t, s, "ada", testPassword, 1)
	var all Page[ServerSkippedFile]
	if w := call(t, s, "GET", "/v1/admin/skipped", limited.AccessToken, nil, &all); w.Code != 200 || len(all.Items) != 2 || all.NextCursor == "" {
		t.Fatalf("an administrator's first page answered %d: %s", w.Code, w.Body)
	}
	onlyShows, no := []string{made[1].ID}, false
	if code := status(t, s, "PATCH", "/v1/admin/members/"+admin.ID, owner, MemberEdit{AllLibraries: &no, Libraries: &onlyShows}); code != 200 {
		t.Fatalf("restricting libraries answered %d", code)
	}
	limited = signInAs(t, s, "ada", testPassword, 2)
	all = Page[ServerSkippedFile]{}
	if w := call(t, s, "GET", "/v1/admin/skipped", limited.AccessToken, nil, &all); w.Code != 200 || len(all.Items) != 1 || all.Items[0].LibraryID != made[1].ID || all.NextCursor != "" {
		t.Fatalf("a restricted administrator's list answered %d: %s", w.Code, w.Body)
	}

	// A file that is gone leaves the list at the next scan. (A folder left empty would be
	// taken for a drive that is not there, and nothing in it forgotten.)
	os.Remove(filepath.Join(shows, "Bad \xfd three.mkv"))
	scanNow(t, s, owner, made[1].ID)
	if all := read(""); len(all.Items) != 2 || all.NextCursor != "" || all.Items[0].LibraryID != made[0].ID {
		t.Fatalf("after the shows' file went: %+v", all)
	}

	if code := status(t, s, "GET", "/v1/admin/skipped?cursor=nonsense", owner, nil); code != 400 {
		t.Errorf("a cursor that is not one answered %d", code)
	}
	for _, token := range []string{"", memberToken(t, s, owner)} {
		if code := status(t, s, "GET", "/v1/admin/skipped", token, nil); code != 401 && code != 403 {
			t.Errorf("someone other than the owner was answered %d", code)
		}
	}
}
