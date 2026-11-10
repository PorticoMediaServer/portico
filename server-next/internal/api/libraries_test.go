package api

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitForJob reads a job until it has ended and returns how it ended.
func waitForJob(t *testing.T, s *Server, token, id string) Job {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var job Job
		if w := call(t, s, "GET", "/v1/jobs/"+id, token, nil, &job); w.Code != 200 {
			t.Fatalf("reading the job answered %d: %s", w.Code, w.Body)
		}
		if job.State != "waiting" && job.State != "running" {
			return job
		}
	}
	t.Fatal("the job did not end")
	return Job{}
}

func scanNow(t *testing.T, s *Server, token, library string) {
	t.Helper()
	var job Job
	if w := call(t, s, "POST", "/v1/admin/libraries/"+library+"/scan", token, nil, &job); w.Code != 202 {
		t.Fatalf("Scan now answered %d: %s", w.Code, w.Body)
	}
	if done := waitForJob(t, s, token, job.ID); done.State != "done" {
		t.Fatalf("the scan ended %s: %s", done.State, done.Error)
	}
}

// The owner makes a library over real folders, it is scanned, and the owner sees what was
// skipped and what is missing, empties the trash, deletes a title's files, takes a folder
// out and removes the library. Nobody else may do any of it.
func TestTheOwnerManagesLibrariesScansAndDeletes(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	root := filepath.Join(t.TempDir(), "films")
	writeFile(t, filepath.Join(root, "Heat (1995)", "Heat (1995).mkv"), 101)
	writeFile(t, filepath.Join(root, "Alien (1979).mkv"), 102)
	writeFile(t, filepath.Join(root, "Bad \xff name.mkv"), 103)
	extra := filepath.Join(t.TempDir(), "more")
	writeFile(t, filepath.Join(extra, "Ronin (1998).mkv"), 104)

	// What is refused: a folder that is not there, a relative path, a kind nobody knows.
	for _, bad := range []LibraryCreate{{Name: "Films", Kind: "movies", Folders: []string{root + "/nowhere"}}, {Name: "Films", Kind: "movies"},
		{Name: "Films", Kind: "movies", Folders: []string{"films"}}, {Name: "Films", Kind: "films", Folders: []string{root}},
		{Name: "Films", Kind: "movies", Folders: []string{"/proc/1"}}} {
		if code := status(t, s, "POST", "/v1/admin/libraries", owner, bad); code != 400 {
			t.Errorf("%+v answered %d; want 400", bad, code)
		}
	}
	var lib AdminLibrary
	create := LibraryCreate{Name: "Films", Kind: "movies", Folders: []string{root}}
	r := newRequest("POST", "/v1/admin/libraries", owner, create)
	r.Header.Set("Idempotency-Key", "one")
	if w := serve(s, r); w.Code != 200 || jsonInto(w, &lib) != nil || !lib.Scanning && lib.LastScanFinishedAt == nil {
		t.Fatalf("creating the library answered %d: %s", w.Code, w.Body)
	}
	// The same request again is the same library, not a second one.
	var again AdminLibrary
	r = newRequest("POST", "/v1/admin/libraries", owner, create)
	r.Header.Set("Idempotency-Key", "one")
	if w := serve(s, r); w.Code != 200 || jsonInto(w, &again) != nil || again.ID != lib.ID {
		t.Fatalf("repeating the create answered %d: %s", w.Code, w.Body)
	}
	if lib.Options.Depth != "basic" || lib.Options.Trash.Mode != "days" || lib.Options.Trash.Days != 30 || lib.Options.AllowDelete {
		t.Errorf("a new library's options: %+v", lib.Options)
	}
	scanNow(t, s, owner, lib.ID) // joins the first scan or runs after it
	base := "/v1/admin/libraries/" + lib.ID

	var scan ScanStatus
	if w := call(t, s, "GET", base+"/scan", owner, nil, &scan); w.Code != 200 || scan.State != "idle" || scan.LastScanFinishedAt == nil ||
		len(scan.Folders) != 1 || !scan.Folders[0].Available {
		t.Fatalf("scan status: %d %s", w.Code, w.Body)
	}
	var skipped Page[SkippedFile]
	if w := call(t, s, "GET", base+"/skipped", owner, nil, &skipped); w.Code != 200 || len(skipped.Items) != 1 || skipped.Items[0].Reason != "unreadable_name" {
		t.Fatalf("files skipped: %d %s", w.Code, w.Body)
	}

	// A file that is gone makes its title missing; Empty trash wants the count typed.
	os.Remove(filepath.Join(root, "Alien (1979).mkv"))
	scanNow(t, s, owner, lib.ID)
	var missing MissingTitles
	if w := call(t, s, "GET", base+"/missing", owner, nil, &missing); w.Code != 200 || missing.Total != 1 || missing.Items[0].Title != "Alien" ||
		missing.Items[0].RemovedAt == nil {
		t.Fatalf("missing titles: %d %s", w.Code, w.Body)
	}
	if code := status(t, s, "DELETE", base+"/missing", owner, nil); code != 400 {
		t.Errorf("Empty trash without the count answered %d", code)
	}
	var emptied TrashEmptied
	if w := call(t, s, "DELETE", base+"/missing?confirm=1", owner, nil, &emptied); w.Code != 200 || emptied.Removed != 1 {
		t.Fatalf("Empty trash: %d %s", w.Code, w.Body)
	}

	// Deleting is off until the owner allows it, and wants the title typed.
	heat := missingItemID(t, s, owner, lib.ID, "Heat")
	item := "/v1/admin/items/" + heat
	var preview DeletionPreview
	if w := call(t, s, "GET", item+"/deletion", owner, nil, &preview); w.Code != 200 || preview.Titles != 1 || preview.Files != 1 || preview.Bytes != 101 {
		t.Fatalf("delete preview: %d %s", w.Code, w.Body)
	}
	if code := status(t, s, "DELETE", item+"?confirm=Heat", owner, nil); code != 403 {
		t.Errorf("deleting while it is off answered %d", code)
	}
	allow := LibraryEdit{Options: &LibraryOptions{Depth: "basic", Trash: TrashRule{Mode: "manual"}, AllowDelete: true}, Folders: &[]string{root, extra}}
	if w := call(t, s, "PATCH", base, owner, allow, &lib); w.Code != 200 || !lib.Options.AllowDelete || len(lib.Folders) != 2 {
		t.Fatalf("editing the library: %d %s", w.Code, w.Body)
	}
	if code := status(t, s, "DELETE", item, owner, nil); code != 400 {
		t.Errorf("deleting without the title typed answered %d", code)
	}
	var result DeletionResult
	if w := call(t, s, "DELETE", item+"?confirm=heat", owner, nil, &result); w.Code != 200 || !result.Removed || result.FilesDeleted != 1 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")); err == nil {
		t.Error("the deleted title's file is still on disk")
	}
	if w := call(t, s, "GET", base+"/missing", owner, nil, &missing); missing.Total != 0 {
		t.Errorf("a deleted title went to the trash: %s", w.Body)
	}

	// The added folder was scanned; taking it out removes its title and leaves its file.
	scanNow(t, s, owner, lib.ID)
	missingItemID(t, s, owner, lib.ID, "Ronin")
	only := LibraryEdit{Folders: &[]string{root}}
	if w := call(t, s, "PATCH", base, owner, only, &lib); w.Code != 200 || len(lib.Folders) != 1 {
		t.Fatalf("removing a folder: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(extra, "Ronin (1998).mkv")); err != nil {
		t.Error("removing a folder from the library touched its files")
	}

	// A member is refused everything here.
	member := memberToken(t, s, owner)
	for _, route := range [][2]string{{"GET", "/v1/admin/libraries"}, {"POST", base + "/scan"}, {"DELETE", base + "?confirm=Films"}, {"GET", base + "/skipped"}} {
		if code := status(t, s, route[0], route[1], member, nil); code != 403 {
			t.Errorf("%s %s answered a member %d; want 403", route[0], route[1], code)
		}
	}

	// Removing the library wants its name typed, and is a job.
	if code := status(t, s, "DELETE", base+"?confirm=Movies", owner, nil); code != 400 {
		t.Errorf("removing the library with the wrong name answered %d", code)
	}
	var job Job
	if w := call(t, s, "DELETE", base+"?confirm="+url.QueryEscape("Films"), owner, nil, &job); w.Code != 202 {
		t.Fatalf("removing the library: %d %s", w.Code, w.Body)
	}
	if done := waitForJob(t, s, owner, job.ID); done.State != "done" {
		t.Fatalf("removing the library ended %s: %s", done.State, done.Error)
	}
	var list Page[AdminLibrary]
	if w := call(t, s, "GET", "/v1/admin/libraries", owner, nil, &list); w.Code != 200 || len(list.Items) != 0 {
		t.Fatalf("libraries after the removal: %s", w.Body)
	}
}

// missingItemID finds a film of a library by title through the browse route, and fails the
// test if it is not there.
func missingItemID(t *testing.T, s *Server, token, library, title string) string {
	t.Helper()
	var page struct {
		Items []struct{ ID, Title string }
	}
	if w := call(t, s, "GET", "/v1/items?kind=movie&library="+library, token, nil, &page); w.Code != 200 {
		t.Fatalf("listing the library answered %d: %s", w.Code, w.Body)
	}
	for _, item := range page.Items {
		if item.Title == title {
			return item.ID
		}
	}
	t.Fatalf("no title %q in the library: %+v", title, page.Items)
	return ""
}

// memberToken makes a member and returns a session of theirs on their main profile.
func memberToken(t *testing.T, s *Server, owner string) string {
	t.Helper()
	addMember(t, s, owner, "sam", "member")
	return signInAs(t, s, "sam", testPassword, 7).AccessToken
}

// Deleting one of two episodes held in one file says the other goes too, and takes both.
// With the library's disk not connected nothing is deleted and the title stays. The whole
// disk and the server's own folder are refused as library folders.
func TestDeleteNamesWhatSharesAFileAndRefusesAMissingDisk(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	for _, folder := range []string{"/", s.dataDir, filepath.Dir(s.dataDir)} {
		if code := status(t, s, "POST", "/v1/admin/libraries", owner, LibraryCreate{Name: "Shows", Kind: "shows", Folders: []string{folder}}); code != 400 {
			t.Errorf("a library at %s answered %d; want 400", folder, code)
		}
	}
	root := filepath.Join(t.TempDir(), "shows")
	double := filepath.Join(root, "Lost", "Season 1", "Lost - S01E01-E02.mkv")
	writeFile(t, double, 1)
	var lib AdminLibrary
	create := LibraryCreate{Name: "Shows", Kind: "shows", Folders: []string{root}, Options: &LibraryOptions{Depth: "basic", Trash: TrashRule{Mode: "manual"}, AllowDelete: true}}
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, create, &lib); w.Code != 200 {
		t.Fatalf("creating the library answered %d: %s", w.Code, w.Body)
	}
	scanNow(t, s, owner, lib.ID)
	episodes := func() []struct{ ID, Title string } {
		var page struct {
			Items []struct{ ID, Title string }
		}
		if w := call(t, s, "GET", "/v1/items?kind=episode&library="+lib.ID, owner, nil, &page); w.Code != 200 {
			t.Fatalf("listing episodes answered %d: %s", w.Code, w.Body)
		}
		return page.Items
	}
	both := episodes()
	if len(both) != 2 {
		t.Fatalf("episodes: %+v; want the two the file holds", both)
	}
	item := "/v1/admin/items/" + both[0].ID
	var preview DeletionPreview
	if w := call(t, s, "GET", item+"/deletion", owner, nil, &preview); w.Code != 200 || preview.Titles != 2 || preview.Files != 1 ||
		len(preview.AlsoRemoves) != 1 || preview.AlsoRemoves[0].ID != both[1].ID {
		t.Fatalf("delete preview: %d %s", w.Code, w.Body)
	}

	// The disk goes away: its folder is there and empty.
	away := root + "-away"
	if err := os.Rename(root, away); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	confirm := "?confirm=" + url.QueryEscape(preview.Title)
	if w := call(t, s, "DELETE", item+confirm, owner, nil, nil); w.Code != 409 || len(episodes()) != 2 {
		t.Fatalf("deleting with the disk away answered %d: %s", w.Code, w.Body)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(away, root); err != nil {
		t.Fatal(err)
	}

	var result DeletionResult
	if w := call(t, s, "DELETE", item+confirm, owner, nil, &result); w.Code != 200 || !result.Removed || result.FilesDeleted != 1 ||
		len(result.AlsoRemoved) != 1 || result.AlsoRemoved[0].ID != both[1].ID {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(double); err == nil {
		t.Error("the file is still on disk")
	}
	if left := episodes(); len(left) != 0 {
		t.Errorf("episodes left after the delete: %+v", left)
	}
}
