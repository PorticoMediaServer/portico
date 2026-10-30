//go:build linux || darwin

package playback

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestHLSOutputRefusesFIFOAndInRootSymlink(t *testing.T) {
	path := t.TempDir()
	dir := filepath.Join(path, "session")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "segment-000000.ts"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// An in-root target distinguishes O_NOFOLLOW from root escape protection.
	target := filepath.Join(dir, "private")
	if err := os.WriteFile(target, []byte("private artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "master.m3u8")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"segment-000000.ts", "master.m3u8"} {
		if opened, err := openHLSOutput(root, "session", name); err == nil {
			opened.Close()
			t.Fatalf("FIFO or symlink artifact %s was accepted", name)
		}
	}
}
