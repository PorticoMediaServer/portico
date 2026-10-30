//go:build linux || darwin

package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A local observation's change token is content identity: the object's inode,
// size and modification time. A permission write moves the file's ctime and
// must not move the token, or a current publication would be refused and
// republished for a change no reader can see. A real edit moves the token.
// The corpus listing and the observed opener must agree on the same token.
func TestLocalChangeTokenTracksContentNotPermissionMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publication.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7 first edition"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := nativeObjectBinding(info)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("a local observation has no change token")
	}
	if listed := snapshot(path, info); listed.ChangeToken != first {
		t.Fatalf("listing token %q differs from opener token %q", listed.ChangeToken, first)
	}

	if err = os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, after, err := nativeObjectBinding(info); err != nil {
		t.Fatal(err)
	} else if after != first {
		t.Fatalf("permission-only change moved the token: %q -> %q", first, after)
	}

	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	when := info.ModTime().Add(2 * time.Second)
	if err = os.WriteFile(path, []byte("%PDF-1.7 second edition, more pages"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, edited, err := nativeObjectBinding(info); err != nil {
		t.Fatal(err)
	} else if edited == first {
		t.Fatal("a real edit kept the change token")
	}
}
