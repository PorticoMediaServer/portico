package ingestion

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// This optional paired measurement also runs against the unmodified baseline.
// The ordinary retention-plan test is the unconditional zero-overrun guard.
func TestScanRetentionGrowthMeasurement(t *testing.T) {
	if os.Getenv("PORTICO_RETENTION_SCALE") == "" {
		t.Skip("hosted retention curves: set PORTICO_RETENTION_SCALE=1")
	}
	for _, size := range []int{10_000, 30_000, 100_000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := retentionGrowthFixture(t, size)
			for _, stage := range []string{"directories", "changes"} {
				dbwork.ResetPlans()
				ctx, cost := dbwork.Measure(context.Background())
				more, err := f.s.PruneSource(ctx, f.source)
				if err != nil || !more {
					t.Fatal("prune", more, err)
				}
				p := dbwork.Plans()
				t.Logf("retention size=%d stage=%s readVM=%d maxVM=%d rows=%d pages=%d overruns=%d batch=%d", size, stage, cost().VMSteps, p.Largest.VMOps, p.Largest.Rows, p.Largest.Pages, p.Overruns, scanRetentionBatch)
				// Fixture teardown exposes the next producer without spending a
				// thousand real pruning turns just to measure a second window.
				f.exec(`DELETE FROM inventory_directories WHERE job_id='retention-plan'`)
			}
		})
	}
}

func retentionGrowthFixture(t *testing.T, size int) *loopFixture {
	t.Helper()
	seedDir := os.Getenv("PORTICO_RETENTION_SEED_DIR")
	seed := filepath.Join(seedDir, fmt.Sprintf("prune-%d.sqlite", size))
	if seedDir != "" {
		if _, err := os.Stat(seed); err == nil {
			path := filepath.Join(t.TempDir(), "restored.sqlite")
			copyRetentionSeed(t, seed, path)
			db, err := persistence.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			f := &loopFixture{t: t, db: db, s: New(db, catalog.New(db), assets.Probe{})}
			if err := db.QueryRow(`SELECT r.source_id,j.library_id FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.job_id='retention-plan'`).Scan(&f.source, &f.library); err != nil {
				t.Fatal(err)
			}
			// Pruning does no filesystem I/O. The cached root's physical
			// location is deliberately irrelevant to this write-only fixture.
			return f
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	f := newLoopFixture(t, "movie")
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	f.exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('retention-plan',?,'complete',?)`, f.library, old)
	f.exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,finished_at) SELECT 'retention-plan',id,generation,incarnation,root_identity,1,'complete',? FROM library_sources WHERE id=?`, old, f.source)
	f.exec(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO inventory_directories(job_id,relative_path,state) SELECT 'retention-plan','old-'||x,'done' FROM n`, size)
	f.exec(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO inventory_changes(source_id,job_id,object_id,kind,created_at) SELECT ?,'retention-plan','old-'||x,'observed',? FROM n`, size, f.source, time.Now().Add(-40*24*time.Hour).UTC().Format(time.RFC3339Nano))
	if seedDir != "" {
		if err := os.MkdirAll(seedDir, 0700); err != nil {
			t.Fatal(err)
		}
		var busy, pages, done int
		if err := f.db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &pages, &done); err != nil || busy != 0 {
			t.Fatal("seed checkpoint", busy, err)
		}
		var sequence int
		var name, path string
		if err := f.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
			t.Fatal(err)
		}
		copyRetentionSeed(t, path, seed)
	}
	return f
}

func copyRetentionSeed(t *testing.T, source, target string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	closed := out.Close()
	if err != nil || closed != nil {
		t.Fatal("seed copy", err, closed)
	}
}
