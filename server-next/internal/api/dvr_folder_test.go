package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheOwnerReadsTheEffectiveRecordingFolderAndChangesItWithoutRestart(t *testing.T) {
	s, owner, members := seededWith(t, 20, 1)
	path := "/v1/admin/dvr/folder"
	first := get[DvrFolderStatus](t, s, owner, path)
	if first.Folder == "" || first.Folder != s.dvrOptions().Folder {
		t.Fatalf("the effective default: %+v", first)
	}
	target := filepath.Join(t.TempDir(), "captures")
	if code := status(t, s, "PUT", "/v1/admin/settings/dvr", owner, map[string]string{"folder": target}); code != 200 {
		t.Fatalf("choosing a folder: %d", code)
	}
	if got := get[DvrFolderStatus](t, s, owner, path); got.Folder != target || got.State != "not_made" {
		t.Fatalf("a destination applies to later recordings without restart: %+v", got)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if got := get[DvrFolderStatus](t, s, owner, path); got.State != "ready" {
		t.Fatalf("the made folder: %+v", got)
	}
	for _, token := range []string{"", members[0]} {
		answer := call(t, s, "GET", path, token, nil, nil)
		if answer.Code != 401 && answer.Code != 403 || strings.Contains(answer.Body.String(), target) {
			t.Fatalf("a non-owner read: %d %s", answer.Code, answer.Body)
		}
	}
}
