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
