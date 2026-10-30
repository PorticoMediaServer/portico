package administration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func TestArtworkBytesSurviveCleanupUntilTheirLastTitleIsDeleted(t *testing.T) {
	s, db, root, item, _ := deletionFixture(t)
	ctx := context.Background()
	art := metadata.New(db, "") // No provider credentials or network dependency.
	directory := filepath.Join(root, "artwork")
	if err := art.SetArtworkDirectory(directory); err != nil {
		t.Fatal(err)
	}
	other := addCatalogTestEntity(t, db, "lib-1", compactcatalog.ItemKey(root, filepath.Join(root, "other.mkv"), 0), compactcatalog.Movie, "Other")
	itemEntityID := entityID(t, db, item)
	otherEntityID := entityID(t, db, other)
	names := []string{"shared", "thumbnail", "unique", "orphan"}
	digests := map[string]string{}
	for _, name := range names {
		raw := []byte("retained artwork " + name)
		digest := fmt.Sprintf("%x", sha256.Sum256(raw))
		digests[name] = digest
		if _, err := db.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',?,1,?,'2000-01-01T00:00:00Z')`, digest, len(raw), len(raw)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, digest+".img")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, time.Unix(0, 0), time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
	}
	// Each source's small slot is the shared thumbnail file; its large slot
	// and ambient effect are its own file.
	for _, name := range []string{"shared", "unique"} {
		if _, err := db.Exec(`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/jpeg',1,1,?,?,?,1920,0,0,1,'2000-01-01T00:00:00Z')`, digests[name], digests["thumbnail"], digests[name], digests[name]); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []struct {
		id, role, digest string
		entityID         int64
	}{
		{"art1", "poster", "shared", itemEntityID},
		{"art2", "poster", "shared", otherEntityID},
		{"art3", "backdrop", "unique", itemEntityID},
	} {
		if _, err := db.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,'item',?,?,'owner',?,'upload','','fence','2000-01-01')`, v.id, v.entityID, v.role, v.id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,candidate_id,digest,actor,observed_at) VALUES('item',?,?,?,?,'owner','2000-01-01')`, v.entityID, v.role, v.id, digests[v.digest]); err != nil {
			t.Fatal(err)
		}
	}
	read := func(owner, role, want string) {
		t.Helper()
		f, _, err := art.Artwork(ctx, owner, role)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		raw, err := io.ReadAll(f)
		if err != nil || string(raw) != "retained artwork "+want {
			t.Fatalf("artwork unavailable: %q %v", raw, err)
		}
	}
	sweep := func() {
		t.Helper()
		if err := art.CleanupArtwork(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sweep()
	read(item, "poster", "shared")
	read(item, "backdrop", "unique")
	if _, err := os.Stat(filepath.Join(directory, digests["orphan"]+".img")); !os.IsNotExist(err) {
		t.Fatalf("real orphan not collected: %v", err)
	}
	if _, err := s.Cleanup(ctx, allow, "artwork", CleanupRequest{Confirmation: "artwork", OperationID: "test-policy-key-1"}); err == nil {
		t.Fatal("generic age-based artwork cleanup accepted")
	}
	if _, err := deleteItem(t, s, item, "artwork-delete"); err != nil {
		t.Fatal(err)
	}
	var err error
	sweep()
	read(other, "poster", "shared")
	if _, err = os.Stat(filepath.Join(directory, digests["unique"]+".img")); !os.IsNotExist(err) {
		t.Fatalf("purged item's exclusive artwork retained: %v", err)
	}
	if _, err = deleteItem(t, s, other, "last-artwork-delete"); err != nil {
		t.Fatal(err)
	}
	sweep()
	for _, name := range []string{"shared", "thumbnail"} {
		if _, err = os.Stat(filepath.Join(directory, digests[name]+".img")); !os.IsNotExist(err) {
			t.Fatalf("unreferenced %s retained: %v", name, err)
		}
	}
}
