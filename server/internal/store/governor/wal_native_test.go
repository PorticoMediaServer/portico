package governor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestNativeEmptyWALFirstGovernedWrite(t *testing.T) {
	for _, scenario := range []string{"fresh", "clean-reopen", "truncate-reopen", "header-only"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "empty.sqlite")
			cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
			db, settings, err := sqliteconn.Open(t.Context(), path, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if settings.Engine != "3.53.4" || settings.TempStore != 1 {
				t.Fatal(settings)
			}
			var savedHeader []byte
			if scenario != "fresh" {
				if _, err = db.ExecContext(t.Context(), "CREATE TABLE fixture(id INTEGER PRIMARY KEY, v TEXT); INSERT INTO fixture VALUES(1,'before')"); err != nil {
					t.Fatal(err)
				}
				if scenario == "header-only" {
					b, err := os.ReadFile(path + "-wal")
					if err != nil {
						t.Fatal(err)
					}
					savedHeader = append([]byte(nil), b[:32]...)
				}
				if scenario == "truncate-reopen" {
					cpCfg := cfg
					cpCfg.Role = sqliteconn.Checkpointer
					cp, _, err := sqliteconn.Open(t.Context(), path, cpCfg)
					if err != nil {
						t.Fatal(err)
					}
					var b, n, k int
					if err = cp.QueryRowContext(t.Context(), "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&b, &n, &k); err != nil || b != 0 {
						t.Fatal(b, n, k, err)
					}
					cp.Close()
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
				if savedHeader != nil {
					if err = os.WriteFile(path+"-wal", savedHeader, 0600); err != nil {
						t.Fatal(err)
					}
				}
				db, settings, err = sqliteconn.Open(t.Context(), path, cfg)
				if err != nil {
					t.Fatal(err)
				}
			}
			defer db.Close()
			conn, err := db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err = sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
				t.Fatal(err)
			}
			if scenario != "fresh" {
				var v string
				if err = conn.QueryRowContext(t.Context(), "SELECT v FROM fixture WHERE id=1").Scan(&v); err != nil || v != "before" {
					t.Fatal(v, err)
				}
			}
			if scenario == "fresh" {
				// Read-only startup establishes SQLite's zero-frame WAL/index;
				// absence is still not treated as an empty observation.
				var count int
				if err = conn.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&count); err != nil {
					t.Fatal(err)
				}
			}
			empty, err := ReadWAL(path)
			if err != nil || empty.Frames != 0 || empty.Backfill != 0 || empty.ActiveBytes != 0 || empty.UncheckpointedBytes != 0 || empty.PageSize != 4096 || (empty.PhysicalBytes != 0 && empty.PhysicalBytes != 32) {
				t.Fatal(empty, err)
			}
			if scenario == "header-only" {
				main, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				index, err := os.ReadFile(path + "-shm")
				if err != nil {
					t.Fatal(err)
				}
				for _, offset := range []int{0, 4, 8, 16, 24} {
					bad := filepath.Join(t.TempDir(), "malformed.sqlite")
					header := append([]byte(nil), savedHeader...)
					header[offset] ^= 0xff
					for name, data := range map[string][]byte{bad: main, bad + "-shm": index, bad + "-wal": header} {
						if err = os.WriteFile(name, data, 0600); err != nil {
							t.Fatal(err)
						}
					}
					if _, err = ReadWAL(bad); err == nil {
						t.Fatal("malformed empty header accepted", offset)
					}
				}
			}
			g, a, clock := setup(t)
			w := gate(t)
			g.(*ledger).config.WAL = w
			g.(*ledger).config.Partitions[Foreground][WALBytes] = 1 << 20
			publish(t, w, empty, clock.now())
			r := request(clock, Foreground)
			r.Writer = true
			r.MaxHold = 5 * time.Millisecond
			r.Budget = UnitBudget{1, 100, 8}
			r.Need[WALBytes] = 32 << 10
			p := grant(t, g, r)
			if err = a.Check(p, r); err != nil {
				t.Fatal(err)
			}
			if scenario == "fresh" {
				_, err = conn.ExecContext(t.Context(), "CREATE TABLE fixture(id INTEGER PRIMARY KEY, v TEXT)")
			} else {
				_, err = conn.ExecContext(t.Context(), "UPDATE fixture SET v='governed' WHERE id=1")
			}
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			retire(t, a, p)
			after, err := ReadWAL(path)
			if err != nil || after.Frames == 0 || after.ActiveBytes > r.Need[WALBytes]+32 {
				t.Fatal(after, err)
			}
			publish(t, w, after, clock.now())
			if w.ResetCount() != 0 {
				t.Fatal("initial header creation counted as reuse")
			}
		})
	}
}

func TestNativeWALCopyIsNotReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.sqlite")
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, settings, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if settings.Engine != "3.53.4" || settings.TempStore != 1 {
		t.Fatal(settings)
	}
	writer, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = sqliteconn.Assert(t.Context(), writer, cfg); err != nil {
		t.Fatal(err)
	}
	// Tiny observation fixture, not INT's canonical schema or command recipe.
	if _, err = writer.ExecContext(t.Context(), "CREATE TABLE fixture(id INTEGER PRIMARY KEY, v TEXT); INSERT INTO fixture VALUES(1,'first')"); err != nil {
		t.Fatal(err)
	}
	before, err := ReadWAL(path)
	if err != nil || before.Frames == 0 || before.UncheckpointedBytes == 0 {
		t.Fatal(before, err)
	}
	w := gate(t)
	publish(t, w, before, time.Now())
	cfg.Role = sqliteconn.Checkpointer
	cpDB, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cpDB.Close()
	cp, err := cpDB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if _, err = sqliteconn.Assert(t.Context(), cp, cfg); err != nil {
		t.Fatal(err)
	}
	var busy, frames, copied int
	if err = cp.QueryRowContext(t.Context(), "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil {
		t.Fatal(err)
	}
	after, err := ReadWAL(path)
	if err != nil || busy != 0 || uint32(frames) != before.Frames || uint32(copied) != after.Backfill || after.Backfill != after.Frames || after.UncheckpointedBytes != 0 || after.ActiveBytes != before.ActiveBytes || after.PhysicalBytes != before.PhysicalBytes {
		t.Fatal(before, after, busy, frames, copied, err)
	}
	publish(t, w, after, time.Now())
	if w.ResetCount() != 0 {
		t.Fatal("completed copy counted as reset")
	}
	if _, err = writer.ExecContext(t.Context(), "UPDATE fixture SET v='second' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	reused, err := ReadWAL(path)
	if err != nil || reused.Salt == after.Salt || reused.Frames >= after.Frames || reused.PhysicalBytes != after.PhysicalBytes {
		t.Fatal(after, reused, err)
	}
	publish(t, w, reused, time.Now())
	if w.ResetCount() != 1 {
		t.Fatal("native reuse not observed")
	}
}
