package metadata_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb/tmdbtest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// titleProber reports a title written inside every file, as a real file's tags would.
type titleProber struct{}

func (titleProber) Probe(_ context.Context, path string) (scan.Facts, error) {
	title := "Matrix Written Inside"
	if strings.Contains(path, "Zzyzx") {
		title = "The Title Written Inside"
	}
	return scan.Facts{Container: "mkv", DurationMS: 60_000, Title: title, Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264"}}}, nil
}

// A film its provider could not match shows the title written inside its file; one it matched
// keeps the provider's, though its file has a title inside too; and before either is looked up
// each keeps the name its file name gives, which is what it is looked up by.
func TestAnUnmatchedFilmShowsTheTitleInsideItsFile(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	root := t.TempDir()
	for i, name := range []string{"the.matrix.1999.1080p.mkv", "Zzyzx Home Video (2011).mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, 100+i), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{root}); err != nil {
		t.Fatal(err)
	}
	viewers, _ := seed.Profiles(ctx, d, 1)
	libraries, _ := d.AllLibraries(ctx)
	if _, err := (&scan.Scanner{DB: d, Prober: titleProber{}}).Scan(ctx, libraries[0]); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, d: d, lib: libraries[0], viewer: viewers[0],
		s: &metadata.Service{DB: d, Providers: map[string]metadata.Provider{"tmdb": tmdb.New("test-token", tmdbtest.New(t).URL)}}}
	f.title(db.KindMovie, "Zzyzx Home Video") // not looked up yet: the file name's
	f.match()
	f.title(db.KindMovie, "The Title Written Inside")
	f.title(db.KindMovie, "The Matrix")
}

// A film showing a title written inside its file is looked up by the name its file name gives,
// never by the title shown: "The Matrix (1999).mkv" is searched as The Matrix, 1999, and found.
func TestALookupSearchesByTheFileNameNotTheTitleShown(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	viewers, _ := seed.Profiles(ctx, d, 1)
	libraries, _ := d.AllLibraries(ctx)
	lib := libraries[0]
	ref := db.FileRef{Name: "The Matrix (1999).mkv"}
	// The film as a failed lookup leaves it: named by the title written inside its file.
	if _, err := d.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{RelPath: ""}},
		Files: []db.ImportFile{{FileRef: ref, Role: db.FileMedia, Size: 1000, Container: "mkv", DurationMS: 60_000}},
		Items: []db.ImportItem{{Kind: db.KindMovie, Title: "Matrix Rip Written Inside", Keys: []db.ItemKey{{Kind: db.KeyFile, Key: ref.Name}},
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: ref}}}}}}}); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, d: d, lib: lib, viewer: viewers[0],
		s: &metadata.Service{DB: d, Providers: map[string]metadata.Provider{"tmdb": tmdb.New("test-token", tmdbtest.New(t).URL)}}}
	if stats := f.match(); stats.Matched != 1 {
		t.Fatalf("looked up by the title shown, not the file name: %+v", stats)
	}
	if film := f.title(db.KindMovie, "The Matrix"); film.Year != 1999 {
		t.Fatalf("matched to %+v", film)
	}
}
