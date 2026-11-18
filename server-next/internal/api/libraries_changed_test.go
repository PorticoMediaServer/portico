package api

import (
	"path/filepath"
	"testing"
)

// A tool allowed Libraries says which files changed: the library folder holding them is scanned,
// a path in no library scans nothing, and a key that may only read is refused. (Definition 13 rule 19.)
func TestChangedPathsScanTheFoldersThatHoldThem(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	var radarr, reader KeyCreated
	call(t, s, "POST", "/v1/admin/api-keys", owner, KeyCreate{Name: "Radarr", Scopes: []string{"library"}}, &radarr)
	call(t, s, "POST", "/v1/admin/api-keys", owner, KeyCreate{Name: "Reader", Scopes: []string{"read"}}, &reader)
	movies := th.libs["movies"]
	report := ChangedPaths{Paths: []string{filepath.Join(movies.Paths[0].Path, "New Film (2024)", "New Film (2024).mkv"), "/nowhere/else.mkv"}}
	if code := status(t, s, "POST", "/v1/libraries/changed", reader.Secret, report); code != 403 {
		t.Errorf("a read-only key reported changed files: %d", code)
	}
	var out ChangedScans
	if w := call(t, s, "POST", "/v1/libraries/changed", radarr.Secret, report, &out); w.Code != 200 || out.Folders != 1 {
		t.Fatalf("the report answered %d %+v, want one folder scanned", w.Code, out)
	}
	var st ScanStatus
	if w := call(t, s, "GET", "/v1/admin/libraries/"+movies.PublicID+"/scan", owner, nil, &st); w.Code != 200 || st.Job == nil {
		t.Errorf("after a changed film the films library has no scan: %d %+v", w.Code, st)
	}
	if code := status(t, s, "POST", "/v1/libraries/changed", radarr.Secret, ChangedPaths{}); code != 400 {
		t.Errorf("an empty report answered %d, want 400", code)
	}
}
