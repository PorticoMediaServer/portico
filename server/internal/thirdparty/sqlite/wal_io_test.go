package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

func walTestDB(t *testing.T, pageSize int) (*sql.DB, *sql.Conn, *conn, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "io.sqlite")
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Exec(fmt.Sprintf("PRAGMA page_size=%d;CREATE TABLE format_probe(id INTEGER PRIMARY KEY)", pageSize)); err != nil {
		seed.Close()
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)&_pragma=cache_size(4)", path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	c, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); db.Close() })
	var native *conn
	if err := c.Raw(func(raw any) error { native = raw.(*conn); return nil }); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "linux" {
		if native.WALIO().Qualified {
			t.Fatal("unqualified OS claimed accounting")
		}
		t.Skip("Linux default unix VFS qualification")
	}
	if !native.WALIO().Qualified {
		t.Fatal("observer absent before first mutation")
	}
	var gotSize int
	if err = c.QueryRowContext(context.Background(), `PRAGMA page_size`).Scan(&gotSize); err != nil || gotSize != pageSize {
		t.Fatalf("page size %d want %d: %v", gotSize, pageSize, err)
	}
	return db, c, native, path
}

func walExec(t *testing.T, c *sql.Conn, q string) {
	t.Helper()
	if _, err := c.ExecContext(context.Background(), q); err != nil {
		t.Fatal(err)
	}
}

func TestWALIOCountsSpillRollbackAndReuseAtActualPageSizes(t *testing.T) {
	for _, size := range []int{1024, 4096, 65536} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			_, c, native, path := walTestDB(t, size)
			walExec(t, c, `CREATE TABLE sample(id INTEGER PRIMARY KEY,body BLOB)`)
			before := native.WALIO()
			walExec(t, c, `BEGIN IMMEDIATE; WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<64) INSERT INTO sample SELECT i,zeroblob(8192) FROM ids`)
			spilled := native.WALIO()
			if spilled.AttemptedBytes <= before.AttemptedBytes {
				t.Fatal("tiny-cache uncommitted spill was not counted")
			}
			walExec(t, c, `ROLLBACK`)
			rolled := native.WALIO()
			if rolled.AttemptedBytes < spilled.AttemptedBytes {
				t.Fatal("rollback refunded native writes")
			}
			var count int
			if err := c.QueryRowContext(context.Background(), `SELECT count(*) FROM sample`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rollback semantics changed: %d %v", count, err)
			}
			walExec(t, c, `WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<64) INSERT INTO sample SELECT i,zeroblob(8192) FROM ids`)
			walExec(t, c, `PRAGMA wal_checkpoint(PASSIVE)`)
			stat, err := os.Stat(path + "-wal")
			if err != nil {
				t.Fatal(err)
			}
			copied := native.WALIO()
			walExec(t, c, `UPDATE sample SET body=zeroblob(8191) WHERE id=1`)
			reused := native.WALIO()
			after, err := os.Stat(path + "-wal")
			if err != nil {
				t.Fatal(err)
			}
			if reused.AttemptedBytes <= copied.AttemptedBytes || after.Size() != stat.Size() {
				t.Fatalf("same-size WAL reuse unmeasured: before %+v after %+v sizes %d/%d", copied, reused, stat.Size(), after.Size())
			}
			if reused.WrittenBytes != reused.AttemptedBytes || reused.WrittenEnd > after.Size() || reused.HeaderWrites == 0 {
				t.Fatalf("I/O counters do not cover actual file: %+v size %d", reused, after.Size())
			}
			walExec(t, c, `PRAGMA wal_checkpoint(TRUNCATE)`)
			reset := native.WALIO()
			if reset.AttemptedBytes != reused.AttemptedBytes || reset.Truncates <= reused.Truncates || reset.CurrentEnd != 0 {
				t.Fatalf("reset lost accounting: before %+v after %+v", reused, reset)
			}
		})
	}
}

func testWALWriteFailure(_ *libc.TLS, _ uintptr, _ uintptr, _ int32, _ int64) int32 {
	return sqlite3.SQLITE_IOERR_WRITE
}

func testWALWriteSuccess(_ *libc.TLS, _ uintptr, _ uintptr, _ int32, _ int64) int32 {
	return sqlite3.SQLITE_OK
}

func TestWALIOObserverDoesNotAllocatePerWrite(t *testing.T) {
	const token = uintptr(1)
	f := &walNativeFile{state: &walIOState{}, original: sqlite3.Tsqlite3_io_methods{FxWrite: cFuncPointer(testWALWriteSuccess)}}
	walNative.mu.Lock()
	if walNative.files[token] != nil {
		walNative.mu.Unlock()
		t.Fatal("test token occupied")
	}
	walNative.files[token] = f
	walNative.mu.Unlock()
	defer func() { walNative.mu.Lock(); delete(walNative.files, token); walNative.mu.Unlock() }()
	if got := testing.AllocsPerRun(1000, func() { walWrite(nil, token, 0, 32, 0) }); got != 0 {
		t.Fatalf("%.2f allocations per native write", got)
	}
}

func TestWALIOPreservesNativeWriteFailureAndCountsAttempt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, c, native, _ := walTestDB(t, 4096)
	walExec(t, c, `CREATE TABLE sample(body BLOB)`)
	var file *walNativeFile
	walNative.mu.Lock()
	for _, f := range walNative.files {
		if f.state == native.walIO {
			file = f
			break
		}
	}
	if file == nil {
		walNative.mu.Unlock()
		t.Fatal("live WAL file absent")
	}
	original := file.original.FxWrite
	file.original.FxWrite = cFuncPointer(testWALWriteFailure)
	walNative.mu.Unlock()
	before := native.WALIO()
	_, err := c.ExecContext(context.Background(), `INSERT INTO sample VALUES(zeroblob(8192))`)
	walNative.mu.Lock()
	file.original.FxWrite = original
	walNative.mu.Unlock()
	if err == nil {
		t.Fatal("native write failure swallowed")
	}
	if e, ok := err.(*Error); !ok || e.Code() != sqlite3.SQLITE_IOERR_WRITE {
		t.Fatalf("native error replaced: %v", err)
	}
	after := native.WALIO()
	if after.AttemptedBytes <= before.AttemptedBytes || after.Errors <= before.Errors || after.WrittenBytes != before.WrittenBytes {
		t.Fatalf("failed bytes treated as successful/refunded: before %+v after %+v", before, after)
	}
}

func TestWALIOReleasesRegistriesWhenNativeFilesClose(t *testing.T) {
	db, c, native, path := walTestDB(t, 4096)
	walExec(t, c, `CREATE TABLE sample(body BLOB);INSERT INTO sample VALUES(x'00')`)
	if !native.WALIO().Qualified {
		t.Fatal("unqualified")
	}
	c.Close()
	db.Close()
	walNative.mu.RLock()
	defer walNative.mu.RUnlock()
	if _, exists := walNative.states[path]; exists {
		t.Fatal("closed database retained counter state")
	}
	for _, f := range walNative.files {
		if f.state.path == path {
			t.Fatal("closed WAL retained native methods")
		}
	}
}
