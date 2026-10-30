package persistence

import (
	"fmt"
	"path/filepath"
	"testing"
)

// A source names exactly its small, large and ambient files, all of which
// must exist; its large edge and every file's MIME stay inside the approved
// sets, and a file a source names cannot be deleted under it.
func TestArtworkSourcesNameTheirSlotFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blob := func(n int) string { return fmt.Sprintf("%064x", n) }
	for n := 1; n <= 3; n++ {
		if _, err := db.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',800,1200,10000,'2026-10-28T00:00:00Z')`, blob(n)); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(source, small string, edge any, mime string) error {
		_, err := db.Exec(`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,?,2000,3000,?,?,?,?,0,0,1,'2026-10-28T00:00:00Z')`, source, mime, small, blob(2), blob(3), edge)
		return err
	}
	source := blob(100)
	for _, edge := range []any{1280, 1920, 2560} {
		if err := insert(fmt.Sprintf("%064x", edge), blob(1), edge, "image/webp"); err != nil {
			t.Fatalf("rejected large edge %v: %v", edge, err)
		}
	}
	for _, bad := range []struct {
		source, small string
		edge          any
		mime          string
	}{
		{source, blob(9), 1920, "image/jpeg"},         // missing small file
		{source, blob(1), 800, "image/jpeg"},          // not a large edge
		{source, blob(1), "automatic", "image/jpeg"},  // not an edge at all
		{source, blob(1), 1920, "image/gif"},          // not an accepted input
		{"NOT-A-DIGEST", blob(1), 1920, "image/jpeg"}, // not a SHA-256
		{source[:63], blob(1), 1920, "image/jpeg"},    // short digest
	} {
		if err := insert(bad.source, bad.small, bad.edge, bad.mime); err == nil {
			t.Fatalf("accepted invalid source %+v", bad)
		}
	}
	if _, err := db.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/webp',1,1,1,'now')`, blob(50)); err == nil {
		t.Fatal("accepted a WebP output file")
	}
	if _, err := db.Exec(`DELETE FROM artwork_blobs WHERE digest=?`, blob(1)); err == nil {
		t.Fatal("deleted a file a source names")
	}
	if _, err := db.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) VALUES('item',1,'poster','','missing',?,0,1,'','now')`, blob(99)); err == nil {
		t.Fatal("selected a source that is not published")
	}
}
