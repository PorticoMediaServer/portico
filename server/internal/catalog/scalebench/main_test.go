package main

import (
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestSeedResumesAtCommittedBatchBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, _, err = seed(db, 1000, 0, 64); err != nil {
		t.Fatal(err)
	}
	if _, _, err = seed(db, 1000, 64, 96); err != nil {
		t.Fatal(err)
	}
	var items, assets, credits int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM catalog_assets`).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM catalog_credits`).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if items != 96 || assets != 96 || credits != 96*6 {
		t.Fatalf("resumed seed split item or child rows: items=%d assets=%d credits=%d", items, assets, credits)
	}
}
