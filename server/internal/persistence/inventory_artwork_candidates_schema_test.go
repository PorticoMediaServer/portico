package persistence

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestInventoryArtworkCandidatesKeepSeparateObservationEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	library := persistenceLibrary(t, db, "art", "Art", "movie", "/art")
	for id := 1; id <= 3; id++ {
		exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(?,randomblob(16),?,?,'Title','title','t')`, id, library, id)
		exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(?,?,?,1,1,'mp4','h264','aac',1,1,1)`, id, fmt.Sprintf("asset-%d", id), fmt.Sprintf("/art/%d.mp4", id))
		exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES(?,'art',?,'root',?,'opaque:revision','{}',1,1)`, fmt.Sprintf("object-%d", id), fmt.Sprintf("asset-%d", id), fmt.Sprintf("%d.mp4", id))
	}
	exec(`INSERT INTO local_artwork(library_id,kind,entity_id,role,art_key,observed_at) VALUES('art','item',1,'poster','primary-key','2026-10-24T00:00:00Z')`)
	const insert = `INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key) VALUES(?,'opaque:revision',?,?,?,'poster',?)`
	for _, key := range []string{"decoded-key-a", "decoded-key-b"} {
		exec(insert, "object-1", "asset-1", "item", 1, key)
	}
	for id := 2; id <= 3; id++ {
		exec(insert, fmt.Sprintf("object-%d", id), fmt.Sprintf("asset-%d", id), []string{"", "item", "show", "season"}[id], id, "decoded-key-a")
	}
	if _, err := db.Exec(insert, "object-1", "asset-1", "item", 1, "decoded-key-a"); err == nil {
		t.Fatal("duplicate candidate replaced captured evidence")
	}
	for _, change := range []string{
		`object_id='missing'`, `asset_id='missing'`, `entity_id=999`,
		`asset_id=NULL`, `source_revision=NULL`, `source_revision=''`,
		`kind='artist'`, `kind='other'`, `role='portrait'`, `role='still'`,
		`art_key=NULL`, `art_key=''`,
	} {
		if _, err := db.Exec(`UPDATE inventory_artwork_candidates SET ` + change + ` WHERE object_id='object-1' AND art_key='decoded-key-b'`); err == nil {
			t.Fatalf("accepted invalid candidate: %s", change)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM inventory_artwork_candidates WHERE entity_id=1 AND kind='item' AND role='poster'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("numbered candidates collapsed: count=%d err=%v", count, err)
	}
	var key string
	if err := db.QueryRow(`SELECT art_key FROM local_artwork WHERE library_id='art' AND entity_id=1 AND kind='item' AND role='poster'`).Scan(&key); err != nil || key != "primary-key" {
		t.Fatalf("candidate evidence changed primary local art: %q, %v", key, err)
	}
	// Retiring each source authority cleans its candidates independently.
	exec(`DELETE FROM inventory_objects WHERE id='object-1'`)
	// An object can advance while its older captured asset evidence awaits cleanup.
	exec(`UPDATE inventory_objects SET asset_id='asset-1',revision='replacement' WHERE id='object-2'`)
	exec(`DELETE FROM catalog_assets WHERE token='asset-2'`)
	exec(`DELETE FROM catalog_entities WHERE id=3`)
	if err := db.QueryRow(`SELECT count(*) FROM inventory_artwork_candidates`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("candidate cascade left evidence: count=%d err=%v", count, err)
	}
}
