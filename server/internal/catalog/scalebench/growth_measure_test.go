package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Extend the existing mixed reference rather than build a competing seeder.
// Three jobs use identical density and fixed page sizes, varying only N.
func TestCrossKindGrowthMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_GROWTH_ITEMS"))
	if n == 0 {
		t.Skip("opt-in hosted growth diagnostic")
	}
	dir := os.Getenv("PORTICO_SCALE_REPORT")
	if dir == "" {
		t.Fatal("explicit evidence directory required")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	save := func(name string, v any) {
		t.Helper()
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name+".json"), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	seed := filepath.Join(os.Getenv("TMPDIR"), fmt.Sprintf("F-growth-%d.sqlite", n))
	began := time.Now()
	seeded := false
	if _, e := os.Stat(seed); os.IsNotExist(e) {
		seeded = true
		db, e := persistence.Open(seed + ".building")
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = seedReference(db, n, 0, n, 500, false); e != nil {
			db.Close()
			t.Fatal(e)
		}
		if e = seedGrowthBooks(db, n); e != nil {
			db.Close()
			t.Fatal(e)
		}
		if e = compactcatalog.Drain(context.Background(), db); e != nil {
			db.Close()
			t.Fatal(e)
		}
		if e = persistence.RefreshPlannerStatistics(context.Background(), db); e != nil {
			db.Close()
			t.Fatal(e)
		}
		if _, e = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); e != nil {
			db.Close()
			t.Fatal(e)
		}
		if e = db.Close(); e != nil {
			t.Fatal(e)
		}
		if e = os.Rename(seed+".building", seed); e != nil {
			t.Fatal(e)
		}
	}
	save("seed", map[string]any{"base_items": n, "new_seed": seeded, "seconds": time.Since(began).Seconds(), "peak_rss_kb": growthPeakRSS(), "cpus": runtime.NumCPU(), "go": runtime.Version(), "boundary": "reference seed, extra reading/audiobook facts, full derivation; not a scan"})
	path := filepath.Join(t.TempDir(), "db")
	in, e := os.Open(seed)
	if e != nil {
		t.Fatal(e)
	}
	out, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.Copy(out, in)
	in.Close()
	out.Close()
	if e != nil {
		t.Fatal(e)
	}
	db, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := catalog.New(db)
	counts := map[string]int{}
	for _, table := range []string{"catalog_entities", "catalog_assets", "catalog_songs", "catalog_albums", "catalog_artists", "catalog_episodes", "catalog_shows", "catalog_books", "catalog_book_files", "catalog_book_chapters", "reading_publications"} {
		var count int
		if e = db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); e != nil {
			t.Fatal(e)
		}
		counts[table] = count
	}
	save("counts", counts)
	results := []routeResult{}
	measure := func(name string, fn func(context.Context, *catalog.Service) error) {
		r, e := exerciseRead(db, s, name, 10, fn)
		if e != nil {
			t.Fatal(e)
		}
		results = append(results, r)
		save("growth", map[string]any{"base_items": n, "routes": results, "peak_rss_kb": growthPeakRSS()})
	}
	libs := []string{"movies", "tv", "music", "audio", "reading", "comics", "anime"}
	for _, scope := range [][2]string{{"movies", "movies"}, {"tv", "shows"}, {"tv", "seasons"}, {"tv", "episodes"}, {"music", "artists"}, {"music", "albums"}, {"music", "songs"}, {"audio", "books"}, {"reading", "books"}, {"comics", "comics"}, {"anime", "shows"}} {
		library, pivot := scope[0], scope[1]
		v := catalog.Viewer{Profile: "viewer", Fence: "growth", Libraries: []string{library}}
		measure(library+"/browse/"+pivot, func(ctx context.Context, s *catalog.Service) error {
			p, e := s.BrowseEntities(v, catalog.BrowseRequest{Library: library, Profile: v.Profile, ViewerFence: v.Fence, Pivot: pivot, Limit: 40})
			if e == nil && len(p.Entries) == 0 {
				return fmt.Errorf("empty browse %s/%s", library, pivot)
			}
			return e
		})
	}
	for _, lib := range libs {
		v := catalog.Viewer{Profile: "viewer", Fence: "growth", Libraries: []string{lib}}
		measure(lib+"/home", func(ctx context.Context, s *catalog.Service) error {
			_, e := s.HomeRows(catalog.HomeRequest{Viewer: v, ServerID: "s", Profile: v.Profile, ViewerFence: v.Fence, Libraries: v.Libraries, Now: time.Now()})
			return e
		})
		for _, query := range []string{"Title", "Title 000000042"} {
			q := query
			measure(lib+"/search/"+q, func(ctx context.Context, s *catalog.Service) error {
				p, e := s.Search(ctx, catalog.SearchRequest{Viewer: v, Profile: v.Profile, ViewerFence: v.Fence, Libraries: v.Libraries, Q: q, Limit: 12})
				if e != nil {
					return e
				}
				for _, g := range p.Groups {
					if g.ErrorCode != "" {
						return fmt.Errorf("%s: %s", g.ID, g.ErrorCode)
					}
				}
				return nil
			})
		}
		rows, e := db.Query(`SELECT kind,min(id) FROM catalog_entities WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND retired=0 GROUP BY kind`, lib)
		if e != nil {
			t.Fatal(e)
		}
		ids := map[int]int64{}
		for rows.Next() {
			var k int
			var id int64
			if e = rows.Scan(&k, &id); e != nil {
				t.Fatal(e)
			}
			ids[k] = id
		}
		rows.Close()
		for kind, id := range ids {
			var pid string
			if e = db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, id).Scan(&pid); e != nil {
				t.Fatal(e)
			}
			measure(fmt.Sprintf("%s/detail/kind%d", lib, kind), func(ctx context.Context, s *catalog.Service) error {
				if kind == 20 || kind == 21 {
					_, e := s.ReadingDetail(v, "s", pid, nil)
					return e
				}
				_, e := s.DetailWithRelated(v, "s", pid, false, false)
				return e
			})
			if kind == 8 {
				measure("audio/chapters", func(ctx context.Context, s *catalog.Service) error {
					p, _, e := s.BookChapters(v, pid, "", 40)
					if e == nil && len(p) == 0 {
						return fmt.Errorf("empty chapter page")
					}
					return e
				})
			}
		}
	}

	for _, lib := range []string{"reading", "comics"} {
		actor := catalog.ResourceActor{Authority: "local", AccountID: "growth-account", ProfileID: "growth-profile"}
		profile := identity.PersonalKey(identity.Viewer{Authority: actor.Authority, AccountID: actor.AccountID, ProfileID: actor.ProfileID})
		v := catalog.Viewer{Profile: profile, Fence: "growth-reading", Libraries: []string{lib}}
		name := "Growth " + lib
		pivot := "books"
		if lib == "comics" {
			pivot = "comics"
		}
		def := catalog.SavedViewDefinition{LibraryID: lib, Pivot: pivot, Sort: []catalog.BrowseSortSelection{{Field: "title", Direction: "asc"}}, Presentation: "grid"}
		made, e := s.MutateSavedResource(actor, "", "create", catalog.SavedResourceMutation{OperationID: "growth-" + lib, Kind: "view", Name: &name, Definition: &def}, func(*sql.Tx) error { return nil })
		if e != nil {
			t.Fatal(e)
		}
		measure(lib+"/home_saved_row", func(ctx context.Context, s *catalog.Service) error {
			p, e := s.HomeSingleRow(catalog.HomeRequest{Viewer: v, ServerID: "s", Profile: v.Profile, ViewerFence: v.Fence, Libraries: v.Libraries, RowOrder: []string{"view:" + made.ResourceID}}, "view:"+made.ResourceID, catalog.HomeRowPage{Limit: 12})
			if e == nil && len(p.Entries) == 0 {
				return fmt.Errorf("empty reading Home row")
			}
			return e
		})
		for _, field := range []string{"author", "series"} {
			field := field
			measure(lib+"/facet/"+field, func(ctx context.Context, s *catalog.Service) error {
				_, e := s.Facets(v, catalog.FacetRequest{Library: lib, Pivot: map[string]string{"author": "authors", "series": "series"}[field], Field: field, Limit: 40})
				return e
			})
		}
	}
	for _, view := range []string{"authors", "series"} {
		v := catalog.Viewer{Profile: "viewer", Fence: "growth", Libraries: []string{"audio"}}
		request := catalog.ContentRequest{Viewer: v, Library: "audio", Profile: v.Profile, ViewerFence: v.Fence, View: view, Limit: 40}
		measure("audio/"+view, func(ctx context.Context, s *catalog.Service) error {
			p, e := s.Content(request)
			if e == nil && (len(p.Sections) == 0 || len(p.Sections[0].Entries) == 0) {
				return fmt.Errorf("empty audio %s", view)
			}
			return e
		})
	}
	v := catalog.Viewer{Profile: "viewer", Fence: "growth", Libraries: []string{"movies"}}
	measure("movies/see_all", func(ctx context.Context, s *catalog.Service) error {
		_, e := s.HomeSingleRow(catalog.HomeRequest{Viewer: v, ServerID: "s", Profile: v.Profile, ViewerFence: v.Fence, Libraries: v.Libraries, Now: time.Now()}, "new_on_server", catalog.HomeRowPage{Limit: 40})
		return e
	})
}

func growthPeakRSS() int64 {
	b, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(b), "\n") {
		var kb int64
		if _, e := fmt.Sscanf(line, "VmHWM: %d kB", &kb); e == nil {
			return kb
		}
	}
	return 0
}
func seedGrowthBooks(db *sql.DB, n int) error {
	ctx := context.Background()
	var audio, reading, comics, anime int64
	if e := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		for _, l := range []struct {
			id, kind string
			dst      *int64
		}{{"audio", "audiobook", &audio}, {"reading", "book", &reading}, {"comics", "comic", &comics}, {"anime", "anime", &anime}} {
			if _, e := tx.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,?,?)`, l.id, l.id, l.kind, "/fixture/"+l.id); e != nil {
				return e
			}
			id, e := compactcatalog.LibraryTx(ctx, tx, l.id)
			if e != nil {
				return e
			}
			*l.dst = id
		}
		return nil
	}); e != nil {
		return e
	}
	count := max(10, n/20)
	for off := 0; off < count; off += 50 {
		if e := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			for i := off; i < min(count, off+50); i++ {
				key := fmt.Sprintf("%09d", i)
				author := fmt.Sprintf("Author %06d", i/20)
				series := fmt.Sprintf("Series %06d", i/5)
				book, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: audio, Kind: compactcatalog.Book, Key: compactcatalog.BookKey(key), Title: "Title " + key, Added: "2026-01-01T00:00:00.000Z"})
				if e != nil {
					return e
				}
				if e = compactcatalog.SetFactsTx(ctx, tx, book, map[string]any{"library_id": audio, "local_key": key, "author": author}); e != nil {
					return e
				}
				if _, _, e = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: audio, Kind: compactcatalog.Author, Key: compactcatalog.AuthorKey(author), Title: author}); e != nil {
					return e
				}
				for part := 0; part < 4; part++ {
					path := fmt.Sprintf("/fixture/audio/%s-%d.m4b", key, part)
					file, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: audio, Kind: compactcatalog.Part, Parent: book, Key: compactcatalog.ItemKey("/fixture/audio", path, 0), Title: fmt.Sprintf("Part %d", part+1), Added: "2026-01-01T00:00:00.000Z"})
					if e != nil {
						return e
					}
					if e = compactcatalog.SetFactsTx(ctx, tx, file, map[string]any{"book_id": book, "part_number": part + 1}); e != nil {
						return e
					}
					asset, token, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "m4b", AudioCodec: "aac", Duration: 3600})
					if e != nil {
						return e
					}
					if e = compactcatalog.LinkAssetTx(ctx, tx, file, asset, compactcatalog.Link{}); e != nil {
						return e
					}
					if _, e = tx.ExecContext(ctx, `INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('audio',?,'series','portico_json',?),('audio',?,'series_index','portico_json',?)`, token, series, token, fmt.Sprint(i%5+1)); e != nil {
						return e
					}
					chapters := make([]compactcatalog.Chapter, 12)
					for k := range chapters {
						chapters[k] = compactcatalog.Chapter{Title: fmt.Sprintf("Chapter %d", k+1), Start: float64(k * 300), End: float64((k + 1) * 300)}
					}
					if e = compactcatalog.SetBookChaptersTx(ctx, tx, file, chapters); e != nil {
						return e
					}
				}
				// A separate anime library uses the same show/season/episode write path.
				show, season, e := seedShow(ctx, tx, anime, i/100)
				if e != nil {
					return e
				}
				path := "/fixture/anime/" + key + ".mkv"
				item, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: anime, Kind: compactcatalog.Episode, Parent: season, Key: path, Title: "Title " + key, Added: "2026-01-01T00:00:00.000Z"})
				if e != nil {
					return e
				}
				if e = compactcatalog.SetFactsTx(ctx, tx, item, map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": i%100 + 1}); e != nil {
					return e
				}
				asset, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 1500})
				if e != nil {
					return e
				}
				if e = compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{}); e != nil {
					return e
				}
			}
			return nil
		}); e != nil {
			return e
		}
	}
	if e := fixture.SeedReadingGrowth(ctx, db, reading, compactcatalog.Ebook, "/fixture/reading", "epub", count); e != nil {
		return e
	}
	if e := fixture.SeedReadingGrowth(ctx, db, comics, compactcatalog.Comic, "/fixture/comics", "cbz", count); e != nil {
		return e
	}

	return nil
}
