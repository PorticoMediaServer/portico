package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"

	// The pure-Go driver: no cgo, so every server target cross-compiles.
	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// The observed driver is registered once, at package init, so every handle this
// package opens is instrumented and none of the call sites have to know.
func init() {
	inner, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic("dbwork: sqlite driver unavailable: " + err.Error())
	}
	base := inner.Driver()
	_ = inner.Close()
	sql.Register(DriverName, observedDriver{inner: base})
}

var _ driver.Driver = observedDriver{}

// OpenHandle creates the database file with private permissions and opens it
// under the validated policy. The policy's per-connection pragmas travel in the
// DSN so every pooled connection inherits them, and the same set is executed once
// after open so the values are also visible to a diagnostics read.
func OpenHandle(path string, policy Policy) (*sql.DB, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Create the file private if it is absent, and never open one that exists:
	// closing any descriptor of a database file drops every POSIX lock this
	// process holds on it, so a second handle (the watchdog's reopen) would take
	// the live handle's locks away, and another connection could then rebuild
	// the WAL index under its mapping (the retired server's SIGBUS, 2 Sep).
	created := false
	if file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
		file.Close()
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if created || info.Size() == 0 {
		// Establish the file format once, before WAL creates the header. It is
		// intentionally absent from the DSN: repeated assignments on existing
		// connections can mutate the file being preserved for restore rollback.
		initial, err := sql.Open("sqlite", sqliteFileURI(path))
		if err != nil {
			return nil, err
		}
		initial.SetMaxOpenConns(1)
		_, err = initial.Exec("PRAGMA auto_vacuum=INCREMENTAL; PRAGMA application_id=0")
		closeErr := initial.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	dsn, err := policy.readerDSN(path)
	if err != nil {
		return nil, err
	}
	writerDSN, err := policy.WriterDSN(path)
	if err != nil {
		return nil, err
	}
	writer, err := sql.Open(DriverName, writerDSN)
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	background, err := sql.Open(DriverName, dsn)
	if err != nil {
		writer.Close()
		return nil, err
	}
	background.SetMaxOpenConns(1)
	background.SetMaxIdleConns(1)
	background.SetConnMaxLifetime(policy.ConnMaxLifetime)
	// The gate owns admission; readers never borrow this connection.
	db := sql.OpenDB(&splitConnector{dsn: dsn, driver: &splitDriver{Driver: writer.Driver(), dsn: dsn, writer: writer, background: background}})
	if err = policy.Apply(db); err != nil {
		db.Close()
		return nil, err
	}
	for _, pragma := range policy.RuntimePragmas() {
		if _, err = InstallRetry.Do(context.Background(), func() error {
			_, execErr := db.Exec(pragma)
			return execErr
		}); err != nil {
			db.Close()
			return nil, err
		}
	}
	var pageSize int64
	if err = db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		db.Close()
		return nil, err
	}
	var native bool
	var scope *readerScope
	conn, err := writer.Conn(context.Background())
	if err != nil {
		db.Close()
		return nil, err
	}
	err = conn.Raw(func(inner any) error {
		if c, ok := inner.(*observedConn); ok {
			scope = c.readers
			inner = c.inner
		}
		if counter, ok := inner.(interface{ WALWriteBytes() (uint64, bool) }); ok {
			_, native = counter.WALWriteBytes()
		}
		return nil
	})
	_ = conn.Close()
	if err != nil {
		db.Close()
		return nil, err
	}
	nativePath := DatabaseFile(context.Background(), db)
	if nativePath == "" {
		db.Close()
		return nil, errors.New("dbwork: live database filename unavailable")
	}
	l := newWALLedger(nativePath, pageSize)
	l.native = native
	l = shareWALLedger(scope, l)
	db.Driver().(*splitDriver).wal = l
	return db, nil
}

// PoolStats is the observable pool state. WaitCount and WaitDuration are the
// numbers that say whether the pool is the bottleneck: they count callers that
// found every connection busy, which a lane rejection never shows.
type PoolStats struct {
	MaxOpenConnections int   `json:"maxOpenConnections"`
	OpenConnections    int   `json:"openConnections"`
	InUse              int   `json:"inUse"`
	Idle               int   `json:"idle"`
	WaitCount          int64 `json:"waitCount"`
	WaitMillis         int64 `json:"waitMillis"`
	MaxIdleClosed      int64 `json:"maxIdleClosed"`
	MaxLifetimeClosed  int64 `json:"maxLifetimeClosed"`
}

// Pool snapshots database/sql pool counters.
func Pool(db *sql.DB) PoolStats {
	if db == nil {
		return PoolStats{}
	}
	stats := db.Stats()
	return PoolStats{
		MaxOpenConnections: stats.MaxOpenConnections,
		OpenConnections:    stats.OpenConnections,
		InUse:              stats.InUse,
		Idle:               stats.Idle,
		WaitCount:          stats.WaitCount,
		WaitMillis:         stats.WaitDuration.Milliseconds(),
		MaxIdleClosed:      stats.MaxIdleClosed,
		MaxLifetimeClosed:  stats.MaxLifetimeClosed,
	}
}
