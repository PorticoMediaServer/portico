//go:build linux || darwin

package scanbench

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFixtureTimestampUsesOpenFile(t *testing.T) {
	scratch := t.TempDir()
	root := filepath.Join(scratch, "library")
	anchor, err := createFixtureRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer anchor.Close()
	file, err := anchor.create("media.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	outside := filepath.Join(scratch, "outside.wav")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "media.wav"), filepath.Join(root, "retained.wav")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "media.wav")); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(946684800, 0)
	if err := stampFixtureFile(file, stamp); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || !retained.ModTime().Equal(stamp) {
		t.Fatal("timestamp followed replacement path")
	}
}
