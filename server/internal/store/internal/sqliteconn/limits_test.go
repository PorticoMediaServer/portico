package sqliteconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Preserve the driver's contextual methods while interposing on the optional
// limit interface, as the owned snapshot connector does in production.
type limitTestConn interface {
	driver.Conn
	driver.ConnPrepareContext
	driver.ConnBeginTx
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
	nativeLimiter
}

type limitObserver struct {
	driver.Connector
	opens, sets, gets atomic.Int64
}

type observedLimitConn struct {
	limitTestConn
	observer *limitObserver
}

func (c *limitObserver) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	c.opens.Add(1)
	return &observedLimitConn{conn.(limitTestConn), c}, nil
}

func (c *observedLimitConn) SQLiteLimit(id, value int) (int, error) {
	if value < 0 {
		c.observer.gets.Add(1)
	} else {
		c.observer.sets.Add(1)
	}
	return c.limitTestConn.SQLiteLimit(id, value)
}

func limitedReader(t *testing.T, path string, cfg Config) (*sql.DB, *limitObserver) {
	t.Helper()
	connector, err := Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy := connector.(*limitConnector)
	observed := &limitObserver{Connector: policy.Connector}
	policy.Connector = observed
	db := sql.OpenDB(policy)
	db.SetMaxOpenConns(cfg.Connections)
	db.SetMaxIdleConns(cfg.Connections)
	t.Cleanup(func() { db.Close() })
	return db, observed
}

func TestReadLimitsRejectPartialAndNonReaderPolicy(t *testing.T) {
	path, _, cfg := fixture(t)
	for _, limits := range []ReadLimits{{LengthBytes: 1024}, {Columns: 8}, {LengthBytes: -1, Columns: 8}, {LengthBytes: 1024, Columns: -1}} {
		cfg.Role, cfg.ReadLimits = Reader, limits
		if _, err := Connector(path, cfg); !errors.Is(err, ErrPolicy) {
			t.Fatalf("accepted partial/negative limits %+v: %v", limits, err)
		}
	}
	for _, role := range []Role{Writer, Checkpointer} {
		cfg.Role, cfg.ReadLimits = role, ReadLimits{LengthBytes: 65536, Columns: 64}
		if _, err := Connector(path, cfg); !errors.Is(err, ErrPolicy) {
			t.Fatalf("accepted role %d: %v", role, err)
		}
	}
	// SQLite clamps above its compiled hard limits. A requested value must
	// never become false Settings evidence or an unexpected reservation.
	cfg.Role, cfg.ReadLimits = Reader, ReadLimits{LengthBytes: math.MaxInt32, Columns: 64}
	if db, _, err := Open(t.Context(), path, cfg); !errors.Is(err, ErrPolicy) {
		if db != nil {
			db.Close()
		}
		t.Fatalf("silently clamped a hard-limit overflow: %v", err)
	}
}

func TestReadLimitsGrowthDriftAndReplacement(t *testing.T) {
	path, _, cfg := fixture(t)
	cfg.Role, cfg.Connections, cfg.ReadLimits = Reader, 3, ReadLimits{LengthBytes: 65536, Columns: 64}
	db, observed := limitedReader(t, path, cfg)
	var held []*sql.Conn
	defer func() {
		for _, conn := range held {
			conn.Close()
		}
	}()
	for range 3 {
		conn := assertConn(t, db, cfg)
		held = append(held, conn)
		settings, err := Assert(t.Context(), conn, cfg)
		if err != nil || settings.ReadLimits != cfg.ReadLimits {
			t.Fatalf("actual limits: %+v %v", settings, err)
		}
	}
	if observed.opens.Load() != 3 || observed.sets.Load() != 6 || observed.gets.Load() < 6 {
		t.Fatalf("limits not fixed once per physical Connect: opens%d sets%d gets%d", observed.opens.Load(), observed.sets.Load(), observed.gets.Load())
	}
	for _, drift := range []struct{ id, value int }{{sqlite3.SQLITE_LIMIT_LENGTH, 65535}, {sqlite3.SQLITE_LIMIT_COLUMN, 63}} {
		before, err := sqlite.Limit(held[0], drift.id, drift.value)
		if err != nil {
			t.Fatal(err)
		}
		setCount := observed.sets.Load()
		settings, err := Assert(t.Context(), held[0], cfg)
		if !errors.Is(err, ErrPolicy) || settings.ReadLimits == cfg.ReadLimits {
			t.Fatalf("missed native drift: %+v %v", settings, err)
		}
		actual, err := sqlite.Limit(held[0], drift.id, -1)
		if err != nil || actual != drift.value || observed.sets.Load() != setCount {
			t.Fatalf("Assert repaired native limits: %d %v", actual, err)
		}
		if _, err := sqlite.Limit(held[0], drift.id, before); err != nil {
			t.Fatal(err)
		}
	}
	if err := held[0].Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	held[0].Close()
	beforeSets := observed.sets.Load()
	held[0] = assertConn(t, db, cfg)
	if observed.opens.Load() != 4 || observed.sets.Load() != beforeSets+2 || db.Stats().OpenConnections != 3 {
		t.Fatalf("replacement did not receive both fixed limits: %+v opens%d sets%d", db.Stats(), observed.opens.Load(), observed.sets.Load())
	}
}

func TestReadLimitsNativeBoundariesAndCancelledReplacement(t *testing.T) {
	path, _, cfg := fixture(t)
	cfg.Role, cfg.ReadLimits = Reader, ReadLimits{LengthBytes: 65536, Columns: 64}
	db, observed := limitedReader(t, path, cfg)
	conn := assertConn(t, db, cfg)
	defer func() { conn.Close() }()
	var blob []byte
	if err := conn.QueryRowContext(t.Context(), "SELECT zeroblob(?)", int64(cfg.ReadLimits.LengthBytes)).Scan(&blob); err != nil || len(blob) != int(cfg.ReadLimits.LengthBytes) {
		t.Fatalf("exact length boundary: %d %v", len(blob), err)
	}
	err := conn.QueryRowContext(t.Context(), "SELECT zeroblob(?)", int64(cfg.ReadLimits.LengthBytes)+1).Scan(&blob)
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code() != sqlite3.SQLITE_TOOBIG {
		t.Fatalf("length+1 was not refused by native SQLite: %v", err)
	}
	for _, n := range []int{64, 65} {
		rows, err := conn.QueryContext(t.Context(), "SELECT "+strings.Repeat("1,", n-1)+"1")
		if n == 64 {
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.Columns()
			rows.Close()
			if err != nil || len(columns) != n {
				t.Fatalf("exact column boundary: %d %v", len(columns), err)
			}
		} else if err == nil {
			rows.Close()
			t.Fatal("column+1 was not refused")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	var sum int64
	err = conn.QueryRowContext(ctx, `WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq) SELECT sum(x) FROM seq`).Scan(&sum)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	conn.Close()
	conn = assertConn(t, db, cfg)
	if observed.opens.Load() != 2 || observed.sets.Load() != 4 {
		t.Fatalf("cancel replacement missing fixed policy: opens%d sets%d", observed.opens.Load(), observed.sets.Load())
	}
}

func TestZeroReadLimitsPreservesStricterNativePolicy(t *testing.T) {
	path, _, cfg := fixture(t)
	for _, role := range []Role{Writer, Reader, Checkpointer} {
		cfg.Role = role
		db, _, err := Open(t.Context(), path, cfg)
		if err != nil {
			t.Fatal(err)
		}
		conn := assertConn(t, db, cfg)
		for _, limit := range []struct{ id, value int }{{sqlite3.SQLITE_LIMIT_LENGTH, 65536}, {sqlite3.SQLITE_LIMIT_COLUMN, 64}} {
			if _, err := sqlite.Limit(conn, limit.id, limit.value); err != nil {
				t.Fatal(err)
			}
		}
		settings, err := Assert(t.Context(), conn, cfg)
		actual, readErr := readLimits(conn)
		if err != nil || readErr != nil || settings.ReadLimits != (ReadLimits{}) || actual != (ReadLimits{65536, 64}) {
			t.Fatalf("zero policy reset/asserted native defaults for role%d: %+v actual%+v %v %v", role, settings, actual, err, readErr)
		}
		conn.Close()
		db.Close()
	}
}
