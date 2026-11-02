//go:build linux || darwin

package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Scanner M1: the stat, list and read operations a scan makes on a local root
// run in this process. Every refusal the helper process enforced is enforced
// the same way here; each case below asserts the in-process path served it.
func TestInProcessKeepsEveryContainmentRefusal(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(binary)
	base := t.TempDir()
	root, outside := filepath.Join(base, "library"), filepath.Join(base, "outside")
	for _, dir := range []string{filepath.Join(root, "Film (2000)"), outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string) {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "Film (2000)", "Film.mkv"))
	write(filepath.Join(outside, "secret.mkv"))
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "Film (2000)"), filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	observed, err := LocalInventoryDirectory(InventoryRequest{Root: root, RelativePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	identity := RootIdentity(observed)
	ctx := context.Background()
	stat := func(relative string, follow bool) (Snapshot, error) {
		before := c.Supervisor.Stats().InProcess
		v, err := c.InventoryStat(ctx, "scan-test", InventoryRequest{Root: root, RootIdentity: identity, RelativePath: relative, FollowSymlinks: follow})
		if c.Supervisor.Stats().InProcess != before+1 {
			t.Fatalf("%s was not served in process", relative)
		}
		return v, err
	}
	if v, err := stat("Film (2000)/Film.mkv", false); err != nil || v.Size != 1 {
		t.Fatalf("an ordinary file inside the root: %+v %v", v, err)
	}
	for _, c := range []struct {
		name, relative string
		follow         bool
	}{
		{"a symlink out of the root, not followed", "escape/secret.mkv", false},
		{"a symlink out of the root, followed", "escape/secret.mkv", true},
		{"a symlink inside the root when links are disabled", "inside/Film.mkv", false},
		{"a parent reference", "../outside/secret.mkv", false},
		{"a parent reference behind a folder", "Film (2000)/../../outside/secret.mkv", true},
	} {
		if v, err := stat(c.relative, c.follow); err == nil {
			t.Fatalf("%s was admitted: %+v", c.name, v)
		}
	}
	if v, err := stat("inside/Film.mkv", true); err != nil || v.Size != 1 {
		t.Fatalf("a followed symlink that stays inside the root: %+v %v", v, err)
	}
	// The root swapped mid-scan: the same path now names a different folder,
	// so the recorded root identity no longer matches and nothing is read.
	if err := os.Rename(root, filepath.Join(base, "library-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Film (2000)"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "Film (2000)", "Film.mkv"))
	if v, err := stat("Film (2000)/Film.mkv", false); err == nil {
		t.Fatalf("a file under a swapped root was read: %+v", v)
	}
	before := c.Supervisor.Stats().InProcess
	if _, err := c.InventoryDirectory(ctx, "scan-test", InventoryRequest{Root: root, RootIdentity: identity, RelativePath: "."}); err == nil || c.Supervisor.Stats().InProcess != before+1 {
		t.Fatalf("a swapped root's directory was listed (or not in process): %v", err)
	}
}

// A root the mount table can't place on a local filesystem keeps its helper
// process, where a hung syscall can be killed.
func TestNonLocalRootsKeepTheHelperProcess(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	previous := localFilesystem
	localFilesystem = func(string) bool { return false }
	t.Cleanup(func() { localFilesystem = previous })
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(binary)
	if v, err := c.InventoryStat(context.Background(), "scan-test", InventoryRequest{Root: root, RelativePath: "a.mkv"}); err != nil || v.Size != 1 {
		t.Fatalf("helper stat: %+v %v", v, err)
	}
	if stats := c.Supervisor.Stats(); stats.InProcess != 0 || stats.Admitted != 1 {
		t.Fatal("a non-local root was served in process")
	}
}
