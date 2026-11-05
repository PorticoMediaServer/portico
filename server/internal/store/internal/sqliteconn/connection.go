// Package sqliteconn applies schema-v1 connection policy to every physical
// bundled-SQLite connection, including replacements made by database/sql.
// Handles stay inside store's private drivers; they are not worker APIs.
package sqliteconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

type Role uint8

const (
	Writer Role = iota + 1
	Reader
	Checkpointer
)

type SyncMode uint8

const (
	Normal SyncMode = 1
	Full   SyncMode = 2
)

// Config specifies an explicit pool and per-connection memory budget. CacheKiB
// is SQLite's negative cache_size value (KiB rather than pages), not an RSS cap.
// Writer and checkpointer pools each have exactly one physical connection.
type Config struct {
	Role        Role
	Connections int
	CacheKiB    int32
	Synchronous SyncMode
}

var ErrPolicy = errors.New("schema-v1 connection policy mismatch")

func (c Config) validate() error {
	if c.Role < Writer || c.Role > Checkpointer || c.CacheKiB <= 0 || c.Connections <= 0 ||
		(c.Role != Reader && c.Connections != 1) || (c.Synchronous != Normal && c.Synchronous != Full) {
		return ErrPolicy
	}
	return nil
}

// Connector accepts an absolute file path, never a caller-supplied DSN or SQL.
// All per-connection settings are in the connector's immutable DSN. No global
// connection hook is installed and no existing pool is modified.
func Connector(path string, c Config) (driver.Connector, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	u, err := fileURI(path)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	mode := "rw"
	if c.Role == Writer {
		mode = "rwc"
	}
	if c.Role == Reader {
		mode = "ro"
	}
	q.Set("mode", mode)
	q.Set("_busy_timeout", "0") // refusal, never wait in SQLite's busy handler
	q.Set("_foreign_keys", "1")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", strconv.Itoa(int(c.Synchronous)))
	q.Set("_query_only", "0")
	if c.Role == Reader {
		q.Set("_query_only", "1")
	}
	q.Add("_pragma", "wal_autocheckpoint(0)")
	q.Add("_pragma", fmt.Sprintf("cache_size(-%d)", c.CacheKiB))
	q.Add("_pragma", "mmap_size(0)")
	// FILE keeps large temporary sorts off the 2 GB RAM budget. This is a
	// spill policy, not a disk quota: GV still governs scratch bytes and IO.
	q.Add("_pragma", "temp_store(FILE)")
	u.RawQuery = q.Encode()
	return sqlite.NewConnector(u.String())
}

// fileURI encodes local absolute paths independently of the build host, so the
// Windows drive grammar can be tested on Unix too. A drive is part of the path,
// never the URI authority: SQLite expects file:///C:/directory/catalog.sqlite.
// UNC/device paths are deliberately refused; no URI-authority build extension
// or network-file locking behavior is assumed.
func fileURI(path string) (url.URL, error) {
	return fileURIForPlatform(path, runtime.GOOS == "windows")
}

func fileURIForPlatform(path string, windows bool) (url.URL, error) {
	if strings.ContainsRune(path, 0) {
		return url.URL{}, fmt.Errorf("%w: database path contains NUL", ErrPolicy)
	}
	if windows {
		// Unix backslashes are filename bytes; only Windows normalizes them.
		normalized := strings.ReplaceAll(path, `\`, "/")
		if strings.HasPrefix(normalized, "//") {
			return url.URL{}, fmt.Errorf("%w: UNC and device paths are unsupported", ErrPolicy)
		}
		drive := len(normalized) >= 3 && ((normalized[0] >= 'A' && normalized[0] <= 'Z') || (normalized[0] >= 'a' && normalized[0] <= 'z')) && normalized[1] == ':' && normalized[2] == '/'
		if !drive {
			return url.URL{}, fmt.Errorf("%w: Windows database path requires an explicit drive-letter absolute path", ErrPolicy)
		}
		path = "/" + normalized
	} else if !strings.HasPrefix(path, "/") {
		return url.URL{}, ErrPolicy
	}
	return url.URL{Scheme: "file", Path: path}, nil
}

// Open performs a startup self-check on an actual acquired connection. Every
// subsequent physical connection receives the SAME settings from Connector.
// Drivers/harnesses must also Assert at each acquisition boundary and record
// the returned values with qualification receipts.
func Open(ctx context.Context, path string, c Config) (*sql.DB, Settings, error) {
	connector, err := Connector(path, c)
	if err != nil {
		return nil, Settings{}, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(c.Connections)
	db.SetMaxIdleConns(c.Connections)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, Settings{}, err
	}
	settings, checkErr := Assert(ctx, conn, c)
	closeErr := conn.Close()
	if err := errors.Join(checkErr, closeErr); err != nil {
		db.Close()
		return nil, settings, err
	}
	return db, settings, nil
}

// Settings is values-only evidence, including actual engine and file mode.
// WAL file allocation, checkpoint debt and resident memory are separate metrics.
type Settings struct {
	Engine         string
	JournalMode    string
	ForeignKeys    int
	Synchronous    int
	AutoCheckpoint int
	CacheSize      int
	BusyTimeout    int
	MmapSize       int64
	QueryOnly      int
	TempStore      int
}

// Assert reads, never repairs, policy. Call outside a transaction and after a
// command's private durability selection. A mismatch invalidates the sample.
func Assert(ctx context.Context, conn *sql.Conn, c Config) (Settings, error) {
	var s Settings
	if conn == nil {
		return s, ErrPolicy
	}
	if err := c.validate(); err != nil {
		return s, err
	}
	for _, query := range []struct {
		sql  string
		dest any
	}{
		{"SELECT sqlite_version()", &s.Engine}, {"PRAGMA journal_mode", &s.JournalMode},
		{"PRAGMA foreign_keys", &s.ForeignKeys}, {"PRAGMA synchronous", &s.Synchronous},
		{"PRAGMA wal_autocheckpoint", &s.AutoCheckpoint}, {"PRAGMA cache_size", &s.CacheSize},
		{"PRAGMA busy_timeout", &s.BusyTimeout}, {"PRAGMA mmap_size", &s.MmapSize}, {"PRAGMA query_only", &s.QueryOnly},
		{"PRAGMA temp_store", &s.TempStore},
	} {
		if err := conn.QueryRowContext(ctx, query.sql).Scan(query.dest); err != nil {
			return s, err
		}
	}
	queryOnly := 0
	if c.Role == Reader {
		queryOnly = 1
	}
	if s.JournalMode != "wal" || s.ForeignKeys != 1 || s.Synchronous != int(c.Synchronous) ||
		s.AutoCheckpoint != 0 || s.CacheSize != -int(c.CacheKiB) || s.BusyTimeout != 0 || s.MmapSize != 0 || s.QueryOnly != queryOnly || s.TempStore != 1 {
		return s, ErrPolicy
	}
	return s, nil
}
