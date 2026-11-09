// Command portico-seed fills a database with a synthetic library, through the same import a
// scan uses. Run it again on the same database and it is an unchanged rescan.
//
//	portico-seed -db /tmp/portico/main.db -items 20000
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

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
		totals, err := seed.Import(ctx, d, seed.Config{Seed: *seedValue, Items: *items, BatchFiles: *batch}, func(t seed.Totals) {
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
		start, rows := time.Now(), 0
		for _, v := range viewers {
			n, err := seed.State(ctx, d, v, *state, *seedValue)
			if err != nil {
				log.Fatal(err)
			}
			rows += n
		}
		log.Printf("state: %d rows for %d profiles in %s", rows, len(viewers), time.Since(start).Round(time.Millisecond))
		report["stateRows"] = rows
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
