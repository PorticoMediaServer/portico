package playback

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestHLSOutputRejectsLinksAndNonRegularFiles(t *testing.T) {
	path := t.TempDir()
	dir := filepath.Join(path, "session")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	target := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(target, []byte("private artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"master.m3u8", "video.m3u8", "segment-000000.ts", "init.mp4", "audio-0-000000.m4s"} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(dir, name)
			if err := os.Symlink(target, file); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			defer os.Remove(file)
			if opened, err := openHLSOutput(root, "session", name); err == nil {
				opened.Close()
				t.Fatal("artifact symlink was opened")
			}
		})
	}
	if err := os.Mkdir(filepath.Join(dir, "segment-000001.ts"), 0700); err != nil {
		t.Fatal(err)
	}
	if opened, err := openHLSOutput(root, "session", "segment-000001.ts"); err == nil {
		opened.Close()
		t.Fatal("directory was opened as a segment")
	}
}

func TestHLSOutputRetainsFileAndRootAcrossReplacement(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "output")
	dir := filepath.Join(path, "session")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "segment-000000.ts")
	if err := os.WriteFile(file, []byte("admitted media"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	opened, err := openHLSOutput(root, "session", "segment-000000.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := os.Rename(file, file+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("replacement media"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(opened)
	if err != nil || string(raw) != "admitted media" {
		t.Fatalf("open response followed replacement: %q %v", raw, err)
	}
	if err := os.Rename(path, filepath.Join(parent, "retained")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("different output tree"), 0600); err != nil {
		t.Fatal(err)
	}
	retained, err := openHLSOutput(root, "session", "segment-000000.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	raw, err = io.ReadAll(retained)
	if err != nil || string(raw) != "replacement media" {
		t.Fatalf("root pathname replacement was adopted: %q %v", raw, err)
	}
}

func TestHLSOutputRejectsRootEscape(t *testing.T) {
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "master.m3u8"), []byte("private manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"", ".", "..", "../outside", outside} {
		if file, err := openHLSOutput(root, session, "master.m3u8"); err == nil {
			file.Close()
			t.Fatalf("escaping session %q was opened", session)
		}
	}
	if err := os.Symlink(outside, filepath.Join(path, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if file, err := openHLSOutput(root, "escape", "master.m3u8"); err == nil {
		file.Close()
		t.Fatal("intermediate symlink escaped output root")
	}
}
