package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestRebuildAcknowledgesQueuedSourcePrefixes(t *testing.T) {
	sizes := []int{256}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{10000, 200000}
	case "deep":
		sizes = []int{10000, 200000, 2000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db := cc1OpenDB(t, "prefix.sqlite")
			const domain = 96
			counts := make([]uint8, n+1)
			Register(domain, Derivation{Version: 1, Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
				return scanIDs(ctx, tx, `SELECT id FROM capacity_sources WHERE id>? ORDER BY id LIMIT ?`, after, limit)
			}, Drain: func(_ context.Context, _ *sql.Tx, keys []Key, _ int) ([]int64, error) {
				for _, key := range keys {
					counts[key.ID]++
				}
				return keyIDs(keys), nil
			}})
			t.Cleanup(func() { registry.Lock(); delete(registry.domains, domain); registry.Unlock() })
			cc1Exec(t, db, `CREATE TABLE capacity_sources(id INTEGER PRIMARY KEY)`)
			cc1Exec(t, db, `INSERT INTO catalog_derivations(domain,version,rebuilding,first_build) VALUES(?,1,1,1)`, domain)
			for after := 0; after < n; after += 10000 {
				cc1Exec(t, db, `WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO capacity_sources SELECT i FROM ids`, after+1, min(n, after+10000))
			}
			cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) SELECT ?,id,1 FROM capacity_sources`, domain)
			w := NewWorker(db)
			start := time.Now()
			for batch := 0; batch < n/500+10; batch++ {
				rebuilding := cc1Scalar(t, db, `SELECT rebuilding FROM catalog_derivations WHERE domain=?`, domain)
				if rebuilding == 0 {
					break
				}
				cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
					_, _, _, err := w.drainDomain(ctx, tx, domain, rebuilding, 500, false)
					return err
				})
			}
			if cc1Scalar(t, db, `SELECT rebuilding FROM catalog_derivations WHERE domain=?`, domain) != 0 {
				t.Fatal("rebuild did not finish")
			}
			for id := 1; id <= n; id++ {
				if counts[id] != 1 {
					t.Fatalf("source %d derived %d times", id, counts[id])
				}
			}
			if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, domain) != 0 {
				t.Fatal("queue did not drain")
			}
			t.Logf("sources=%d derived exactly once in %s", n, time.Since(start))
		})
	}
}

func TestQueuedPrefixDoesNotSkipSourceGapsOrUnfinishedFanouts(t *testing.T) {
	db := cc1OpenDB(t, "gaps.sqlite")
	const domain = 96
	counts := map[int64]int{}
	Register(domain, Derivation{Version: 1, Backfill: func(_ context.Context, _ *sql.Tx, after int64, limit int) ([]int64, error) {
		ids := []int64{}
		for id := after + 1; id <= 32 && len(ids) < limit; id++ {
			ids = append(ids, id)
		}
		return ids, nil
	}, Drain: func(_ context.Context, _ *sql.Tx, keys []Key, _ int) ([]int64, error) {
		done := []int64{}
		for _, key := range keys {
			counts[key.ID]++
			if key.ID != 3 || counts[key.ID] > 1 {
				done = append(done, key.ID)
			}
		}
		return done, nil
	}})
	t.Cleanup(func() { registry.Lock(); delete(registry.domains, domain); registry.Unlock() })
	cc1Exec(t, db, `INSERT INTO catalog_derivations(domain,version,rebuilding,first_build) VALUES(?,1,1,1)`, domain)
	cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,1,1),(?,3,1),(?,4,1),(?,32,1)`, domain, domain, domain, domain)
	w := NewWorker(db)
	for batch := 0; batch < 40; batch++ {
		rebuilding := cc1Scalar(t, db, `SELECT rebuilding FROM catalog_derivations WHERE domain=?`, domain)
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, _, _, err := w.drainDomain(ctx, tx, domain, rebuilding, 4, false)
			return err
		})
	}
	for id := int64(1); id <= 32; id++ {
		if counts[id] == 0 {
			t.Fatalf("source gap %d was skipped", id)
		}
	}
	if counts[3] < 2 || cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, domain) != 0 {
		t.Fatal("unfinished fanout was acknowledged early")
	}
}
