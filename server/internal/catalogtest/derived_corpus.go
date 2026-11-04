package catalogtest

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// DerivedCorpus exercises the real write API for every catalogue media kind, nested
// containers, alternate files, edits, moves, deletion and source availability.
// Family tests reuse it and add their own policy or provider inputs. Live TV
// has a separate channel model and needs its own family corpus.
type DerivedCorpus struct {
	C      *Catalog
	Items  Names
	Stages []DerivedStage
}
type DerivedStage struct {
	Name  string
	Apply func()
}

func DerivedSeed(c *Catalog) *DerivedCorpus {
	x := &DerivedCorpus{C: c, Items: Names{}}
	movies := c.Library("films", "Films", "movie", "/films")
	x.Items["film"] = c.Movie(movies, "/films/Alpha.mkv", "Alpha", 1999)
	x.Items["deleted"] = c.Movie(movies, "/films/Deleted.mkv", "Deleted", 2001)
	c.File(x.Items["film"].ID, "/films/Alpha-alt.mkv", 7200)
	// More than a worker batch, with equal headings and duplicate titles,
	// exercises cursors and counted projections as well as isolated rows.
	for i := 0; i < 72; i++ {
		c.Movie(movies, fmt.Sprintf("/films/batch/%03d.mkv", i), fmt.Sprintf("Shared title %02d", i/2), 1980+i%40)
	}
	c.Collection(movies, "Double bill", x.Items["film"], x.Items["deleted"])
	c.Genres(x.Items["film"].ID, "fixture", "Drama", "Science fiction")
	for _, kind := range []string{"tv", "anime"} {
		lib := c.Library(kind, kind, kind, "/"+kind)
		show := c.Show(lib, "Show "+kind, 2026)
		season := c.Season(show, 1)
		x.Items[kind+"-special"] = c.Episode(show, c.Season(show, 0), 1, "/"+kind+"/Special.mkv")
		x.Items[kind] = c.Episode(show, season, 1, "/"+kind+"/S01E01.mkv")
		x.Items[kind+"-second"] = c.Episode(show, season, 2, "/"+kind+"/S01E02.mkv")
		x.Items[kind+"-season"] = c.Season(show, 2)
	}
	music := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(music, "Performer"), "Record", 2003)
	for i := 1; i <= 3; i++ {
		x.Items[fmt.Sprint("song", i)] = c.Song(album, i, fmt.Sprintf("/music/%02d.flac", i), fmt.Sprint("Track ", i))
	}
	books := c.Library("audio", "Audiobooks", "audiobook", "/audio")
	book := c.Book(books, "Spoken book", "Author")
	for i := 1; i <= 2; i++ {
		x.Items[fmt.Sprint("part", i)] = c.BookFile(book, i, fmt.Sprintf("/audio/%02d.m4b", i))
	}
	for _, v := range []struct {
		library string
		kind    compactcatalog.Kind
		format  string
	}{{"book", compactcatalog.Ebook, "epub"}, {"comic", compactcatalog.Comic, "cbz"}} {
		lib := c.Library(v.library, v.library, v.library, "/"+v.library)
		item := c.Entity(compactcatalog.Entity{Library: lib, Kind: v.kind, Key: "reading:" + v.library, Title: "Read " + v.library, Added: "2026-01-01T00:00:00Z"}, nil)
		item.Asset, item.Token = c.File(item.ID, "/"+v.library+"/Title."+v.format, 0)
		c.Exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,?)`, item.ID, item.Asset, v.format)
		x.Items[v.library] = item
	}
	x.Stages = []DerivedStage{
		{"metadata edit", func() {
			c.Fields(x.Items["film"].ID, map[string]any{"title": "Renamed Ω film", "year": 2025})
			c.Genres(x.Items["film"].ID, "fixture", "Crime")
		}},
		{"move episode", func() {
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				season := x.Items["tv-season"].ID
				if _, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=?`, season, x.Items["tv"].ID); err != nil {
					return err
				}
				var show int64
				if err := tx.QueryRowContext(ctx, `SELECT show_id FROM catalog_seasons WHERE entity_id=?`, season).Scan(&show); err != nil {
					return err
				}
				return compactcatalog.SetFactsTx(ctx, tx, x.Items["tv"].ID, map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": 1})
			})
		}},
		{"delete", func() { c.Delete(x.Items["deleted"].ID); c.Delete(x.Items["song3"].ID) }},
		{"source disabled", func() { c.Exec(`UPDATE library_sources SET enabled=0 WHERE library_id IN('films','book','comic')`) }},
		{"source restored", func() { c.Exec(`UPDATE library_sources SET enabled=1 WHERE library_id IN('films','book','comic')`) }},
		{"restriction input", func() {
			c.Attributes(x.Items["film"].ID, "contentRating", "R")
			c.Attributes(x.Items["tv"].ID, "label", "restricted")
		}},
	}
	return x
}
