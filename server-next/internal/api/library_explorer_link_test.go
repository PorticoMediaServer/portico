package api

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// The explorer never opens /proc, /sys, /dev or /run, also when the path given reaches one
// through a link (as /var/run reaches /run on most Linux systems).
func TestTheExplorerNeverOpensAPlaceNotForFilesThroughALink(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	root := t.TempDir()
	for _, target := range []string{"/proc", "/sys", "/dev", "/run"} {
		if _, err := os.Stat(target); err != nil {
			continue
		}
		link := filepath.Join(root, filepath.Base(target)+"-link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		var out FolderList
		for _, path := range []string{link, filepath.Join(link, "self")} {
			w := call(t, s, "GET", "/v1/admin/filesystem?path="+url.QueryEscape(path), owner, nil, &out)
			if w.Code == 200 {
				t.Errorf("%s (a link to %s) was opened and listed %d folders", path, target, len(out.Folders))
			}
		}
	}
}

// A library cannot be saved at a place that is not for files, also when the folder given
// reaches one through a link.
func TestALibraryCannotBeSavedThroughALinkToAPlaceNotForFiles(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	root := t.TempDir()
	link := filepath.Join(root, "proc-link")
	if err := os.Symlink("/proc", link); err != nil {
		t.Fatal(err)
	}
	folders := []string{link, filepath.Join(link, "self")}
	if target, err := filepath.EvalSymlinks("/var/run"); err == nil && target == "/run" {
		folders = append(folders, "/var/run")
	}
	for _, folder := range folders {
		if code := status(t, s, "POST", "/v1/admin/libraries", owner, LibraryCreate{Name: "Films", Kind: "movies", Folders: []string{folder}}); code != 400 {
			t.Errorf("a library at %s answered %d; want 400", folder, code)
		}
	}
}
