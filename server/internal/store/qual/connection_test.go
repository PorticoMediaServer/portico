package qual

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

type probeConnector struct {
	driver.Connector
	opened atomic.Int64
}

func (c *probeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err == nil {
		c.opened.Add(1)
	}
	return conn, err // preserve every native driver interface
}

func connectionFixture(t *testing.T) (string, sqliteconn.Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "connection.sqlite")
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, startup, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if startup.Engine != "3.53.4" || startup.TempStore != 1 {
		t.Fatalf("unexpected merged helper startup: %+v", startup)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE parent(id INTEGER PRIMARY KEY);CREATE TABLE child(parent_id INTEGER REFERENCES parent(id));INSERT INTO parent VALUES(1);INSERT INTO child VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	return path, cfg
}

func connectionPool(t *testing.T, base driver.Connector, count int) (*sql.DB, *probeConnector) {
	t.Helper()
	counted := &probeConnector{Connector: base}
	db := sql.OpenDB(counted)
	db.SetMaxOpenConns(count)
	db.SetMaxIdleConns(count)
	t.Cleanup(func() { db.Close() })
	return db, counted
}

func captureAcquired(t *testing.T, conn *sql.Conn, cfg sqliteconn.Config) ConnectionEvidence {
	t.Helper()
	r, err := CaptureConnection(t.Context(), conn, cfg)
	if err != nil || !r.PolicyValid || r.Actual == nil || r.Actual.TempStore != 1 || r.Enforcement == nil || r.Error != "" {
		t.Fatalf("invalid acquisition: %+v: %v", r, err)
	}
	if cfg.ReadLimits == (sqliteconn.ReadLimits{}) {
		if r.ReadLimits.Mode != "preserve_existing_unasserted" || r.ReadLimits.Actual != nil {
			t.Fatal("zero policy claimed observed native caps", r.ReadLimits)
		}
	} else if r.ReadLimits.Mode != "fixed_reader" || r.ReadLimits.Actual == nil || *r.ReadLimits.Actual != cfg.ReadLimits {
		t.Fatal("fixed native limits not retained", r.ReadLimits)
	}
	logConnectionEvidence(t, r)
	return r
}

func logConnectionEvidence(t *testing.T, r ConnectionEvidence) {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("connection_evidence=%s", b)
}

func TestConnectionReceiptsRecordAllActualSettings(t *testing.T) {
	path, cfg := connectionFixture(t)
	for _, role := range []sqliteconn.Role{sqliteconn.Writer, sqliteconn.Reader, sqliteconn.Checkpointer} {
		for _, mode := range []sqliteconn.SyncMode{sqliteconn.Normal, sqliteconn.Full} {
			cfg.Role, cfg.Synchronous = role, mode
			db, startup, err := sqliteconn.Open(t.Context(), path, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			conn, err := db.Conn(t.Context())
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			r := captureAcquired(t, conn, cfg)
			if *r.Actual != startup || r.Expected != cfg || r.At.IsZero() || r.AssertionElapsedNS <= 0 {
				t.Fatalf("receipt lost actual settings/expected policy: %+v", r)
			}
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ConnectionEvidence
			if err := json.Unmarshal(b, &decoded); err != nil || !reflect.DeepEqual(r.Actual, decoded.Actual) {
				t.Fatalf("receipt round trip lost actual settings: %s, %v", b, err)
			}
			conn.Close()
			db.Close()
		}
	}
}

func TestConnectionReceiptRetainsDriftWithoutRepair(t *testing.T) {
	path, cfg := connectionFixture(t)
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	captureAcquired(t, conn, cfg)
	if _, err := conn.ExecContext(t.Context(), "PRAGMA synchronous=FULL; PRAGMA temp_store=MEMORY; PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	r, err := CaptureConnection(t.Context(), conn, cfg)
	if !errors.Is(err, ErrConnectionEvidence) || !errors.Is(err, sqliteconn.ErrPolicy) || r.PolicyValid || r.Error == "" || r.Actual != nil || r.Enforcement != nil {
		t.Fatalf("failed readback qualified or fabricated complete settings: %+v %v", r, err)
	}
	logConnectionEvidence(t, r)
	// Independent diagnostic reads prove the observer did not repair drift.
	for query, want := range map[string]int{"PRAGMA synchronous": 2, "PRAGMA temp_store": 2, "PRAGMA foreign_keys": 0} {
		var n int
		if e := conn.QueryRowContext(t.Context(), query).Scan(&n); e != nil || n != want {
			t.Fatalf("observer repaired %s: %d %v", query, n, e)
		}
	}

}

func TestConnectionReceiptPoolGrowthAndReplacement(t *testing.T) {
	path, cfg := connectionFixture(t)
	cfg.Role, cfg.Connections = sqliteconn.Reader, 3
	cfg.ReadLimits = sqliteconn.ReadLimits{LengthBytes: 16384, Columns: 8}
	base, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, counted := connectionPool(t, base, 3)
	var held []*sql.Conn
	defer func() {
		for _, conn := range held {
			conn.Close()
		}
	}()
	for range 3 {
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
		r := captureAcquired(t, conn, cfg)
		if r.Actual.QueryOnly != 1 {
			t.Fatal("reader evidence is writable", r.Actual)
		}
	}
	if counted.opened.Load() != 3 {
		t.Fatal("growth did not use three physical connections", counted.opened.Load())
	}
	if err := held[0].Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	held[0].Close()
	held[0], err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r := captureAcquired(t, held[0], cfg)
	if counted.opened.Load() != 4 || r.Actual.QueryOnly != 1 {
		t.Fatal("replacement was not asserted", counted.opened.Load(), r.Actual)
	}
}

func cancelPhysicalRead(t *testing.T, conn *sql.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	var n int
	err := conn.QueryRowContext(ctx, `WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq) SELECT sum(x) FROM seq`).Scan(&n)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("control did not cancel native read", err)
	}
	conn.Close()
}

func requireForeignKey787(t *testing.T, conn *sql.Conn) {
	t.Helper()
	_, err := conn.ExecContext(t.Context(), "DELETE FROM parent WHERE id=1")
	var code interface{ Code() int }
	if !errors.As(err, &code) || code.Code() != 787 {
		t.Fatal("FK constraint not enforced", err)
	}
}

func TestConnectionCancellationReplacementKeepsPolicy(t *testing.T) {
	path, cfg := connectionFixture(t)
	base, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, counted := connectionPool(t, base, 1)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before := captureAcquired(t, conn, cfg)
	requireForeignKey787(t, conn)
	cancelPhysicalRead(t, conn)
	conn, err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	after := captureAcquired(t, conn, cfg)
	if counted.opened.Load() != 2 {
		t.Fatal("cancellation did not replace physical connection", counted.opened.Load())
	}
	t.Logf("physical_opens=%d", counted.opened.Load())
	if *before.Actual != *after.Actual {
		t.Fatalf("replacement lost policy: %+v %+v", before.Actual, after.Actual)
	}
	requireForeignKey787(t, conn)
}

func TestConnectionOneTimePolicyCancellationIsInvalid(t *testing.T) {
	path, cfg := connectionFixture(t)
	// Deliberately unsafe negative control, never an alternative harness policy.
	base, err := sqlite.NewConnector(path)
	if err != nil {
		t.Fatal(err)
	}
	db, counted := connectionPool(t, base, 1)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), `PRAGMA journal_mode=WAL;PRAGMA foreign_keys=ON;PRAGMA synchronous=NORMAL;PRAGMA wal_autocheckpoint=0;PRAGMA cache_size=-1024;PRAGMA busy_timeout=0;PRAGMA mmap_size=0;PRAGMA query_only=0;PRAGMA temp_store=FILE`); err != nil {
		t.Fatal(err)
	}
	before := captureAcquired(t, conn, cfg)
	requireForeignKey787(t, conn)
	cancelPhysicalRead(t, conn)
	conn, err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	after, err := CaptureConnection(t.Context(), conn, cfg)
	if counted.opened.Load() != 2 {
		t.Fatal("negative did not exercise actual replacement", counted.opened.Load())
	}
	if !errors.Is(err, ErrConnectionEvidence) || !errors.Is(err, sqliteconn.ErrPolicy) || after.PolicyValid || after.Actual != nil || after.Error == "" {
		t.Fatalf("lost policy still qualified: %+v %v", after, err)
	}
	if before.Actual.ForeignKeys != 1 {
		t.Fatal("negative lacked initial FK assertion")
	}
	observed := map[string]int{}
	for query, want := range map[string]int{"PRAGMA foreign_keys": 0, "PRAGMA temp_store": 0, "PRAGMA wal_autocheckpoint": 1000} {
		var n int
		if e := conn.QueryRowContext(t.Context(), query).Scan(&n); e != nil || n != want {
			t.Fatalf("default-policy negative not reproduced %s: %d %v", query, n, e)
		}
		observed[query] = n
	}
	t.Logf("rejected_connection_defaults=%v", observed)
	logConnectionEvidence(t, after)
	t.Logf("physical_opens=%d; expected unsafe-one-time negative", counted.opened.Load())
	if _, err := conn.ExecContext(t.Context(), "DELETE FROM parent WHERE id=1"); err != nil {
		t.Fatal("negative did not reproduce missing FK enforcement", err)
	}
}

func TestConnectionReceiptReadFailureIsNullNotZero(t *testing.T) {
	path, cfg := connectionFixture(t)
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	live, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	for _, input := range []struct {
		conn *sql.Conn
		cfg  sqliteconn.Config
	}{{nil, cfg}, {conn, cfg}, {live, sqliteconn.Config{}}} {
		r, err := CaptureConnection(t.Context(), input.conn, input.cfg)
		if !errors.Is(err, ErrConnectionEvidence) || r.PolicyValid || r.Actual != nil || r.Error == "" {
			t.Fatalf("unavailable settings became zeros/pass: %+v %v", r, err)
		}
		logConnectionEvidence(t, r)
		b, err := json.Marshal(r)
		if err != nil || !strings.Contains(string(b), `"actual":null`) {
			t.Fatalf("unavailable read must stay null: %s %v", b, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r, err := CaptureConnection(ctx, live, cfg)
	if !errors.Is(err, context.Canceled) || r.PolicyValid || r.Actual != nil {
		t.Fatalf("cancelled assertion supplied usable evidence: %+v %v", r, err)
	}
	logConnectionEvidence(t, r)
}

// A deterministic readback-only driver isolates version and partial-read
// controls. Native replacement/FK semantics above use the real bundled engine.
type evidenceScript struct {
	engine, failQuery string
	failOn            int
}

func (s evidenceScript) Driver() driver.Driver            { return s }
func (s evidenceScript) Open(string) (driver.Conn, error) { return s.Connect(context.Background()) }
func (s evidenceScript) Connect(context.Context) (driver.Conn, error) {
	return evidenceScriptConn{script: s, calls: map[string]int{}}, nil
}

type evidenceScriptConn struct {
	script evidenceScript
	calls  map[string]int
}

func (c evidenceScriptConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("readback control only")
}
func (c evidenceScriptConn) Begin() (driver.Tx, error) {
	return nil, errors.New("readback control only")
}
func (c evidenceScriptConn) Close() error { return nil }
func (c evidenceScriptConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.calls[query]++
	if query == c.script.failQuery && (c.script.failOn == 0 || c.calls[query] == c.script.failOn) {
		return nil, io.ErrUnexpectedEOF
	}
	values := map[string]driver.Value{
		"SELECT sqlite_version()": c.script.engine, "PRAGMA journal_mode": "wal",
		"PRAGMA foreign_keys": int64(1), "PRAGMA synchronous": int64(1),
		"PRAGMA ignore_check_constraints": int64(0), "PRAGMA defer_foreign_keys": int64(0),
		"PRAGMA writable_schema": int64(0), "PRAGMA read_uncommitted": int64(0),
		"PRAGMA wal_autocheckpoint": int64(0), "PRAGMA cache_size": int64(-1024),
		"PRAGMA busy_timeout": int64(0), "PRAGMA mmap_size": int64(0),
		"PRAGMA query_only": int64(0), "PRAGMA temp_store": int64(1),
	}
	value, ok := values[query]
	if !ok {
		return nil, errors.New("unexpected control query")
	}
	return &evidenceScriptRows{value: value}, nil
}

type evidenceScriptRows struct {
	value driver.Value
	done  bool
}

func (r *evidenceScriptRows) Columns() []string { return []string{"value"} }
func (r *evidenceScriptRows) Close() error      { return nil }
func (r *evidenceScriptRows) Next(into []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	into[0] = r.value
	return nil
}

func TestConnectionReceiptRejectsDifferentEngine(t *testing.T) {
	db := sql.OpenDB(evidenceScript{engine: "3.53.5"})
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	r, err := CaptureConnection(t.Context(), conn, cfg)
	if !errors.Is(err, ErrConnectionEvidence) || r.PolicyValid || r.Actual == nil || r.Actual.Engine != "3.53.5" {
		t.Fatalf("different engine claimed policy valid or lost actual readback: %+v %v", r, err)
	}
	logConnectionEvidence(t, r)
}

func TestConnectionReceiptPartialReadIsNull(t *testing.T) {
	db := sql.OpenDB(evidenceScript{engine: "3.53.4", failQuery: "PRAGMA cache_size"})
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	r, err := CaptureConnection(t.Context(), conn, cfg)
	if !errors.Is(err, io.ErrUnexpectedEOF) || !errors.Is(err, ErrConnectionEvidence) || r.PolicyValid || r.Actual != nil || r.Error == "" {
		t.Fatalf("partial read became complete/default-valued evidence: %+v %v", r, err)
	}
	logConnectionEvidence(t, r)
}
