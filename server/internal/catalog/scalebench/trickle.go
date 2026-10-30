package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// exerciseTrickle measures steady-state derivation the way provider metadata
// arrives in a large library: one title's details at a time, each in its own
// transaction, the worker drained after each. It reports the cost per update
// by kind; the cost must not grow with the library or the title's group.
func exerciseTrickle(db *sql.DB, updates int) error {
	ctx := context.Background()
	type target struct {
		name  string
		query string
		write func(ctx context.Context, tx *sql.Tx, id int64, i int) error
	}
	targets := []target{
		{"movie-details", `SELECT id FROM catalog_entities WHERE kind=1 ORDER BY id LIMIT 1 OFFSET ?`, func(ctx context.Context, tx *sql.Tx, id int64, i int) error {
			if err := compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"overview": fmt.Sprint("Overview ", i), "tagline": fmt.Sprint("Tagline ", i)}); err != nil {
				return err
			}
			return compactcatalog.SetAttributesTx(ctx, tx, id, "studio", []string{fmt.Sprint("Studio ", i%50)})
		}},
		{"episode-details", `SELECT id FROM catalog_entities WHERE kind=4 ORDER BY id LIMIT 1 OFFSET ?`, func(ctx context.Context, tx *sql.Tx, id int64, i int) error {
			if err := compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"overview": fmt.Sprint("Overview ", i)}); err != nil {
				return err
			}
			return compactcatalog.SetAttributesTx(ctx, tx, id, "studio", []string{fmt.Sprint("Studio ", i%50)})
		}},
		{"show-details", `SELECT id FROM catalog_entities WHERE kind=2 ORDER BY id LIMIT 1 OFFSET ?`, func(ctx context.Context, tx *sql.Tx, id int64, i int) error {
			if err := compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"overview": fmt.Sprint("Overview ", i), "studio": fmt.Sprint("Studio ", i%50)}); err != nil {
				return err
			}
			return compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, "fixture", []compactcatalog.Term{{SourceID: fmt.Sprint(i % 20), Name: fmt.Sprint("Genre ", i%20)}})
		}},
		{"album-details", `SELECT id FROM catalog_entities WHERE kind=6 ORDER BY id LIMIT 1 OFFSET ?`, func(ctx context.Context, tx *sql.Tx, id int64, i int) error {
			return compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"overview": fmt.Sprint("Overview ", i), "label": fmt.Sprint("Label ", i%50)})
		}},
		{"artist-details", `SELECT id FROM catalog_entities WHERE kind=5 ORDER BY id LIMIT 1 OFFSET ?`, func(ctx context.Context, tx *sql.Tx, id int64, i int) error {
			return compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"overview": fmt.Sprint("Overview ", i)})
		}},
	}
	w := compactcatalog.NewWorker(db)
	w.PaceToHold(compactcatalog.BatchHoldTarget)
	for _, tg := range targets {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE kind=(SELECT kind FROM catalog_entities WHERE id=(`+tg.query+`))`, 0).Scan(&count); err != nil || count == 0 {
			continue
		}
		var durations []float64
		var units, statements int
		var maxHold uint64
		for i := 0; i < updates; i++ {
			var id int64
			// The first entity of a kind is the largest group in the fixture
			// (its first show, album and artist); the rest spread evenly.
			offset := 0
			if i > 0 {
				offset = (i * 7919) % count
			}
			if err := db.QueryRow(tg.query, offset).Scan(&id); err != nil {
				return err
			}
			dbwork.WriteGate().ResetPeak()
			dbwork.ProfileStatements(true)
			before := dbwork.Reads().Statements
			start := time.Now()
			if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return tg.write(ctx, tx, id, i) }); err != nil {
				return err
			}
			for {
				n, err := w.Step(ctx, compactcatalog.BatchMax)
				if err != nil {
					return err
				}
				if n == 0 {
					break
				}
				units += n
			}
			durations = append(durations, float64(time.Since(start))/float64(time.Millisecond))
			if took := time.Since(start); took > 40*time.Millisecond {
				fmt.Fprintf(os.Stderr, "TRICKLE_SLOW %s id=%d ms=%d\n", tg.name, id, took.Milliseconds())
				for i, entry := range dbwork.StatementProfile() {
					if i == 4 {
						break
					}
					fmt.Fprintf(os.Stderr, "  %8.1f ms %5d× %.200s\n", entry.Millis(), entry.Count, entry.Shape)
				}
			}
			statements += int(dbwork.Reads().Statements - before)
			maxHold = max(maxHold, dbwork.WriteGate().Stats().MaxHeldMilli)
		}
		dbwork.ProfileStatements(false)
		sort.Float64s(durations)
		fmt.Fprintf(os.Stderr, "TRICKLE %s updates=%d p50_ms=%.2f p95_ms=%.2f max_ms=%.2f units_per_update=%.1f statements_per_update=%.1f max_gate_held_ms=%d\n",
			tg.name, len(durations), durations[len(durations)/2], durations[len(durations)*95/100], durations[len(durations)-1], float64(units)/float64(len(durations)), float64(statements)/float64(len(durations)), maxHold)
	}
	return nil
}
