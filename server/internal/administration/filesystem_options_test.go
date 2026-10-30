package administration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemFlagsReasonsAndCursorScope(t *testing.T) {
	s, _, _ := newService(t)
	root := t.TempDir()
	s.PickerRoots = []string{root}
	for _, name := range []string{"Alpha", "alpha", "Beta", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "movie.mkv"), []byte("not read"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "absent"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/proc", filepath.Join(root, "system-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(root)
	page, err := s.Browse(context.Background(), root, "", 200, false, BrowseOptions{ShowHidden: true, CheckWritable: true})
	if err != nil {
		t.Fatal(err)
	}
	if !page.WritableChecked || !page.Writable || page.RunsAs == "" {
		t.Fatalf("permission check: %+v", page)
	}
	seen := map[string]FilesystemEntry{}
	for _, e := range page.Entries {
		seen[e.Name] = e
	}
	if !seen[".hidden"].Hidden || seen["broken"].Reason != "broken-link" || seen["broken"].Readable {
		t.Fatalf("flags: %+v", page.Entries)
	}
	for _, name := range []string{"system-link", "outside", "movie.mkv"} {
		if _, ok := seen[name]; ok {
			t.Fatalf("unsafe/unrequested entry %s", name)
		}
	}
	after, _ := os.Stat(root)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("browse/permission check wrote into folder")
	}
	first, err := s.Browse(context.Background(), root, "", 1, false, BrowseOptions{Prefix: "A"})
	if err != nil || len(first.Entries) != 1 || first.Entries[0].Name != "Alpha" {
		t.Fatalf("prefix %+v %v", first, err)
	}
	all, _ := s.Browse(context.Background(), root, "", 1, false)
	if _, err = s.Browse(context.Background(), root, all.NextCursor, 1, false, BrowseOptions{ShowHidden: true}); !errors.Is(err, ErrCursor) {
		t.Fatalf("cross-filter cursor accepted: %v", err)
	}
	missing, err := s.Browse(context.Background(), filepath.Join(root, "ALPHA"), "", 200, false)
	if !errors.Is(err, ErrNotFound) || missing.Reason != "missing" || missing.Path != filepath.Join(root, "ALPHA") || len(missing.Suggestions) != 2 {
		t.Fatalf("case suggestions %+v %v", missing, err)
	}
	raw, _ := json.Marshal(page)
	for _, key := range []string{"summary", "count", "contents"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("content hint: %s", raw)
		}
	}
	if _, err = s.Browse(context.Background(), "/proc", "", 200, false, BrowseOptions{ShowHidden: true}); !errors.Is(err, ErrDenied) {
		t.Fatal("system tree admitted")
	}
	if _, err = s.Browse(context.Background(), root, "", 200, false, BrowseOptions{Prefix: "../"}); !errors.Is(err, ErrInput) {
		t.Fatal("path prefix admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Browse(ctx, root, "", 200, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestFilesystemPermissionRefusalIsRealAndReadOnly(t *testing.T) {
	s, _, _ := newService(t)
	root := t.TempDir()
	s.PickerRoots = []string{root}
	denied := filepath.Join(root, "denied")
	if err := os.Mkdir(denied, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(denied, 0700)
	page, err := s.Browse(context.Background(), root, "", 200, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Readable || page.Entries[0].Reason != "permission" {
		t.Fatalf("unreadable folder: %+v", page)
	}
	page, err = s.Browse(context.Background(), denied, "", 200, false, BrowseOptions{CheckWritable: true})
	if !errors.Is(err, ErrDenied) || page.Reason != "permission" || page.Writable {
		t.Fatalf("selected folder: %+v %v", page, err)
	}
}

func TestFilesystemPrefixFindsNameBeyondOrdinaryCap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _, _ := newService(t)
	root := t.TempDir()
	s.PickerRoots = []string{root}
	for n := 0; n < maxDirectoryNames+5; n++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("folder-%05d", n)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	names, err := dir.Readdirnames(maxDirectoryNames + 5)
	dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	target := names[len(names)-1]
	page, err := s.Browse(context.Background(), root, "", 200, false, BrowseOptions{Prefix: target})
	if err != nil || page.Truncated || len(page.Entries) != 1 || page.Entries[0].Name != target {
		t.Fatalf("beyond-cap prefix %+v %v", page, err)
	}
}
