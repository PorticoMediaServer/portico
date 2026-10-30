package dbwork

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// warmBursts is enough repetitions that a pool which closes surplus idle
// handles would have to reopen the same connections many times.
const warmBursts = 20

// TestReadPoolHotLoopStaysWarm is the guard for the read pool's idle policy.
// It reproduces the quiet-cycle pattern: a burst holds every connection the
// policy allows, runs the same parameterized SELECT on each, and releases them
// all. The published pool and statement-cache counters are the guard rates:
// physical connections reopened per burst show up as MaxIdleClosed and
// MaxLifetimeClosed, and bytecode prepared per request shows up as
// statement-cache misses. After the first burst both must stay at zero, because
// the pool retained every connection it was allowed to keep.
func TestReadPoolHotLoopStaysWarm(t *testing.T) {
	policy := DefaultPolicy()
	if policy.MaxIdleConns != policy.MaxOpenConns {
		t.Errorf("read pool keeps %d of %d allowed connections warm: a smaller idle set discards page caches and prepared bytecode between bursts",
			policy.MaxIdleConns, policy.MaxOpenConns)
	}

	db, err := OpenHandle(filepath.Join(t.TempDir(), "warm.sqlite"), policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE warm(id INTEGER PRIMARY KEY, label TEXT NOT NULL)`,
		`INSERT INTO warm(id, label) VALUES(1, 'one'), (2, 'two')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	const query = `SELECT label FROM warm WHERE id = ?`
	// Baseline after schema setup: only this test's SELECT may move the deltas
	// from here on.
	previous := Pool(db)
	cache := Reads().StatementCache
	misses, bypasses, hits := cache.Misses, cache.Bypasses, cache.Hits

	var closed int64
	var reprepared uint64
	for burst := 0; burst < warmBursts; burst++ {
		// Hold every permitted handle at once, then issue the same statement on
		// each. No goroutines: the burst is the set of handles being in use.
		conns := make([]*sql.Conn, 0, policy.MaxOpenConns)
		for index := 0; index < policy.MaxOpenConns; index++ {
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatalf("burst %d: acquiring connection %d: %v", burst, index, err)
			}
			conns = append(conns, conn)
			t.Cleanup(func() { _ = conn.Close() })
		}
		for index, conn := range conns {
			var label string
			if err := conn.QueryRowContext(ctx, query, int64(index%2+1)).Scan(&label); err != nil {
				t.Fatalf("burst %d: connection %d: %v", burst, index, err)
			}
			if want := []string{"one", "two"}[index%2]; label != want {
				t.Fatalf("burst %d: connection %d read %q, want %q", burst, index, label, want)
			}
		}
		for index, conn := range conns {
			if err := conn.Close(); err != nil {
				t.Fatalf("burst %d: returning connection %d: %v", burst, index, err)
			}
		}

		stats := Pool(db)
		closed += stats.MaxIdleClosed - previous.MaxIdleClosed
		closed += stats.MaxLifetimeClosed - previous.MaxLifetimeClosed
		previous = stats

		cache = Reads().StatementCache
		if burst == 0 {
			// One physical connection per cache: the statement is prepared once
			// on each, which is what proves the cache is per connection.
			if prepared := cache.Misses - misses; prepared != uint64(policy.MaxOpenConns) {
				t.Fatalf("first burst prepared the SELECT %d times, want %d (one per connection)", prepared, policy.MaxOpenConns)
			}
		} else {
			reprepared += cache.Misses - misses
		}
		if cache.Bypasses != bypasses {
			t.Fatalf("burst %d bypassed the statement cache %d times", burst, cache.Bypasses-bypasses)
		}
		misses = cache.Misses
	}

	t.Logf("warm bursts=%d connection closures=%d prepares after first burst=%d", warmBursts, closed, reprepared)
	settled := Pool(db)
	if closed != 0 {
		t.Errorf("%d physical connections were closed over %d warm bursts; want 0", closed, warmBursts)
	}
	if reprepared != 0 {
		t.Errorf("%d SELECTs were re-prepared over %d warm bursts; want 0 prepares per request after the initial warm", reprepared, warmBursts)
	}
	if settled.Idle != policy.MaxIdleConns || settled.OpenConnections != policy.MaxOpenConns {
		t.Errorf("pool settled at %d idle of %d open; want %d idle of %d",
			settled.Idle, settled.OpenConnections, policy.MaxIdleConns, policy.MaxOpenConns)
	}
	if Reads().StatementCache.Hits == hits {
		t.Error("warm bursts never reused cached bytecode")
	}
}
