package metadata

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// TestArtworkCompactionStepScale is an opt-in measurement (BE-SRV-14): with N
// published sources already at the current policy, one idle compaction step's
// keyed read of the large-edge index.
//
//	PORTICO_ARTWORK_SCALE=1000000 go test ./internal/metadata -run TestArtworkCompactionStepScale -count=1 -v
func TestArtworkCompactionStepScale(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_ARTWORK_SCALE"))
	if n <= 0 {
		t.Skip("set PORTICO_ARTWORK_SCALE to the source count")
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "art.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	start := time.Now()
	if _, err = db.Exec(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<?)
	 INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) SELECT printf('%064x',i),'image/jpeg',800,1200,120000+(i%300000),'2026-01-01T00:00:00Z' FROM n`, n-1); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,'image/jpeg',800,1200,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d sources in %s", n, time.Since(start).Round(time.Millisecond))
	s := New(db, "")
	s.cacheRoot = t.TempDir()
	best := time.Duration(1 << 62)
	for k := 0; k < 5; k++ {
		s.compactionEdge = 0
		began := time.Now()
		if err := s.CompactArtworkStep(context.Background()); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(began))
	}
	t.Logf("compaction step (large-edge index, nothing to do): best of 5 %s", best.Round(10*time.Microsecond))
}
