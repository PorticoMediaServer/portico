package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

// These limits are unit-fixture connection initialization, not request setters.
// Production snapshot pools receive their fixed policy from sqliteconn.
func fixedBudgetConn(t *testing.T, length, columns int) (*sql.DB, *sql.Conn) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fixed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, length); err != nil {
		t.Fatal(err)
	}
	if _, err = Limit(conn, sqlite3.SQLITE_LIMIT_COLUMN, columns); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); db.Close() })
	return db, conn
}
func assertFixed(t *testing.T, conn *sql.Conn, length, columns int) {
	t.Helper()
	for id, want := range map[int]int{sqlite3.SQLITE_LIMIT_LENGTH: length, sqlite3.SQLITE_LIMIT_COLUMN: columns} {
		got, err := Limit(conn, id, -1)
		if err != nil || got != want {
			t.Fatal("read budget changed fixed native policy", id, got, want, err)
		}
	}
}
func TestRowBudgetOverlapNeverChangesFixedLimits(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			db, conn := fixedBudgetConn(t, 65536, 16)
			a, _ := NewRowBudget(2, 64<<10)
			b, _ := NewRowBudget(2, 128<<10)
			first, err := conn.QueryContext(WithRowBudget(t.Context(), a), "SELECT zeroblob(4096)")
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second, err := conn.QueryContext(WithRowBudget(t.Context(), b), "SELECT zeroblob(4096)")
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			assertFixed(t, conn, 65536, 16)
			if reverse {
				second.Close()
				first.Close()
			} else {
				first.Close()
				second.Close()
			}
			assertFixed(t, conn, 65536, 16)
			conn.Close()
			again, err := db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			assertFixed(t, again, 65536, 16)
			var blob []byte
			if err = again.QueryRowContext(t.Context(), "SELECT zeroblob(32768)").Scan(&blob); err != nil || len(blob) != 32768 {
				t.Fatal(len(blob), err)
			}
		})
	}
}
func TestRowBudgetNeverRaisesStricterLimit(t *testing.T) {
	_, conn := fixedBudgetConn(t, 1024, 16)
	for _, query := range []string{"SELECT zeroblob(2048)", "SELECT 1 UNION ALL SELECT zeroblob(2048)"} {
		budget, _ := NewRowBudget(2, 64<<10)
		rows, err := conn.QueryContext(WithRowBudget(t.Context(), budget), query)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var v any
				if err = rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
			}
			err = errors.Join(rows.Err(), rows.Close())
		}
		if err == nil {
			t.Fatal("fixed1024limit was raised", query)
		}
		assertFixed(t, conn, 1024, 16)
	}
	statement, err := conn.PrepareContext(t.Context(), "SELECT zeroblob(?)")
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for i := 0; i < 2; i++ {
		budget, _ := NewRowBudget(1, 64<<10)
		rows, err := statement.QueryContext(WithRowBudget(t.Context(), budget), 2048)
		if rows != nil {
			rows.Close()
		}
		if err == nil {
			t.Fatal("prepared query raised fixed limit")
		}
		assertFixed(t, conn, 1024, 16)
	}
	var blob []byte
	if err = conn.QueryRowContext(t.Context(), "SELECT zeroblob(512)").Scan(&blob); err != nil || len(blob) != 512 {
		t.Fatal(len(blob), err)
	}
}
func TestRowBudgetNativeAggregateIsSeparateFromGoBudget(t *testing.T) {
	for _, columns := range []int{16, 128} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			connector, err := NewConnector(filepath.Join(t.TempDir(), "aggregate.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := connector.Connect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			c := raw.(*conn)
			c.limit(sqlite3.SQLITE_LIMIT_LENGTH, 16384)
			c.limit(sqlite3.SQLITE_LIMIT_COLUMN, columns)
			query := "SELECT " + strings.TrimSuffix(strings.Repeat("randomblob(12000),", columns), ",")
			budget, _ := NewRowBudget(1, 64<<10)
			rr, err := c.QueryContext(WithRowBudget(t.Context(), budget), query, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer rr.Close()
			r := rr.(*rows)
			pointers := make(map[uintptr]bool)
			var nativeBytes int64
			for i := 0; i < columns; i++ {
				nativeBytes += int64(sqlite3.Xsqlite3_column_bytes(c.tls, r.pstmt, int32(i)))
				pointers[sqlite3.Xsqlite3_column_blob(c.tls, r.pstmt, int32(i))] = true
			}
			reservedNative := int64(columns) * 16384
			if nativeBytes != int64(columns)*12000 || len(pointers) != columns || nativeBytes > reservedNative {
				t.Fatal(nativeBytes, reservedNative, len(pointers))
			}
			dest := make([]driver.Value, columns)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			err = rr.Next(dest)
			runtime.ReadMemStats(&after)
			if !errors.Is(err, ErrRowBudget) || after.TotalAlloc-before.TotalAlloc > 64<<10 {
				t.Fatal("Go pre-copy aggregate failed", err, after.TotalAlloc-before.TotalAlloc)
			}
			t.Logf("columns=%d fixedNativeReservation=%d actualNative=%d GoBudget=%d GoAlloc=%d", columns, reservedNative, nativeBytes, 65536, after.TotalAlloc-before.TotalAlloc)
			if c.limit(sqlite3.SQLITE_LIMIT_LENGTH, -1) != 16384 || c.limit(sqlite3.SQLITE_LIMIT_COLUMN, -1) != columns {
				t.Fatal("pre-copy path mutated fixed limits")
			}
		})
	}
}
func TestRowBudgetOtherPoolUnbudgetedReadsUnchanged(t *testing.T) {
	_, bounded := fixedBudgetConn(t, 1024, 16)
	other, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ordinary.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before, err := Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, -1)
	if err != nil {
		t.Fatal(err)
	}
	budget, _ := NewRowBudget(1, 64<<10)
	rows, _ := bounded.QueryContext(WithRowBudget(t.Context(), budget), "SELECT zeroblob(2048)")
	if rows != nil {
		rows.Close()
	}
	var blob []byte
	if err = conn.QueryRowContext(context.Background(), "SELECT zeroblob(131072)").Scan(&blob); err != nil || len(blob) != 131072 {
		t.Fatal(len(blob), err)
	}
	after, err := Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, -1)
	if err != nil || after != before {
		t.Fatal(before, after, err)
	}
}

func TestRowBudgetStepPanicCannotChangeFixedLimit(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	d := &Driver{}
	if err := d.RegisterDeterministicScalarFunction("fixed_limit_panic", 0, func(*FunctionContext, []driver.Value) (driver.Value, error) { panic("fixed-limit scalar panic") }); err != nil {
		t.Fatal(err)
	}
	raw, err := d.Open(filepath.Join(t.TempDir(), "panic.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	c := raw.(*conn)
	c.limit(sqlite3.SQLITE_LIMIT_LENGTH, 1024)
	c.limit(sqlite3.SQLITE_LIMIT_COLUMN, 16)
	statement, err := raw.Prepare("SELECT fixed_limit_panic()")
	if err != nil {
		t.Fatal(err)
	}
	budget, _ := NewRowBudget(1, 64<<10)
	var caught any
	func() {
		defer func() { caught = recover() }()
		_, _ = statement.(driver.StmtQueryContext).QueryContext(WithRowBudget(t.Context(), budget), nil)
	}()
	if caught != "fixed-limit scalar panic" {
		t.Fatal("native scalar panic not exercised", caught)
	}
	if c.limit(sqlite3.SQLITE_LIMIT_LENGTH, -1) != 1024 || c.limit(sqlite3.SQLITE_LIMIT_COLUMN, -1) != 16 {
		t.Fatal("panic changed fixed pool policy")
	}
	// Explicit statement disposal is still required; no limit restoration is
	// transferred to an unreturned result or left to GC.
	_ = statement.Close()
	if c.limit(sqlite3.SQLITE_LIMIT_LENGTH, -1) != 1024 {
		t.Fatal("statement disposal changed fixed limit")
	}
}

func TestRowBudgetPreCopyMetadataRowsAndLiveCopies(t *testing.T) {
	_, conn := fixedBudgetConn(t, 16384, 16)
	for _, test := range []struct {
		query string
		rows  uint32
		bytes int64
	}{
		{"SELECT NULL AS v", 1, 64},
		{"SELECT zeroblob(12000) UNION ALL SELECT zeroblob(12000)", 2, 64 << 10},
		{"SELECT 1 UNION ALL SELECT 2", 1, 64 << 10},
	} {
		budget, _ := NewRowBudget(test.rows, test.bytes)
		result, err := conn.QueryContext(WithRowBudget(t.Context(), budget), test.query)
		if err == nil {
			defer result.Close()
			for result.Next() {
				var v any
				if err = result.Scan(&v); err != nil {
					t.Fatal(err)
				}
			}
			err = errors.Join(result.Err(), result.Close())
		}
		if !errors.Is(err, ErrRowBudget) {
			t.Fatal("Go aggregate/row/metadata guard was not enforced", test.query, err)
		}
		assertFixed(t, conn, 16384, 16)
		var healthy int
		if err = conn.QueryRowContext(t.Context(), "SELECT 42").Scan(&healthy); err != nil || healthy != 42 {
			t.Fatal("construction failure damaged statement ownership", healthy, err)
		}
	}
}
