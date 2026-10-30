package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Copy only this harness's synthetic source facts into a fresh baseline. This
// is fixture preparation, never an application database upgrade or auth copy.
func copyReferenceFacts(db *sql.DB, path string) (float64, float64, error) {
	var err error
	path, err = filepath.Abs(path)
	if err != nil {
		return 0, 0, err
	}
	if !strings.Contains(path, "/_program/scratch/scale-check-2/.scratch/") {
		return 0, 0, fmt.Errorf("refusing a non-fixture source")
	}
	usage, e := scratchUsage()
	if e != nil {
		return 0, 0, e
	}
	info, e := os.Stat(path)
	if e != nil {
		return 0, 0, e
	}
	if usage+info.Size()*2 >= scratchStop {
		return 0, 0, fmt.Errorf("copy would exceed scratch headroom: current=%d source=%d", usage, info.Size())
	}
	source := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	ctx := context.Background()
	return 0, 0, dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `ATTACH DATABASE ? AS source_facts`, source.String()); err != nil {
			return err
		}
		tables := []string{"libraries", "catalog_attribute_fields", "catalog_attribute_terms", "catalog_credit_labels", "catalog_people", "catalog_terms", "catalog_entities", "catalog_item_details", "catalog_shows", "catalog_seasons", "catalog_artists", "catalog_albums", "catalog_songs", "catalog_episodes", "catalog_song_artists", "catalog_assets", "catalog_asset_links", "catalog_item_attribute_edges", "catalog_credits", "catalog_entity_terms", "catalog_term_sources", "progress", "progress_activity", "catalog_identities"}
		for _, table := range tables {
			start := time.Now()
			operation := "INSERT INTO "
			if table == "catalog_attribute_fields" {
				operation = "INSERT OR IGNORE INTO "
			}
			if _, err := tx.ExecContext(ctx, operation+`main.`+table+` SELECT * FROM source_facts.`+table); err != nil {
				return fmt.Errorf("copy %s: %w", table, err)
			}
			fmt.Printf("copy facts %s in %s\n", table, time.Since(start).Round(time.Millisecond))
		}
		return nil
	})
}
