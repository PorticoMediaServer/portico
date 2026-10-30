package persistence

import (
	"path/filepath"
	"testing"
)

func TestArtworkSizeSweepSchemaIsIdleAndKeepsExactRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var running, cursorID int
	var kind, role, subject string
	if err := db.QueryRow(`SELECT running,cursor_kind,cursor_id,cursor_role,cursor_subject FROM metadata_artwork_size_sweep WHERE singleton=1`).Scan(&running, &kind, &cursorID, &role, &subject); err != nil {
		t.Fatal(err)
	}
	if running != 0 || cursorID != 0 || kind != "" || role != "" || subject != "" {
		t.Fatal("fresh size sweep is not idle")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM metadata_artwork_size_requests`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("fresh size requests: count=%d err=%v", count, err)
	}
	if _, err := db.Exec(`INSERT INTO metadata_artwork_size_requests(kind,entity_id,role,subject,candidate_id,digest,selection_revision,edge,next_attempt) VALUES('item',7,'poster','subject','candidate','digest',9,2560,'2026-10-23T01:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var candidate, digest, next string
	var revision, edge int
	if err := db.QueryRow(`SELECT candidate_id,digest,selection_revision,edge,next_attempt FROM metadata_artwork_size_requests WHERE kind='item' AND entity_id=7 AND role='poster' AND subject='subject'`).Scan(&candidate, &digest, &revision, &edge, &next); err != nil {
		t.Fatal(err)
	}
	if candidate != "candidate" || digest != "digest" || revision != 9 || edge != 2560 || next != "2026-10-23T01:00:00Z" {
		t.Fatal("size request changed its captured selection")
	}
	for _, value := range []int{1280, 1920, 2560} {
		if _, err := db.Exec(`UPDATE metadata_artwork_size_requests SET edge=?`, value); err != nil {
			t.Fatalf("rejected size edge %d: %v", value, err)
		}
	}
	for _, value := range []any{nil, 0, 1279, 1281, 1921, 2561, "automatic"} {
		if _, err := db.Exec(`UPDATE metadata_artwork_size_requests SET edge=?`, value); err == nil {
			t.Fatalf("accepted invalid size edge %v", value)
		}
	}
	if _, err := db.Exec(`INSERT INTO metadata_artwork_size_requests(kind,entity_id,role,subject,candidate_id,digest,selection_revision,edge) VALUES('item',7,'poster','subject','another','another',10,1920)`); err == nil {
		t.Fatal("duplicate request replaced its captured selection")
	}
	if _, err := db.Exec(`INSERT INTO artwork_jobs(id,kind,entity_id,role,created_at) VALUES('job','item',7,'poster','2026-10-23T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var refresh int
	if err := db.QueryRow(`SELECT size_refresh FROM artwork_jobs WHERE id='job'`).Scan(&refresh); err != nil || refresh != 0 {
		t.Fatalf("ordinary job became a size refresh: flag=%d err=%v", refresh, err)
	}
	if _, err := db.Exec(`UPDATE artwork_jobs SET size_refresh=1 WHERE id='job'`); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{nil, -1, 2, "automatic"} {
		if _, err := db.Exec(`UPDATE artwork_jobs SET size_refresh=? WHERE id='job'`, value); err == nil {
			t.Fatalf("accepted invalid refresh flag %v", value)
		}
		if _, err := db.Exec(`UPDATE metadata_artwork_size_sweep SET running=? WHERE singleton=1`, value); err == nil {
			t.Fatalf("accepted invalid sweep flag %v", value)
		}
	}
}
