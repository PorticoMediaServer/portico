package libraryfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOnlyFilesReallyInsideALibraryFolderOpen(t *testing.T) {
	library, elsewhere := t.TempDir(), t.TempDir()
	inside := filepath.Join(library, "Films", "film.mkv")
	if err := os.MkdirAll(filepath.Dir(inside), 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(elsewhere, "secret")
	for _, path := range []string{inside, secret} {
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := Open(inside, []string{library})
	if err != nil {
		t.Fatalf("a file in the library: %v", err)
	}
	file.Close()

	refused := []string{secret, library, filepath.Join(library, "missing.mkv"), "relative.mkv", filepath.Join(library, "Films", "..", "..", filepath.Base(elsewhere), "secret")}
	if os.Symlink(secret, filepath.Join(library, "link.mkv")) == nil {
		refused = append(refused, filepath.Join(library, "link.mkv"))
	}
	if os.Symlink(elsewhere, filepath.Join(library, "folder-link")) == nil {
		refused = append(refused, filepath.Join(library, "folder-link", "secret"))
	}
	for _, path := range refused {
		if file, err := Open(path, []string{library}); !errors.Is(err, ErrOutside) {
			if file != nil {
				file.Close()
			}
			t.Fatalf("%s opened: %v", path, err)
		}
	}
	// A library that is itself reached through a link still works, and a link
	// between two library folders is inside a library.
	alias := filepath.Join(t.TempDir(), "alias")
	if os.Symlink(library, alias) == nil {
		if _, err := Resolve(filepath.Join(alias, "Films", "film.mkv"), []string{alias}); err != nil {
			t.Fatalf("a library behind a link: %v", err)
		}
	}
}
