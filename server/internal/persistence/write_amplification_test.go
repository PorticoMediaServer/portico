package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Scanning two million items is the workload that decides whether this server
// can hold the libraries it claims to. This measures the current catalogue
// write path: one entity, file, link, genres, credits, and browse attributes.
func TestCatalogueInsertCostIsFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := Open(filepath.Join(t.TempDir(), "amplification.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	library := persistenceLibrary(t, db, "lib", "Films", "movie", "/films")

	const batch = 10
	measure := func(offset, rows int) time.Duration {
		start := time.Now()
		for written := 0; written < rows; written += batch {
			err = dbwork.WithWriteTxContext(context.Background(), db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
				for i := written; i < written+batch && i < rows; i++ {
					key := fmt.Sprintf("item-%08d", offset+i)
					path := "/films/" + key + ".mp4"
					item, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
						Library: library, Kind: compactcatalog.Movie,
						Key: compactcatalog.ItemKey("/films", path, 0), Title: "Film " + key,
						Year: 2001, Added: "2026-01-01T00:00:00.000Z",
					})
					if e != nil {
						return e
					}
					asset, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{
						Path: path, Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60,
					})
					if e != nil {
						return e
					}
					if e = compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{}); e != nil {
						return e
					}
					if e = compactcatalog.SetFieldsTx(ctx, tx, item, compactcatalog.Automatic, map[string]any{"poster_url": "p"}); e != nil {
						return e
					}
					for field, value := range map[string]string{
						"contentRating": "PG-13", "label": "violence", "studio": "Studio", "tag": "Tag",
					} {
						if e = compactcatalog.SetAttributesTx(ctx, tx, item, field, []string{value}); e != nil {
							return e
						}
					}
					genres := make([]compactcatalog.Term, 3)
					for g := range genres {
						name := fmt.Sprintf("Genre %d", g)
						genres[g] = compactcatalog.Term{SourceID: name, Name: name}
					}
					if e = compactcatalog.SetTermsTx(ctx, tx, item, compactcatalog.VocabGenre, "fixture", genres); e != nil {
						return e
					}
					credits := make([]compactcatalog.Credit, 6)
					for c := range credits {
						person := fmt.Sprintf("person-%05d", (offset+i)*3+c)
						credits[c] = compactcatalog.Credit{
							PersonKey: "fixture:" + person, PersonName: "Person " + person,
							ProviderPersonID: person, CreditID: person, CreditedName: "Person " + person,
							Role: "Actor", Department: "cast", Ordinal: c,
						}
					}
					if e = compactcatalog.SetCreditsTx(ctx, tx, item, "fixture", credits); e != nil {
						return e
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		return time.Since(start)
	}

	// The default suite compares blocks of 50 rows; release and deep tiers use
	// 1,000. Drain between blocks so both timed samples begin with settled derived
	// work while measuring synchronous catalogue writes.
	rows := 50
	if scaleTier() {
		rows = 1000
	}
	first := measure(0, rows)
	persistenceDrain(t, db)
	second := measure(rows, rows)
	persistenceDrain(t, db)
	perItem := second / time.Duration(rows)
	rate := float64(rows) / second.Seconds()
	t.Logf("insert cost: first %d rows %s, next %d rows %s (%s/item, %.0f items/s)",
		rows, first.Round(time.Millisecond), rows, second.Round(time.Millisecond), perItem.Round(time.Microsecond), rate)

	// Flatness is the property that matters: the second block must not cost
	// appreciably more than the first, because a trigger whose cost is
	// proportional to the size of the table it reads shows up here as growth.
	if second > first*2 {
		t.Fatalf("inserting the second %d rows took %s against %s for the first: a trigger's cost is growing with the catalogue", rows, second, first)
	}
	if scaleTier() && perItem > 20*time.Millisecond {
		t.Fatalf("one catalogue row costs %s to insert; a two-million-item scan would take %s", perItem, (perItem * 2_000_000).Round(time.Minute))
	}
}
