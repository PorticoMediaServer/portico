package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestScanRequestNamesOneFolderOfThisLibrary(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	lib, err := s.db.CreateLibrary(context.Background(), "Films", db.LibraryMovies, "en", []string{t.TempDir(), t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.db.CreateLibrary(context.Background(), "Other", db.LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var answer, foreign AdminLibrary
	base := "/v1/admin/libraries/" + lib.PublicID
	call(t, s, "GET", base, owner, nil, &answer)
	call(t, s, "GET", "/v1/admin/libraries/"+other.PublicID, owner, nil, &foreign)
	if len(answer.Folders) != 2 || answer.Folders[0].ID == "" || answer.Folders[0].ID == answer.Folders[1].ID || len(foreign.Folders) != 1 {
		t.Fatalf("folders have no distinct selectors: %+v, %+v", answer.Folders, foreign.Folders)
	}
	for _, id := range []string{"not-a-folder", foreign.Folders[0].ID} {
		if code := status(t, s, "POST", base+"/scan", owner, ScanRequest{Folder: id}); code != 400 {
			t.Fatalf("a folder outside this library answered %d", code)
		}
	}
	var job Job
	if w := call(t, s, "POST", base+"/scan", owner, ScanRequest{Folder: answer.Folders[0].ID, AllFiles: true}, &job); w.Code != 202 {
		t.Fatalf("scan one folder: %d %s", w.Code, w.Body)
	}
	if ended := waitForJob(t, s, owner, job.ID); ended.State != "done" {
		t.Fatalf("scan one folder ended %+v", ended)
	}
	call(t, s, "GET", base, owner, nil, &answer)
	if answer.Folders[0].LastScanAt == nil || answer.Folders[1].LastScanAt != nil {
		t.Fatalf("the scan did not stay in its chosen folder: %+v", answer.Folders)
	}
}
