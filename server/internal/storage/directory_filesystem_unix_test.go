//go:build darwin || linux

package storage

import (
	"os"
	"syscall"
	"testing"
)

func TestDirectoryIdentityUsesInheritedHandle(t *testing.T) {
	path := t.TempDir()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	wanted, _, err := observedObjectBinding(info, path)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	inherited := os.NewFile(uintptr(fd), "registered-source-root")
	defer inherited.Close()
	got, _, err := observedFileBinding(inherited)
	if err != nil || got != wanted {
		t.Fatalf("inherited directory identity %q %v, want %q", got, err, wanted)
	}
}
