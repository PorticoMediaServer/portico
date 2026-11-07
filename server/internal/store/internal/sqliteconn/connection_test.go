package sqliteconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type countedConnector struct {
	driver.Connector
	opened atomic.Int64
}

func (c *countedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err == nil {
		c.opened.Add(1)
	}
	return conn, err
}

func fixture(t *testing.T) (string, *sql.DB, Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source %#&.sqlite")
	cfg := Config{Role: Writer, Connections: 1, CacheKiB: 1024, Synchronous: Normal}
	db, settings, err := Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if settings.Engine != "3.53.4" {
		t.Fatalf("qualification control engine changed: %s", settings.Engine)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE parent(id INTEGER PRIMARY KEY);CREATE TABLE child(parent_id INTEGER REFERENCES parent(id));INSERT INTO parent VALUES(1);INSERT INTO child VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	return path, db, cfg
}

func TestFileURIAbsolutePaths(t *testing.T) {
	for _, tc := range []struct {
		path, want string
		windows    bool
	}{
		{`C:\Portico\catalog.sqlite`, "file:///C:/Portico/catalog.sqlite", true},
		{"C:/Portico/catalog.sqlite", "file:///C:/Portico/catalog.sqlite", true},
		{`d:\Portico files\100% #.sqlite`, "file:///d:/Portico%20files/100%25%20%23.sqlite", true},
		{"/home/justin/catalog.sqlite", "file:///home/justin/catalog.sqlite", false},
		{"/home/justin/source ?#% name.sqlite", "file:///home/justin/source%20%3F%23%25%20name.sqlite", false},
		{`/home/justin/back\slash.sqlite`, "file:///home/justin/back%5Cslash.sqlite", false},
		{`/\server\share`, "file:///%5Cserver%5Cshare", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			u, err := fileURIForPlatform(tc.path, tc.windows)
			if err != nil {
				t.Fatal(err)
			}
			if got := u.String(); got != tc.want {
				t.Fatalf("got%q want%q", got, tc.want)
			}
			parsed, err := url.Parse(u.String())
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Host != "" || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.Path != u.Path {
				t.Fatalf("path escaped into another URI component: %+v", parsed)
			}
		})
	}
	for _, windows := range []bool{false, true} {
		for _, path := range []string{`\\server\share\catalog.sqlite`, `\\?\C:\catalog.sqlite`, `\/?/C:`, `\\.\C:\catalog.sqlite`, `C:relative.sqlite`, `\Portico\catalog.sqlite`, "relative.sqlite", "", "a\x00b"} {
			if _, err := fileURIForPlatform(path, windows); !errors.Is(err, ErrPolicy) {
				t.Fatalf("unsafe/unsupported path%q windows%v: %v", path, windows, err)
			}
		}
		if _, err := fileURIForPlatform("a\x00b", windows); err == nil || !strings.Contains(err.Error(), "NUL") {
			t.Fatalf("unclear NUL error: %v", err)
		}
	}
	for _, path := range []string{"//server/share/catalog.sqlite", `/\server\share`, `//?/`, `/\?\C:\catalog.sqlite`} {
		if _, err := fileURIForPlatform(path, true); !errors.Is(err, ErrPolicy) {
			t.Fatalf("Windows network/device path%q: %v", path, err)
		}
	}
	for _, path := range []string{"/Portico/x.sqlite", `\Portico\x.sqlite`, "/C:/Portico/x.sqlite", "C:", "C:foo"} {
		if _, err := fileURIForPlatform(path, true); !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "drive-letter") {
			t.Fatalf("Windows non-drive path%q: %v", path, err)
		}
	}
}

func assertConn(t *testing.T, db *sql.DB, cfg Config) *sql.Conn {
	t.Helper()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if settings, err := Assert(t.Context(), conn, cfg); err != nil {
		conn.Close()
		t.Fatalf("assert: %+v: %v", settings, err)
	}
	return conn
}

func requireFK787(t *testing.T, conn *sql.Conn) {
	t.Helper()
	_, err := conn.ExecContext(t.Context(), "DELETE FROM parent WHERE id=1")
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code() != 787 {
		t.Fatalf("expected SQLITE_CONSTRAINT_FOREIGNKEY 787, got %v", err)
	}
}

func TestReplacementRestoresEveryConnectionSetting(t *testing.T) {
	path, original, cfg := fixture(t)
	if err := original.QueryRowContext(t.Context(), "PRAGMA main.page_size").Scan(&cfg.WriterPageSize); err != nil {
		t.Fatal(err)
	}
	base, err := Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	counted := &countedConnector{Connector: base}
	db := sql.OpenDB(counted)
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn := assertConn(t, db, cfg)
	if got, err := Assert(t.Context(), conn, cfg); err != nil || got.WriterPageSize != cfg.WriterPageSize {
		t.Fatal("initial physical connection lacks page evidence", got, err)
	}
	// A security turn changes only its private connection. A subsequent physical
	// replacement must still start from the configured NORMAL policy.
	if _, err := conn.ExecContext(t.Context(), "PRAGMA synchronous=FULL"); err != nil {
		t.Fatal(err)
	}
	full := cfg
	full.Synchronous = Full
	if _, err := Assert(t.Context(), conn, full); err != nil {
		t.Fatal(err)
	}
	if _, err := Assert(t.Context(), conn, cfg); !errors.Is(err, ErrPolicy) {
		t.Fatal("assert silently repaired drift", err)
	}
	if _, err := conn.ExecContext(t.Context(), "PRAGMA synchronous=NORMAL; PRAGMA temp_store=MEMORY"); err != nil {
		t.Fatal(err)
	}
	if settings, err := Assert(t.Context(), conn, cfg); !errors.Is(err, ErrPolicy) || settings.TempStore != 2 {
		t.Fatalf("temp_store drift was not detected: %+v %v", settings, err)
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	conn.Close()
	conn = assertConn(t, db, cfg)
	defer conn.Close()
	if got, err := Assert(t.Context(), conn, cfg); err != nil || got.WriterPageSize != cfg.WriterPageSize {
		t.Fatal("replacement lacks same-connection page evidence", got, err)
	}
	if counted.opened.Load() != 2 {
		t.Fatal("physical replacement not exercised", counted.opened.Load())
	}
	requireFK787(t, conn)
}

func TestCancelledReadReopensWithPolicy(t *testing.T) {
	path, _, cfg := fixture(t)
	base, err := Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	counted := &countedConnector{Connector: base}
	db := sql.OpenDB(counted)
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn := assertConn(t, db, cfg)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	var n int
	err = conn.QueryRowContext(ctx, `WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq) SELECT sum(x) FROM seq`).Scan(&n)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	conn.Close()
	conn = assertConn(t, db, cfg)
	defer conn.Close()
	if counted.opened.Load() != 2 {
		t.Fatalf("cancel did not exercise physical replacement: %d connections", counted.opened.Load())
	}
	requireFK787(t, conn)
}

func TestReadersAndCheckpointerSharePolicy(t *testing.T) {
	path, _, cfg := fixture(t)
	for _, role := range []Role{Reader, Checkpointer} {
		t.Run(map[Role]string{Reader: "reader", Checkpointer: "checkpointer"}[role], func(t *testing.T) {
			c := cfg
			c.Role = role
			db, _, err := Open(t.Context(), path, c)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			conn := assertConn(t, db, c)
			// Exercise a second physical reader/checkpointer as well as startup.
			if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			conn.Close()
			conn = assertConn(t, db, c)
			defer conn.Close()
			if role == Reader {
				if _, err := conn.ExecContext(t.Context(), "INSERT INTO parent VALUES(2)"); err == nil {
					t.Fatal("reader was writable")
				}
				// Even a caller clearing query_only cannot defeat mode=ro.
				if _, err := conn.ExecContext(t.Context(), "PRAGMA query_only=0"); err != nil {
					t.Fatal(err)
				}
				if _, err := conn.ExecContext(t.Context(), "INSERT INTO parent VALUES(2)"); err == nil {
					t.Fatal("read-only file became writable")
				}
			} else {
				var busy, log, checkpointed int
				if err := conn.QueryRowContext(t.Context(), "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &checkpointed); err != nil {
					t.Fatal(err)
				}
				if busy != 0 || log <= 0 || checkpointed != log {
					t.Fatalf("PASSIVE did not drain unpinned WAL: busy=%d log=%d checkpointed=%d", busy, log, checkpointed)
				}
			}
		})
	}
}

func TestReaderPoolGrowthAssertsEveryPhysicalConnection(t *testing.T) {
	path, _, cfg := fixture(t)
	cfg.Role = Reader
	cfg.Connections = 3
	db, _, err := Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var held []*sql.Conn
	defer func() {
		for _, conn := range held {
			conn.Close()
		}
	}()
	for range 3 {
		held = append(held, assertConn(t, db, cfg))
	}
	if db.Stats().OpenConnections != 3 {
		t.Fatalf("reader pool did not grow: %+v", db.Stats())
	}
	for _, conn := range held {
		if _, err := conn.ExecContext(t.Context(), "INSERT INTO parent VALUES(2)"); err == nil {
			t.Fatal("grown reader was writable")
		}
	}
	if err := held[0].Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	held[0].Close()
	held[0] = assertConn(t, db, cfg)
	if db.Stats().OpenConnections != 3 {
		t.Fatalf("reader replacement did not retain pool: %+v", db.Stats())
	}
}

func TestConnectionRejectsUnknownPolicyAndMissingReadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sqlite")
	for _, cfg := range []Config{{}, {Role: Writer, Connections: 2, CacheKiB: 1, Synchronous: Normal}, {Role: Reader, Connections: 1, CacheKiB: 0, Synchronous: Normal}, {Role: Writer, Connections: 1, CacheKiB: 1, Synchronous: 3}} {
		if _, err := Connector(path, cfg); !errors.Is(err, ErrPolicy) {
			t.Fatal(cfg, err)
		}
	}
	cfg := Config{Role: Reader, Connections: 1, CacheKiB: 1, Synchronous: Normal}
	if _, err := Connector("relative.sqlite", cfg); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	if db, _, err := Open(t.Context(), path, cfg); err == nil {
		db.Close()
		t.Fatal("reader created absent database")
	}
	if _, err := Assert(t.Context(), nil, cfg); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
}
