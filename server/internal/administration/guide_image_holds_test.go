package administration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestGuideCollectorRechecksBackupHoldAfterRename(t *testing.T) {
	state := t.TempDir()
	db, err := persistence.Open(filepath.Join(state, "Database/server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewAt(db, state)
	root, err := service.guideImageRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("immutable guide bytes")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	name := digest + ".png"
	if err = os.WriteFile(filepath.Join(root, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
	err = service.retireGuideImage(context.Background(), root, name, digest, func() error {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("file was not marked before the race")
		}
		_, err := db.Exec(`INSERT INTO backup_blob_holds(singleton,backup_id,process_epoch,scratch_token,created_ms,state) VALUES(1,'backup','epoch','scratch',0,'copying')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(got) != string(raw) {
		t.Fatal("mid-pass hold lost its file", err)
	}
	if _, err = db.Exec(`DELETE FROM backup_blob_holds`); err != nil {
		t.Fatal(err)
	}
	if err = service.retireGuideImage(t.Context(), root, name, digest, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatal("unheld orphan was not collected", err)
	}
}

func TestGuideCollectorHoldProtectsRowsAndInterruptedTomb(t *testing.T) {
	state := t.TempDir()
	db, err := persistence.Open(filepath.Join(state, "Database/server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewAt(db, state)
	root, err := s.guideImageRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("guide tomb")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	name := digest + ".png"
	tomb := name + guideImageTombMarker + "interrupted"
	if err = os.WriteFile(filepath.Join(root, tomb), raw, 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err = os.Chtimes(filepath.Join(root, tomb), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO live_programme_images(url,small_digest,large_digest,media_type,small_width,small_height,small_bytes,large_width,large_height,large_bytes,stored_ms) VALUES('orphan',?,?,'image/png',1,1,1,1,1,1,0)`, digest, digest); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO backup_blob_holds(singleton,backup_id,process_epoch,scratch_token,created_ms,state) VALUES(1,'backup','epoch','scratch',0,'files')`); err != nil {
		t.Fatal(err)
	}
	s.cleanupGuideImages(t.Context())
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM live_programme_images WHERE url='orphan'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("hold lost database image row", count, err)
	}
	if _, err = os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatal("held interrupted tomb did not restore", err)
	}
}
