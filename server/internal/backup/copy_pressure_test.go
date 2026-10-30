package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// copyFixture is a live handle with a few MiB of rows, so a copy takes many
// 256 KiB steps.
func copyFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := dbwork.OpenHandle(filepath.Join(dir, "state.sqlite"), dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `CREATE TABLE sample(id INTEGER PRIMARY KEY,body BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO sample(body) SELECT randomblob(8192) FROM json_each('[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]')`); err != nil {
			t.Fatal(err)
		}
	}
	return db, dir
}

func testCopyLimits(class dbwork.Class) copyLimits {
	limits := scheduledCopyLimits()
	limits.class = class
	limits.reportEvery = 0
	return limits
}

func copiedRows(t *testing.T, path string) int {
	t.Helper()
	copy, err := dbwork.OpenCandidate(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if err = quickCheck(context.Background(), copy); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = copy.QueryRowContext(context.Background(), `SELECT count(*) FROM sample`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The copy is taken while the server runs, with committed rows still only in
// the live WAL file: the writer never checkpoints, and nothing else does here.
// The copy holds them, and is a standalone database that passes quick_check.
func TestCopyTakenWhileRunningCarriesTheLiveWAL(t *testing.T) {
	db, dir := copyFixture(t)
	walPath := dbwork.DatabaseFile(context.Background(), db) + "-wal"
	if size, err := walBytes(walPath); err != nil || size == 0 {
		t.Fatalf("fixture has no live WAL: %d %v", size, err)
	}
	dest := filepath.Join(dir, "copy", "server.sqlite")
	if err := copyDatabase(context.Background(), db, dest, nil, testCopyLimits(dbwork.ClassMaintenance)); err != nil {
		t.Fatal(err)
	}
	if size, _ := walBytes(walPath); size == 0 {
		t.Fatal("the copy checkpointed the live WAL; it must only read it")
	}
	if n := copiedRows(t, dest); n != 32*16 {
		t.Fatalf("copy holds %d rows, want %d", n, 32*16)
	}
}

// Writes made while the copy runs go through the writer connection the copy
// steps on, so SQLite brings the copy up to date instead of restarting it:
// progress never moves backwards, and the copy holds every row committed
// before it finished.
func TestWritesDuringTheCopyUpdateItWithoutARestart(t *testing.T) {
	db, dir := copyFixture(t)
	ctx := context.Background()
	written := 32 * 16
	var lastDone int64
	steps := 0
	guard := func(done, total int64) error {
		if done < lastDone {
			t.Errorf("copy restarted: %d bytes after %d", done, lastDone)
		}
		lastDone = done
		steps++
		// A user write between steps, through the gate as every write is.
		if _, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO sample(body) VALUES(randomblob(4096))`); err != nil {
			return err
		}
		written++
		return nil
	}
	dest := filepath.Join(dir, "copy.sqlite")
	if err := copyDatabaseGuarded(ctx, db, dest, nil, testCopyLimits(dbwork.ClassMaintenance), guard); err != nil {
		t.Fatal(err)
	}
	if steps < 8 {
		t.Fatalf("copy took %d steps; the fixture should need many", steps)
	}
	// The last guard write follows the finishing step, so it is not in the copy.
	if n := copiedRows(t, dest); n != written-1 {
		t.Fatalf("copy holds %d rows, want %d", n, written-1)
	}
}

// A scheduled backup takes maintenance tickets, so it never runs while a user
// write waits. Under constant user writes it makes no progress and gives up
// with ErrBackupPressure; a backup the owner started takes interactive tickets
// and finishes alongside the same writes.
func TestScheduledBackupYieldsToConstantWritesAndManualSharesTurns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, dir := copyFixture(t)
	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var writes atomic.Int64
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				// Each write holds the writer a little, so the queue never empties.
				err := dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO sample(body) VALUES(randomblob(512))`)
					time.Sleep(2 * time.Millisecond)
					return err
				})
				if err == nil {
					writes.Add(1)
				}
			}
		}()
	}
	defer func() { stop(); wg.Wait() }()
	time.Sleep(20 * time.Millisecond)

	scheduled := testCopyLimits(dbwork.ClassMaintenance)
	scheduled.noProgress = 300 * time.Millisecond
	err := copyDatabase(context.Background(), db, filepath.Join(dir, "scheduled.sqlite"), nil, scheduled)
	if !errors.Is(err, ErrBackupPressure) {
		t.Fatalf("scheduled backup under constant writes: %v", err)
	}

	manual := testCopyLimits(dbwork.ClassInteractive)
	manual.quiet = 0
	if err = copyDatabase(context.Background(), db, filepath.Join(dir, "manual.sqlite"), nil, manual); err != nil {
		t.Fatalf("manual backup under constant writes: %v", err)
	}
	if writes.Load() == 0 {
		t.Fatal("the fixture made no writes")
	}
	// The abandoned scheduled attempt released everything: writes still go
	// through and the live log can be reset.
	stop()
	wg.Wait()
	if _, err = dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `INSERT INTO sample(body) VALUES(x'00')`); err != nil {
		t.Fatal(err)
	}
	if result := dbwork.Checkpoint(context.Background(), db); result.Err != nil || result.Busy != 0 {
		t.Fatalf("a backup left the log pinned: %+v", result)
	}
}

// An automatic backup may not hold the writer for more than a couple of
// seconds at a time: a step that is still too slow at the small size ends the
// attempt, and nothing is left behind.
func TestScheduledBackupStopsWhenEvenSmallStepsAreSlow(t *testing.T) {
	db, dir := copyFixture(t)
	limits := testCopyLimits(dbwork.ClassMaintenance)
	limits.slowStep, limits.quiet = 0, time.Nanosecond
	dest := filepath.Join(dir, "slow.sqlite")
	if err := copyDatabase(context.Background(), db, dest, nil, limits); !errors.Is(err, ErrBackupPressure) {
		t.Fatalf("slow steps: %v", err)
	}
	if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `INSERT INTO sample(body) VALUES(x'00')`); err != nil {
		t.Fatal("the stopped backup left the writer blocked", err)
	}
	// The next attempt starts clean.
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err := copyDatabase(context.Background(), db, dest, nil, testCopyLimits(dbwork.ClassMaintenance)); err != nil {
		t.Fatal(err)
	}
}

// A guard that ends the copy releases the backup on its connection, leaves
// the source writable and the log resettable.
func TestAbortedCopyReleasesTheWriter(t *testing.T) {
	db, dir := copyFixture(t)
	calls := 0
	err := copyDatabaseGuarded(context.Background(), db, filepath.Join(dir, "copy.sqlite"), nil, testCopyLimits(dbwork.ClassMaintenance), func(done, total int64) error {
		calls++
		if calls == 2 {
			return ErrBackupPressure
		}
		return nil
	})
	if !errors.Is(err, ErrBackupPressure) || calls != 2 {
		t.Fatal(err, calls)
	}
	if _, err = dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `INSERT INTO sample(body) VALUES(zeroblob(1000))`); err != nil {
		t.Fatal("backup left source blocked", err)
	}
	var busy, log, checkpointed int
	t.Logf("readers %+v", dbwork.ReaderLifetimes())
	if err = db.QueryRow(`PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &log, &checkpointed); err != nil || busy != 0 {
		t.Fatal("passive", busy, log, checkpointed, err)
	}
	if err = db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil || busy != 0 {
		t.Fatal(busy, log, checkpointed, err)
	}
}

// Every requested step counts against the attempt's copy allowance, so a copy
// SQLite keeps restarting cannot run forever.
func TestCopyAllowance(t *testing.T) {
	if got := copyAllowance(100 << 20); got != 100<<20+1<<30 {
		t.Fatalf("small database allowance %d", got)
	}
	if got := copyAllowance(1 << 30); got != 4<<30 {
		t.Fatalf("large database allowance %d", got)
	}
}

// A copy an earlier attempt could not finish stays attached to the writer
// (every write would keep updating it); the next attempt finishes it before
// copying anything.
func TestNextCopyFinishesAStrandedOne(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, dir := copyFixture(t)
	ctx := context.Background()
	limits := testCopyLimits(dbwork.ClassMaintenance)
	if err := os.WriteFile(filepath.Join(dir, "stranded.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	r := &copyRun{source: db, dest: filepath.Join(dir, "stranded.sqlite"), limits: limits}
	if _, err := r.step(ctx, backupStepBytes); err != nil || r.backup == nil {
		t.Fatalf("first step: %v", err)
	}
	strandedMu.Lock()
	stranded = append(stranded, r)
	strandedMu.Unlock()
	if err := copyDatabase(ctx, db, filepath.Join(dir, "next.sqlite"), nil, limits); err != nil {
		t.Fatal(err)
	}
	strandedMu.Lock()
	left := len(stranded)
	strandedMu.Unlock()
	if left != 0 || r.backup != nil {
		t.Fatalf("%d stranded copies left", left)
	}
}

// A stranded copy whose database has since closed was finished by the driver
// as its connections closed: settling drops it instead of keeping it forever.
func TestAStrandedCopyOnAClosedDatabaseIsDropped(t *testing.T) {
	dir := t.TempDir()
	db, err := dbwork.OpenHandle(filepath.Join(dir, "closed.sqlite"), dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `CREATE TABLE sample(body BLOB); INSERT INTO sample SELECT randomblob(8192) FROM json_each('[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32,33,34,35,36,37,38,39,40]')`); err != nil {
		t.Fatal(err)
	}
	limits := testCopyLimits(dbwork.ClassMaintenance)
	if err = os.WriteFile(filepath.Join(dir, "stranded.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	r := &copyRun{source: db, dest: filepath.Join(dir, "stranded.sqlite"), limits: limits}
	if _, err = r.step(ctx, backupStepBytes); err != nil || r.backup == nil {
		t.Fatalf("first step: %v", err)
	}
	strandedMu.Lock()
	stranded = append(stranded, r)
	strandedMu.Unlock()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	// The next attempt usually runs on a new handle; it sweeps the old one.
	other, _ := copyFixture(t)
	if err = settleStranded(ctx, other, limits); err != nil {
		t.Fatalf("settling with a new handle: %v", err)
	}
	strandedMu.Lock()
	defer strandedMu.Unlock()
	for _, left := range stranded {
		if left == r {
			t.Fatal("a closed database's copy stayed stranded")
		}
	}
}

// Settling runs from several backup services at once; the registry is shared.
func TestConcurrentSettlingIsRaceFree(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	limits := testCopyLimits(dbwork.ClassMaintenance)
	var sources []*sql.DB
	for i := 0; i < 4; i++ {
		db, err := dbwork.OpenHandle(filepath.Join(dir, fmt.Sprintf("s%d.sqlite", i)), dbwork.DefaultPolicy())
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, db)
		strandedMu.Lock()
		stranded = append(stranded, &copyRun{source: db})
		strandedMu.Unlock()
		if i%2 == 0 {
			db.Close()
		} else {
			t.Cleanup(func() { db.Close() })
		}
	}
	var wg sync.WaitGroup
	for _, db := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = settleStranded(ctx, db, limits)
		}()
	}
	wg.Wait()
	strandedMu.Lock()
	stranded = nil
	strandedMu.Unlock()
}
