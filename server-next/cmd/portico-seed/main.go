// Command portico-seed fills a database with a synthetic library, through the same import a
// scan uses. Run it again on the same database and it is an unchanged rescan.
//
//	portico-seed -db /tmp/portico/main.db -items 20000
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

func main() {
	path := flag.String("db", "main.db", "database file")
	items := flag.Int("items", 20_000, "playable items to make (films, episodes, tracks and books)")
	seedValue := flag.Uint64("seed", 1, "the same seed and size give the same library")
	batch := flag.Int("batch", 40, "files per import transaction")
	phase := flag.String("phase", "all", "import, state or all")
	profiles := flag.Int("profiles", 10, "profiles to make in the state phase")
	state := flag.Int("state", 0, "state rows per profile; 0 means a tenth of the items")
	pictures := flag.Int("pictures", 0, "write a plain picture for this many of the titles' posters and covers, beside the database, so artwork can be fetched")
	flag.Parse()
	log.SetFlags(log.Ltime)

	ctx := context.Background()
	d, err := db.Open(ctx, *path)
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()
	report := map[string]any{"items": *items, "seed": *seedValue, "batchFiles": *batch}

	if *phase == "all" || *phase == "import" {
		last := time.Now()
		cfg := seed.Config{Seed: *seedValue, Items: *items, BatchFiles: *batch}
		if store := artwork.New(filepath.Dir(*path)); *pictures > 0 {
			cfg.Picture = func(hash string) {
				if *pictures--; *pictures >= 0 {
					if err := store.Put(hash, plainPicture(hash)); err != nil {
						log.Fatal(err)
					}
				}
			}
		}
		totals, err := seed.Import(ctx, d, cfg, func(t seed.Totals) {
			if time.Since(last) > 10*time.Second {
				last = time.Now()
				log.Printf("%d files, %.0f files/s, %d rows written", t.Files, t.FilesPerSec, t.RowsWritten)
			}
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("import: %d files in %s (%.0f files/s), %d new files, %d new items, %d rows written",
			totals.Files, totals.Elapsed.Round(time.Millisecond), totals.FilesPerSec, totals.FilesNew, totals.ItemsNew, totals.RowsWritten)
		report["import"] = totals
	}
	if *phase == "all" || *phase == "state" {
		if *state == 0 {
			*state = *items / 10
		}
		viewers, err := seed.Profiles(ctx, d, *profiles)
		if err != nil {
			log.Fatal(err)
		}
		start := time.Now()
		rows, err := seed.State(ctx, d, viewers, *state, *seedValue)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("state: %d rows for %d profiles in %s", rows, len(viewers), time.Since(start).Round(time.Millisecond))
		report["stateRows"] = rows
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// plainPicture is a poster-shaped JPEG in a colour taken from its name, darker toward the bottom.
func plainPicture(hash string) []byte {
	var tint [3]uint64
	for n := range tint {
		tint[n], _ = strconv.ParseUint(hash[n*2:n*2+2], 16, 8)
	}
	const width, height = 600, 900
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		shade := 255 - y*160/height
		for x := range width {
			picture.SetRGBA(x, y, color.RGBA{uint8(int(tint[0]) * shade / 255), uint8(int(tint[1]) * shade / 255), uint8(int(tint[2]) * shade / 255), 255})
		}
	}
	var out bytes.Buffer
	jpeg.Encode(&out, picture, nil)
	return out.Bytes()
}
