package telemetry

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// openProfiledTelemetryDatabase uses the server's observed driver, because the
// statement profile sees only statements issued through it.
func openProfiledTelemetryDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestTelemetryPersistsOneBoundedSampleWithoutPerMetricStatements(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := openProfiledTelemetryDatabase(t)
	clock := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	c := New(Options{Sampler: &fakeSampler{gpu: true}, DB: db, Now: func() time.Time { return clock }})
	ctx := context.Background()
	for range 3 {
		c.Collect(ctx)
		clock = clock.Add(2 * time.Second)
	}
	clock = clock.Add(time.Minute)
	// Keep the real completed sample pending, then observe its actual SQL.
	c.lastPersist = clock.UnixMilli()
	c.Collect(ctx)
	dbwork.ProfileStatements(true)
	c.persist(ctx, true)
	profile := dbwork.StatementProfile()
	dbwork.ProfileStatements(false)
	var inserts int64
	for _, entry := range profile {
		if strings.HasPrefix(entry.Shape, "INSERT INTO telemetry_rollups") {
			inserts += entry.Count
		}
	}
	if inserts != 1 {
		t.Fatal("sample persisted per metric", inserts)
	}
	var rows, samples int
	var total float64
	if err := db.QueryRow(`SELECT count(*) FROM telemetry_rollups`).Scan(&rows); err != nil || rows < 4 {
		t.Fatal("lost known metric cells", rows, err)
	}
	if err := db.QueryRow(`SELECT samples,total FROM telemetry_rollups WHERE metric='cpu'`).Scan(&samples, &total); err != nil || samples != 3 || total != 6 {
		t.Fatal("lost actual sample weights", samples, total, err)
	}
	c.persist(ctx, true)
	var again int
	if err := db.QueryRow(`SELECT samples FROM telemetry_rollups WHERE metric='cpu'`).Scan(&again); err != nil || again != samples {
		t.Fatal("replayed completed sample", again, err)
	}
}
