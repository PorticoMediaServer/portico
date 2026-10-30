//go:build windows

package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalObjectBindingSurvivesRenameAndDetectsReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "media.mkv")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	binding := func(path string) Snapshot {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		s := snapshot(path, info)
		if s.ObjectIdentity == "" || s.ChangeToken == "" {
			t.Fatalf("missing binding: %+v", s)
		}
		return s
	}
	first := binding(path)
	renamed := filepath.Join(dir, "renamed.mkv")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	if got := binding(renamed); got.ObjectIdentity != first.ObjectIdentity {
		t.Fatal("rename changed object identity")
	}
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := binding(path); got.ObjectIdentity == first.ObjectIdentity {
		t.Fatal("replacement reused object identity")
	}
	if got := binding(dir); !got.Directory {
		t.Fatal("directory binding lost type")
	}
}

func TestLocalObjectBindingRejectsStaleAttributes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "media.mkv")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed size"), 0600); err != nil {
		t.Fatal(err)
	}
	if object, _, err := observedObjectBinding(info, path); err == nil || object != "" {
		t.Fatal("stale attributes accepted")
	}
}

// R-05: toggling the read-only attribute moves FILE_BASIC_INFO.ChangeTime but
// no byte. The change token must hold; a real write must move it.
func TestLocalChangeTokenIgnoresAttributeWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publication.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7 first edition"), 0600); err != nil {
		t.Fatal(err)
	}
	token := func() string {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		_, value, err := observedObjectBinding(info, path)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := token()
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if got := token(); got != first {
		t.Fatalf("an attribute write moved the change token: %q -> %q", first, got)
	}
	if err := os.WriteFile(path, []byte("%PDF-1.7 second edition, longer"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := token(); got == first {
		t.Fatal("a content write kept the change token")
	}
}
