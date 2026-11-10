package decoder

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A helper that was killed leaves its folder behind. The next helper removes
// it, and never one whose helper is still listening or has only just started.
func TestDeadHelperFoldersAreRemoved(t *testing.T) {
	tmp := t.TempDir()
	old := time.Now().Add(-time.Hour)
	dead := filepath.Join(tmp, helperFolderPrefix+"dead")
	live := filepath.Join(tmp, helperFolderPrefix+"live")
	fresh := filepath.Join(tmp, helperFolderPrefix+"fresh")
	other := filepath.Join(tmp, "something-else")
	for _, folder := range []string{dead, live, fresh, other} {
		if err := os.Mkdir(folder, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// A socket file nobody listens on, as a killed helper leaves it.
	abandoned, err := net.Listen("unix", filepath.Join(dead, "input.sock"))
	if err != nil {
		t.Skipf("no unix sockets here: %v", err)
	}
	abandoned.(*net.UnixListener).SetUnlinkOnClose(false)
	abandoned.Close()
	listening, err := net.Listen("unix", filepath.Join(live, "input.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listening.Close()
	for _, folder := range []string{dead, live, other} {
		if err := os.Chtimes(folder, old, old); err != nil {
			t.Fatal(err)
		}
	}
	removeDeadHelperFolders(tmp)
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatal("a dead helper's folder was kept")
	}
	for _, folder := range []string{live, fresh, other} {
		if _, err := os.Stat(folder); err != nil {
			t.Fatalf("%s was removed: %v", filepath.Base(folder), err)
		}
	}
}
