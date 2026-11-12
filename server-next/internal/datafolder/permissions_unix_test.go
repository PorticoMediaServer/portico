//go:build !windows

package datafolder

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// A data folder others can open is made private, and only the folder itself: what is inside
// keeps its own mode. A second fix finds nothing to change.
func TestADataFolderOthersCanOpenIsMadePrivateAndNothingInside(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	os.Mkdir(data, 0o700)
	os.Chmod(data, 0o755)
	inside := filepath.Join(data, "portico.db")
	os.WriteFile(inside, nil, 0o644)
	os.Chmod(inside, 0o644)
	if open, err := ReadableByOthers(data); err != nil || !open {
		t.Fatalf("a 0755 data folder: %v %v", open, err)
	}
	for range 2 {
		if err := MakePrivate(data); err != nil {
			t.Fatal(err)
		}
	}
	if mode(t, data) != 0o700 || mode(t, inside) != 0o644 {
		t.Fatalf("after the fix: folder %o, file inside %o", mode(t, data), mode(t, inside))
	}
	if open, err := ReadableByOthers(data); err != nil || open {
		t.Fatalf("after the fix: %v %v", open, err)
	}
}

// A data folder given as a link is the folder it points to.
func TestADataFolderGivenAsALinkIsTheFolderItPointsTo(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "real"), filepath.Join(dir, "data")
	os.Mkdir(real, 0o700)
	os.Chmod(real, 0o750)
	os.Symlink(real, link)
	if err := MakePrivate(link); err != nil {
		t.Fatal(err)
	}
	if mode(t, real) != 0o700 {
		t.Fatalf("the folder linked to is %o", mode(t, real))
	}
}

// The folder at the path is swapped for another, or for a link to another, between the look
// and the change: the fix is refused, and neither folder is changed.
func TestAFolderSwappedBetweenTheLookAndTheChangeIsRefused(t *testing.T) {
	for _, swap := range []string{"link", "folder"} {
		t.Run(swap, func(t *testing.T) {
			dir := t.TempDir()
			data, moved, other := filepath.Join(dir, "data"), filepath.Join(dir, "moved"), filepath.Join(dir, "other")
			for _, d := range []string{data, other} {
				os.Mkdir(d, 0o700)
				os.Chmod(d, 0o755)
			}
			opened = func() {
				os.Rename(data, moved)
				if swap == "link" {
					os.Symlink(other, data)
				} else {
					os.Mkdir(data, 0o700)
					os.Chmod(data, 0o755)
				}
			}
			t.Cleanup(func() { opened = func() {} })
			if err := MakePrivate(data); !errors.Is(err, ErrChanged) {
				t.Fatalf("a swapped %s: %v", swap, err)
			}
			if mode(t, moved) != 0o755 || mode(t, other) != 0o755 {
				t.Fatalf("something was changed: the folder looked at %o, the other %o", mode(t, moved), mode(t, other))
			}
			if swap == "folder" && mode(t, data) != 0o755 {
				t.Fatalf("the folder swapped in was changed: %o", mode(t, data))
			}
		})
	}
}

// A folder the server's account does not own is refused with why.
func TestAFolderTheServerDoesNotOwnIsRefusedWithWhy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("run as root, every folder is the server's to change")
	}
	if err := MakePrivate("/"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("the root folder: %v", err)
	}
	if mode(t, "/")&others == 0 {
		t.Fatal("the root folder was changed")
	}
}
