package catalog

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
)

var data2Plans = flag.Bool("data2-plans", false, "measure lane I query plans on the runner's 1k smoke fixture")

type smokeTargets struct {
	ids       map[string]string
	items     int
	libraries map[string]string
}

func smokeItem(ctx context.Context, tx *sql.Tx, library int64, root, path, title string, kind compactcatalog.Kind, parent int64, facts map[string]any, artist int64) (int64, error) {
	id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: kind, Parent: parent, Key: compactcatalog.ItemKey(root, path, 0), Title: title, Added: "2026-01-01T00:00:00.000Z"})
	if err != nil {
		return 0, err
	}
	if err = compactcatalog.SetFactsTx(ctx, tx, id, facts); err != nil {
		return 0, err
	}
	asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 600})
	if err != nil {
		return 0, err
	}
	if err = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); err != nil {
		return 0, err
	}
	if kind == compactcatalog.Movie {
		if err = compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, "tmdb", []compactcatalog.Term{{SourceID: "16", Name: "Animation"}}); err != nil {
			return 0, err
		}
	}
	if kind == compactcatalog.Track {
		if err = compactcatalog.SetSongArtistsTx(ctx, tx, id, []int64{artist}); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func smokeFixture(t *testing.T, c *catalogtest.Catalog) smokeTargets {
	t.Helper()
	targets := smokeTargets{ids: map[string]string{}, items: 0, libraries: map[string]string{}}
	filmLib := c.Library("films", "Films", "movie", "/films")
	tvLib := c.Library("tv", "TV", "tv", "/tv")
	musicLib := c.Library("music", "Music", "music", "/music")
	bookLib := c.Library("books", "Books", "audiobook", "/books")
	for key, library := range map[string]string{"movie": "films", "episode": "tv", "song": "music", "audiobook_file": "books"} {
		targets.libraries[key] = library
	}
	show := c.Show(tvLib, "Runner show", 2020)
	season := c.Season(show, 1)
	artist := c.Artist(musicLib, "Runner artist")
	album := c.Album(artist, "Runner album", 2020)
	book := c.Book(bookLib, "Runner book", "Runner author")
	var roots = map[int64]string{}
	for _, lib := range []int64{filmLib, tvLib, musicLib, bookLib} {
		var root string
		if err := c.DB.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, lib).Scan(&root); err != nil {
			t.Fatal(err)
		}
		roots[lib] = root
	}
	targetIDs := map[string]int64{}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 592; i++ {
			path := fmt.Sprintf("/films/f%06d.mkv", i)
			id, err := smokeItem(ctx, tx, filmLib, roots[filmLib], path, fmt.Sprintf("Film %06d", i), compactcatalog.Movie, 0, nil, 0)
			if err != nil {
				return err
			}
			if i == 0 {
				targetIDs["movie"] = id
			}
		}
		for i := 0; i < 240; i++ {
			path := fmt.Sprintf("/tv/e%06d.mkv", i)
			id, err := smokeItem(ctx, tx, tvLib, roots[tvLib], path, fmt.Sprintf("Episode %06d", i), compactcatalog.Episode, season.ID, map[string]any{"show_id": show.ID, "season_id": season.ID, "numbering": "seasonal", "number": i + 1}, 0)
			if err != nil {
				return err
			}
			if i == 0 {
				targetIDs["episode"] = id
			}
		}
		for i := 0; i < 144; i++ {
			path := fmt.Sprintf("/music/t%06d.mkv", i)
			id, err := smokeItem(ctx, tx, musicLib, roots[musicLib], path, fmt.Sprintf("Track %06d", i), compactcatalog.Track, album.ID, map[string]any{"album_id": album.ID, "track_number": i + 1}, artist.ID)
			if err != nil {
				return err
			}
			if i == 0 {
				targetIDs["song"] = id
			}
		}
		for i := 0; i < 24; i++ {
			path := fmt.Sprintf("/books/p%06d.mkv", i)
			id, err := smokeItem(ctx, tx, bookLib, roots[bookLib], path, fmt.Sprintf("Part %06d", i), compactcatalog.Part, book.ID, map[string]any{"book_id": book.ID, "part_number": i + 1}, 0)
			if err != nil {
				return err
			}
			if i == 0 {
				targetIDs["audiobook_file"] = id
			}
		}
		return nil
	})
	for kind, id := range targetIDs {
		targets.ids[kind] = c.Public(id)
	}
	targets.items = 592 + 240 + 144 + 24
	c.Drain()
	return targets
}

func smokeWholeLibrary(t *testing.T, s *Service, r HomeRequest) []string {
	t.Helper()
	ids, whole, err := s.smallLibraryItems(r, 1<<20)
	if err != nil || !whole {
		t.Fatal("whole library", whole, err)
	}
	return ids
}

func TestRelatedRecommendationSmokePlans(t *testing.T) {
	if !*data2Plans {
		t.Skip("runner-only fixture and plan measurement")
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "smoke.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	shape := smokeFixture(t, c)
	if _, err = db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	for _, kind := range []string{"movie", "episode", "song", "audiobook_file"} {
		id, lib := shape.ids[kind], shape.libraries[kind]
		r := HomeRequest{Libraries: []string{lib}}
		for index, query := range relatedPoolQueries {
			rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, id)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s candidate seek %d", kind, index+1)
			for rows.Next() {
				var node, parent, unused int
				var detail string
				if err = rows.Scan(&node, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				t.Log(detail)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		pool, _, err := s.relatedPool(r, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(pool) > relatedPoolLimit {
			t.Fatal("unbounded pool", len(pool))
		}
		for _, mode := range []string{"before", "after"} {
			var base string
			var args []any
			if mode == "before" {
				base, args = recBase(r, smokeWholeLibrary(t, s, r))
			} else {
				base, args = recBase(r, pool)
			}
			rows, err := db.Query(`EXPLAIN QUERY PLAN `+base+` SELECT f.f FROM facets f JOIN members m ON m.work=f.work WHERE m.id=? ORDER BY f.f`, append(args, id)...)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s %s items=%d candidates=%d", kind, mode, shape.items, len(pool))
			indexed := false
			for rows.Next() {
				var node, parent, unused int
				var detail string
				if err = rows.Scan(&node, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				t.Log(detail)
				if strings.Contains(detail, "SEARCH i USING INDEX ") && strings.Contains(detail, "id=?") {
					indexed = true
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "after" && !indexed {
				t.Fatal("candidate hydration lost its item PK seek")
			}
		}
	}
}
