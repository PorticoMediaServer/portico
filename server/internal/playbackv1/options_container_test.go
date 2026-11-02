package playbackv1

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// PB-1, MU-1, MU-2: playback options name the container a start plays
// within, so a client's Play on an episode, a song or a book part queues the
// whole show, album or book anchored on that item instead of one entry.
func TestOptionsContainerNamesTheParent(t *testing.T) {
	catalog := catalogtest.Open(t)
	root := t.TempDir()
	library := catalog.Library("container-library", "Container library", "movie", root)
	show := catalog.Show(library, "Fixture show", 2026)
	season := catalog.Season(show, 1)
	episode := catalog.Episode(show, season, 1, filepath.Join(root, "s01e01.mkv"))
	special := catalog.Episode(show, catalog.Season(show, 0), 1, filepath.Join(root, "s00e01.mkv"))
	album := catalog.Album(catalog.Artist(library, "Fixture artist"), "Fixture album", 2026)
	song := catalog.Song(album, 1, filepath.Join(root, "song.m4a"), "Song")
	book := catalog.Book(library, "Fixture book", "Author")
	part := catalog.BookFile(book, 2, filepath.Join(root, "part2.m4b"))
	movie := catalog.Movie(library, filepath.Join(root, "film.mkv"), "Film", 2026)

	ctx := context.Background()
	tx, done, err := dbwork.BeginRead(ctx, catalog.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, c := range []struct {
		name, kind, item string
		want             *OptionsContainer
	}{
		{"episode", "episode", episode.Public, &OptionsContainer{Kind: "show", ID: show.Public}},
		{"special", "episode", special.Public, &OptionsContainer{Kind: "show", ID: show.Public}},
		{"song", "song", song.Public, &OptionsContainer{Kind: "album", ID: album.Public}},
		{"book part", "audiobook_file", part.Public, &OptionsContainer{Kind: "book", ID: book.Public}},
		{"movie", "movie", movie.Public, nil},
	} {
		got, err := containerOf(ctx, tx, c.kind, c.item)
		if err != nil {
			t.Fatal(c.name, err)
		}
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Fatalf("%s: container %+v, want %+v", c.name, got, c.want)
		}
	}
	done()

	// A retired parent has no container: the queue selector would refuse it.
	catalog.Exec(`UPDATE catalog_entities SET retired=1 WHERE id=?`, album.ID)
	tx, done2, err := dbwork.BeginRead(ctx, catalog.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer done2()
	if got, err := containerOf(ctx, tx, "song", song.Public); err != nil || got != nil {
		t.Fatal("retired album offered", got, err)
	}
}

// The real options read carries it.
func TestOptionsCarryTheSongAlbum(t *testing.T) {
	f := newCarryFixture(t, true)
	view, err := f.service.Options(context.Background(), f.caller.Principal, f.items[0].Public, Preview{}, nil, Reach{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Container == nil || view.Container.Kind != "album" || view.Container.ID == "" {
		t.Fatalf("options container %+v", view.Container)
	}
}
