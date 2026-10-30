package reading

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The catalogue rows here are real: one title, its publication and the owner
// fields are written through the ordinary write API. Only the Series pointer is
// synthetic, standing in for a caller's row buffer reused across snapshots. A
// title without accepted facts must clear that pointer, while accepted owner
// values stay projected.
func TestReadingAttributionClearsStaleSeriesAndKeepsOwnerValues(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	ctx := context.Background()
	root := t.TempDir()
	library := c.Library("reading", "Reading", "book", root)
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Ebook, Key: "reading:ebook:dune", Title: "Dune", Added: "2026-10-23T00:00:00Z"}, nil)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: filepath.Join(root, "Dune.epub"), Size: 1, ModifiedNS: 1, Container: "epub"})
		if err != nil {
			return err
		}
		if err = compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{}); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'epub')`, item.ID, asset)
		return err
	})
	c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'author','Owner Author','',1,'person:test',datetime('now'))`, item.ID)
	project := func(rows []LibraryRow) []LibraryRow {
		t.Helper()
		g, err := dbwork.BeginSnapshot(ctx, c.DB)
		if err != nil {
			t.Fatal(err)
		}
		defer g.Rollback()
		if err = readingAttributionProjectionTx(ctx, g.Tx(), rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	// A second projection over the same slice must not keep the previous
	// snapshot's series when this one has no accepted series.
	rows := []LibraryRow{{
		ID: "title", LibraryID: "reading", Title: "Dune", Format: "epub",
		Author: &compactcatalog.ReadingValue{Value: "Stale author", Source: compactcatalog.ReadingFolder},
		Series: &SeriesValue{ID: "stale-series", Name: "Stale series", Source: compactcatalog.ReadingFolder},
		entity: item.ID,
	}}
	rows = project(rows)
	if rows[0].Series != nil {
		t.Fatalf("stale automatic series survived: %+v", rows[0].Series)
	}
	if rows[0].Author == nil || rows[0].Author.Value != "Owner Author" || rows[0].Author.Source != compactcatalog.ReadingOwner {
		t.Fatalf("owner author did not persist: %+v", rows[0].Author)
	}
	c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'series','Dune Saga','',1,'person:test',datetime('now'))`, item.ID)
	rows = project(rows)
	if rows[0].Series == nil || rows[0].Series.Name != "Dune Saga" || rows[0].Series.Source != compactcatalog.ReadingOwner || rows[0].Series.ID != compactcatalog.ReadingSeriesID("reading", "Dune Saga") {
		t.Fatalf("owner series did not replace the stale pointer: %+v", rows[0].Series)
	}
	if rows[0].Author == nil || rows[0].Author.Value != "Owner Author" {
		t.Fatalf("owner author lost on the second projection: %+v", rows[0].Author)
	}
}
