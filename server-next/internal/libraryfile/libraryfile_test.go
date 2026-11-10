package libraryfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOnlyPathsWrittenBeneathALibraryFolderOpen(t *testing.T) {
	library, elsewhere := t.TempDir(), t.TempDir()
	inside := filepath.Join(library, "Films", "film.mkv")
	if err := os.MkdirAll(filepath.Dir(inside), 0700); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(elsewhere, "remote.mkv")
	for path, content := range map[string]string{inside: "inside", remote: "remote", filepath.Join(library, "empty.mkv"): ""} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	opens := map[string]string{inside: "inside"}
	// A link to a file, and a link to a folder, somewhere else: both play.
	if os.Symlink(remote, filepath.Join(library, "link.mkv")) == nil {
		opens[filepath.Join(library, "link.mkv")] = "remote"
	}
	if os.Symlink(elsewhere, filepath.Join(library, "Remote")) == nil {
		opens[filepath.Join(library, "Remote", "remote.mkv")] = "remote"
		real, err := Resolve(filepath.Join(library, "Remote", "remote.mkv"), []string{library})
		if want, _ := filepath.EvalSymlinks(remote); err != nil || real != want {
			t.Fatalf("a linked file resolved to %q: %v", real, err)
		}
	}
	for path, want := range opens {
		file, err := Open(path, []string{library})
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		got, _ := io.ReadAll(file)
		file.Close()
		if string(got) != want {
			t.Fatalf("%s read %q", path, got)
		}
	}

	refused := []string{
		remote, library, "relative.mkv",
		filepath.Join(library, "missing.mkv"), filepath.Join(library, "empty.mkv"), filepath.Join(library, "Films"),
		library + string(os.PathSeparator) + filepath.Join("Films", "..", "..", filepath.Base(elsewhere), "remote.mkv"),
	}
	for _, path := range refused {
		if file, err := Open(path, []string{library}); !errors.Is(err, ErrOutside) {
			if file != nil {
				file.Close()
			}
			t.Fatalf("%s opened: %v", path, err)
		}
		if path == remote || path == "relative.mkv" || path == refused[len(refused)-1] {
			if _, err := Resolve(path, []string{library}); !errors.Is(err, ErrOutside) {
				t.Fatalf("%s resolved: %v", path, err)
			}
		}
	}
}
