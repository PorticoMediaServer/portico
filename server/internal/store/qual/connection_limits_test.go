package qual

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestConnectionZeroLimitsPreserveStricterNativeValues(t *testing.T) {
	path, cfg := connectionFixture(t)
	for _, role := range []sqliteconn.Role{sqliteconn.Writer, sqliteconn.Reader, sqliteconn.Checkpointer} {
		cfg.Role = role
		db, _, err := sqliteconn.Open(t.Context(), path, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		for id, value := range map[int]int{sqlite3.SQLITE_LIMIT_LENGTH: 1024, sqlite3.SQLITE_LIMIT_COLUMN: 4} {
			if _, err := sqlite.Limit(conn, id, value); err != nil {
				t.Fatal(err)
			}
		}
		r := captureAcquired(t, conn, cfg)
		if r.ReadLimits.Mode != "preserve_existing_unasserted" || r.ReadLimits.Actual != nil || r.Actual.ReadLimits != (sqliteconn.ReadLimits{}) {
			t.Fatalf("zero policy asserted native defaults/caps: %+v", r)
		}
		for id, want := range map[int]int{sqlite3.SQLITE_LIMIT_LENGTH: 1024, sqlite3.SQLITE_LIMIT_COLUMN: 4} {
			actual, err := sqlite.Limit(conn, id, -1)
			if err != nil || actual != want {
				t.Fatalf("observer changed native limit%d: %d %v", id, actual, err)
			}
		}
		conn.Close()
		db.Close()
	}
}

func fixedReader(t *testing.T) (*sql.DB, *probeConnector, sqliteconn.Config) {
	t.Helper()
	path, cfg := connectionFixture(t)
	cfg.Role = sqliteconn.Reader
	cfg.ReadLimits = sqliteconn.ReadLimits{LengthBytes: 4096, Columns: 4}
	base, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, counted := connectionPool(t, base, 1)
	return db, counted, cfg
}

func TestConnectionFixedLimitsRecordExactBoundaries(t *testing.T) {
	db, _, cfg := fixedReader(t)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := captureAcquired(t, conn, cfg)
	if r.ReadLimits.Actual == nil || *r.ReadLimits.Actual != cfg.ReadLimits || r.Actual.ReadLimits != cfg.ReadLimits {
		t.Fatal("actual caps not recorded", r)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ConnectionEvidence
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.ReadLimits.Mode != "fixed_reader" || decoded.ReadLimits.Actual == nil || *decoded.ReadLimits.Actual != cfg.ReadLimits || decoded.Enforcement == nil || *decoded.Enforcement != *r.Enforcement {
		t.Fatalf("wire receipt lost actual limits/enforcement: %s %v", encoded, err)
	}
	var n int
	if err := conn.QueryRowContext(t.Context(), "SELECT length(?)", strings.Repeat("x", 4096)).Scan(&n); err != nil || n != 4096 {
		t.Fatal("N length boundary", n, err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT length(?)", strings.Repeat("x", 4097)).Scan(&n); err == nil {
		t.Fatal("N+1 length admitted")
	}
	rows, err := conn.QueryContext(t.Context(), "SELECT 1,2,3,4")
	if err != nil {
		t.Fatal("N column boundary", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing boundary row")
	}
	var a, b, c, d int
	if err := rows.Scan(&a, &b, &c, &d); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if bad, err := conn.QueryContext(t.Context(), "SELECT 1,2,3,4,5"); err == nil {
		bad.Close()
		t.Fatal("N+1 columns admitted")
	}
	// Failure at a query boundary must not mutate the per-connection policy.
	captureAcquired(t, conn, cfg)
}

func TestConnectionFixedLimitDriftIsInvalidWithoutRepair(t *testing.T) {
	db, counted, cfg := fixedReader(t)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { conn.Close() }()
	captureAcquired(t, conn, cfg)
	if _, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, 1024); err != nil {
		t.Fatal(err)
	}
	r, err := CaptureConnection(t.Context(), conn, cfg)
	if !errors.Is(err, ErrConnectionEvidence) || !errors.Is(err, sqliteconn.ErrPolicy) || r.PolicyValid || r.Actual != nil || r.ReadLimits.Actual != nil || r.ReadLimits.Mode != "fixed_reader" {
		t.Fatalf("drift qualified or incomplete helper evidence called actual: %+v %v", r, err)
	}
	logConnectionEvidence(t, r)
	if n, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, -1); err != nil || n != 1024 {
		t.Fatal("observer repaired native drift", n, err)
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	conn.Close()
	conn, err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	captureAcquired(t, conn, cfg)
	if counted.opened.Load() != 2 {
		t.Fatal("physical replacement not proved", counted.opened.Load())
	}
}

func TestConnectionFixedLimitsAfterCancellationAndReplacement(t *testing.T) {
	db, counted, cfg := fixedReader(t)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { conn.Close() }()
	captureAcquired(t, conn, cfg)
	cancelPhysicalRead(t, conn)
	conn, err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	captureAcquired(t, conn, cfg)
	postCancel := counted.opened.Load()
	t.Logf("physical_opens_after_cancel=%d (cancel/replacement are separate facts)", postCancel)
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	conn.Close()
	conn, err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	captureAcquired(t, conn, cfg)
	if counted.opened.Load() != postCancel+1 {
		t.Fatal("forced replacement not proved", counted.opened.Load())
	}
}

func TestConnectionLateEnforcementReadFailureIsIncomplete(t *testing.T) {
	db := sql.OpenDB(evidenceScript{engine: "3.53.4", failQuery: "PRAGMA read_uncommitted", failOn: 2})
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	r, err := CaptureConnection(t.Context(), conn, cfg)
	if !errors.Is(err, ErrConnectionEvidence) || !errors.Is(err, io.ErrUnexpectedEOF) || r.PolicyValid || r.Actual != nil || r.Enforcement != nil || r.ReadLimits.Actual != nil {
		t.Fatalf("late failed observation became complete evidence: %+v %v", r, err)
	}
	logConnectionEvidence(t, r)
}
