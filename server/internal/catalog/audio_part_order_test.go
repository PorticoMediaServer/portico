package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The files of a book that carry no track tag and do not start with a number
// still say where they belong: after a "part", "chapter" or "track" word, or
// at the end of the name. Files catalogued out of order then play in order.
func TestUntaggedBookFilesAreNumberedFromTheirNames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, set := range [][]string{
		{"Chapter 03.mp3", "Chapter 01.mp3", "Chapter 02.mp3"},
		{"The Long Book - 03.mp3", "The Long Book - 01.mp3", "The Long Book - 02.mp3"},
		{"Part 3 of 12.mp3", "Part 1 of 12.mp3", "Part 2 of 12.mp3"},
		{"Volume 1 - Chapter 03 - Night.mp3", "Volume 1 - Chapter 01 - Dawn.mp3", "Volume 1 - Chapter 02 - Noon.mp3"},
	} {
		t.Run(set[0], func(t *testing.T) {
			db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			c := catalogtest.New(t, db)
			c.Library("books", "Audiobooks", "audiobook", "/books")
			s := New(db)
			// Catalogued in the order the scan happened to visit them.
			for _, name := range set {
				path := "/books/The Long Book/" + name
				var token string
				c.Write(func(ctx context.Context, tx *sql.Tx) error {
					var err error
					_, token, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mp3", AudioCodec: "mp3", Duration: 90})
					return err
				})
				c.Write(func(ctx context.Context, tx *sql.Tx) error {
					return s.commitAudio(ctx, tx, "books", "audiobook", "/books", token, path, assets.Facts{Container: "mp3", AudioCodec: "mp3", Duration: 90})
				})
			}
			c.Drain()
			rows, err := db.Query(`SELECT a.path FROM catalog_book_files part JOIN catalog_asset_links l ON l.entity_id=part.entity_id JOIN catalog_assets a ON a.id=l.asset_id ORDER BY part.disc_number,part.part_number,part.entity_id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var played []string
			for rows.Next() {
				var path string
				if err = rows.Scan(&path); err != nil {
					t.Fatal(err)
				}
				played = append(played, filepath.Base(path))
			}
			want := []string{set[1], set[2], set[0]}
			if strings.Join(played, "|") != strings.Join(want, "|") {
				t.Fatalf("parts play as %v, want %v", played, want)
			}
		})
	}
}

func TestPartNumberFromName(t *testing.T) {
	for name, want := range map[string]any{
		"Chapter 01.mp3":                         1,
		"chapter12.m4b":                          12,
		"Book Title - 03.mp3":                    3,
		"Part 2 of 12.mp3":                       2,
		"Pt. 4.mp3":                              4,
		"Book One Part 1 - Chapter 05.mp3":       5,
		"Track 007.mp3":                          7,
		"A Space Odyssey 2001.mp3":               nil,
		"Introduction.mp3":                       nil,
		"Catch-22 - Part 3.mp3":                  3,
		"Title_09.flac":                          9,
		"Dispatch Chapter 1000 words.mp3":        1000,
		"Chapter 0.mp3":                          nil,
		"Departure.mp3":                          nil, // "part" inside a word
		"/books/Part 9/The Long Book - Coda.mp3": nil, // only the file's own name
	} {
		if got := partNumberFromName(name); got != want {
			t.Errorf("%q is part %v, want %v", name, got, want)
		}
	}
}
