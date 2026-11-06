package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestWriterProgressesWithEntireReadPoolOccupied(t *testing.T) {
	db := testDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var readers []*sql.Conn
	for range db.Stats().MaxOpenConnections {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, c)
	}
	defer func() {
		for _, c := range readers {
			c.Close()
		}
	}()
	for _, class := range []Class{ClassInteractive, ClassSecurityFence} {
		if err := WithWriteTx(ctx, db, class, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO rows(value) VALUES('write')`)
			return err
		}); err != nil {
			t.Fatal("readers starved writer", class, err)
		}
	}
	if _, err := ExecWrite(ctx, db, ClassInteractive, `INSERT INTO rows(value) VALUES('exec')`); err != nil {
		t.Fatal("readers starved ExecWrite", err)
	}
	reserved, conn, release, err := ReserveWriterConn(ctx, db, ClassMaintenance)
	if err != nil {
		t.Fatal("readers starved writer reservation", err)
	}
	defer release()
	w, err := BeginConn(reserved, conn, ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	if _, err = w.Tx().ExecContext(reserved, `INSERT INTO rows(value) VALUES('reserved')`); err != nil {
		t.Fatal(err)
	}
	if err = w.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestClosingPublicHandleClosesWriterPool(t *testing.T) {
	db := testDB(t)
	writer := writerHandle(db)
	background := ReadHandle(WithClass(context.Background(), ClassBackgroundMedia), db)
	if writer == db {
		t.Fatal("writer not separate")
	}
	if background == db || background == writer || background.Stats().MaxOpenConnections != DefaultPolicy().BackgroundConns || db.Stats().MaxOpenConnections != DefaultPolicy().MaxOpenConns {
		t.Fatal("foreground/background pools are not isolated")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Ping(); err == nil {
		t.Fatal("writer survived database close")
	}
	if err := background.Ping(); err == nil {
		t.Fatal("background reader survived database close")
	}
}

func TestClosedPoolsDrainBorrowedConnections(t *testing.T) {
	for _, pool := range []string{"foreground", "background", "writer"} {
		t.Run(pool, func(t *testing.T) {
			db := testDB(t)
			ctx := context.Background()
			handle := db
			if pool == "background" {
				handle = ReadHandle(WithClass(ctx, ClassBackgroundMedia), db)
			} else if pool == "writer" {
				handle = writerHandle(db)
			}
			conn, err := handle.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if ConnectionsDrained(db) {
				t.Fatal("borrowed connection was reported drained")
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if !ConnectionsDrained(db) {
				t.Fatal("closed pools remained undrained after the last connection returned")
			}
		})
	}
}

func TestBackgroundAndForegroundReadPoolsCannotStarveEachOther(t *testing.T) {
	for _, size := range []string{"default", "maximum"} {
		t.Run(size, func(t *testing.T) {
			policy := DefaultPolicy()
			if size == "maximum" {
				// Exercise large pools without changing GOMAXPROCS: this test
				// must also run on the minimum-core CI/host profile.
				policy.MaxOpenConns, policy.BackgroundConns = hostReaders(16)
				policy.MaxIdleConns = policy.MaxOpenConns
			}
			db, err := OpenHandle(filepath.Join(t.TempDir(), "pools.sqlite"), policy)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			assertNoExtraLoan := func(pool *sql.DB, capacity int) {
				t.Helper()
				wait, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
				extra, err := pool.Conn(wait)
				if extra != nil {
					extra.Close()
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("loan beyond capacity %d: got %v, want deadline exceeded", capacity, err)
				}
				if stats := pool.Stats(); stats.InUse != capacity || stats.OpenConnections != capacity {
					t.Fatalf("extra loan changed occupancy: in use %d, open %d, want %d", stats.InUse, stats.OpenConnections, capacity)
				}
			}
			backgroundCtx := WithClass(ctx, ClassBackgroundMedia)
			background := ReadHandle(backgroundCtx, db)
			if background == db || background == writerHandle(db) ||
				db.Stats().MaxOpenConnections != policy.MaxOpenConns || background.Stats().MaxOpenConnections != policy.BackgroundConns {
				t.Fatal("reader pools are not isolated at the configured capacities")
			}
			foreground := []*sql.Conn{}
			defer func() {
				for _, conn := range foreground {
					conn.Close()
				}
			}()
			// Saturate the ACTUAL pool, not a fixed number of loans: the
			// two-core policy has four connections, while larger hosts have 16.
			for range db.Stats().MaxOpenConnections {
				conn, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				foreground = append(foreground, conn)
			}
			if db.Stats().InUse != policy.MaxOpenConns {
				t.Fatal("foreground pool was not saturated")
			}
			assertNoExtraLoan(db, policy.MaxOpenConns)
			if err := QueryRow(backgroundCtx, db, `SELECT 1`).Scan(new(int)); err != nil {
				t.Fatal("foreground readers starved background", err)
			}
			for _, conn := range foreground {
				conn.Close()
			}
			foreground = nil
			backgroundConns := []*sql.Conn{}
			defer func() {
				for _, conn := range backgroundConns {
					conn.Close()
				}
			}()
			for range background.Stats().MaxOpenConnections {
				conn, err := background.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				backgroundConns = append(backgroundConns, conn)
			}
			if background.Stats().InUse != policy.BackgroundConns {
				t.Fatal("background pool was not saturated")
			}
			assertNoExtraLoan(background, policy.BackgroundConns)
			if err := QueryRow(ctx, db, `SELECT 1`).Scan(new(int)); err != nil {
				t.Fatal("background readers starved foreground", err)
			}
		})
	}
}

func TestBackgroundReadersAreBoundedWithoutHoldingForegroundCapacity(t *testing.T) {
	db := testDB(t)
	ctx := WithClass(context.Background(), ClassBackgroundMedia)
	bg := ReadHandle(ctx, db)
	held := []*sql.Conn{}
	defer func() {
		for _, conn := range held {
			conn.Close()
		}
	}()
	for range DefaultPolicy().BackgroundConns {
		conn, err := bg.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	wait, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if extra, err := bg.Conn(wait); err == nil {
		extra.Close()
		t.Fatal("background readers exceeded their pool")
	}
	if err := QueryRow(context.Background(), db, `SELECT 1`).Scan(new(int)); err != nil {
		t.Fatal(err)
	}
	held[0].Close()
	held = held[1:]
	if err := QueryRow(ctx, db, `SELECT 1`).Scan(new(int)); err != nil {
		t.Fatal("background reader did not resume", err)
	}
}

func TestCheckpointControlBypassesOnlyTheBackgroundConnection(t *testing.T) {
	db := testDB(t)
	for _, class := range []Class{ClassMaintenance, ClassBackgroundMedia} {
		ctx := WithClass(context.Background(), class)
		background := ReadHandle(ctx, db)
		if background == db || background.Stats().MaxOpenConnections != DefaultPolicy().BackgroundConns {
			t.Fatalf("ordinary %v work escaped the background pool", class)
		}
		control := context.WithValue(ctx, checkpointReaderBypassKey{}, true)
		if ReadHandle(control, db) != db {
			t.Fatal("checkpoint control waited on the connection it must recover")
		}
		if ReadHandle(ctx, db) != background {
			t.Fatal("checkpoint control changed ordinary background routing")
		}
	}
	if db.Stats().MaxOpenConnections != DefaultPolicy().MaxOpenConns {
		t.Fatal("foreground capacity changed")
	}
}
