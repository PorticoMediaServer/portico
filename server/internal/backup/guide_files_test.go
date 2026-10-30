package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

func TestBackupCarriesGuideImageACollectorMovedAside(t *testing.T) {
	s, state, db := testService(t)
	s.SetPolicyResolver(func(context.Context) (Snapshot, error) {
		return Snapshot{Settings: Settings{BackupFolder: s.backupRoot(), IncludeArtwork: true}}, nil
	})
	raw := []byte("immutable guide image fixture")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	name := "Metadata/guide-images/" + digest + ".png"
	if err := writeTestFile(filepath.Join(state, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO live_programme_images(url,small_digest,large_digest,media_type,small_width,small_height,small_bytes,large_width,large_height,large_bytes,stored_ms) VALUES('fixture',?,?,'image/png',1,1,?,1,1,?,0)`, digest, digest, len(raw), len(raw)); err != nil {
		t.Fatal(err)
	}
	s.SetCopyScrubber(func(context.Context, *dbwork.Candidate) error {
		return os.Rename(filepath.Join(state, name), filepath.Join(state, name+".tomb-test"))
	})
	id, err := s.Create(t.Context(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	if got := fileHashOrEmpty(t, filepath.Join(folder, name)); got != digest {
		t.Fatal("snapshot guide reference lost during collector rename")
	}
	if _, err = os.Stat(filepath.Join(folder, name+".tomb-test")); !os.IsNotExist(err) {
		t.Fatal("collector scratch became a backup payload", err)
	}
	m, err := readManifest(folder)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkSourceHashes(folder, m); err != nil {
		t.Fatal(err)
	}
}
