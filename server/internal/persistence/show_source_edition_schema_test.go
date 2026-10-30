package persistence

import (
	"path/filepath"
	"testing"
)

func TestShowSourceEditionIsPrivateTextWithLegacyEmptyDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	library := persistenceLibrary(t, db, "tv", "Shows", "tv", "/tv")
	for id := 1; id <= 2; id++ {
		if _, err := db.Exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(?,randomblob(16),?,2,'Show','show','s')`, id, library); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO catalog_shows(entity_id,local_key) VALUES(1,'legacy-show')`); err != nil {
		t.Fatal(err)
	}
	var edition string
	if err := db.QueryRow(`SELECT source_edition FROM catalog_shows WHERE entity_id=1`).Scan(&edition); err != nil || edition != "" {
		t.Fatalf("ordinary show default changed: %q, %v", edition, err)
	}
	const marker = "Bluray Remaster – Director's edition"
	if _, err := db.Exec(`INSERT INTO catalog_shows(entity_id,local_key,source_edition) VALUES(2,'legacy-show',?)`, marker); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT source_edition FROM catalog_shows WHERE entity_id=2`).Scan(&edition); err != nil || edition != marker {
		t.Fatalf("private edition fact was changed: %q, %v", edition, err)
	}
	if _, err := db.Exec(`UPDATE catalog_shows SET source_edition=NULL WHERE entity_id=2`); err == nil {
		t.Fatal("null edition scope was accepted")
	}
	if _, err := db.Exec(`UPDATE catalog_shows SET source_edition='' WHERE entity_id=2`); err != nil {
		t.Fatal(err)
	}
}
