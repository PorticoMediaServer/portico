//go:build windows

package dirsync

import "testing"

func TestDirectorySyncUsesWindowsPublicationPolicy(t *testing.T) {
	if err := Sync(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
