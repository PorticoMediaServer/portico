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
	ReadLimits  ReadLimits `json:",omitzero"`
	// Zero is unobserved. A nonzero value ASSERTS the existing main database's
	// page size on each acquired Writer connection; it never changes that size.
	WriterPageSize int32 `json:",omitzero"`
}

var ErrPolicy = errors.New("schema-v1 connection policy mismatch")

// FileMode narrows an opening to an existing file. It does not change the
// connection policy or authorize a schema/row; the trusted opening must still
// assert the canonical layout and bind the actual file identity.
type FileMode uint8

const ExistingFile FileMode = 1

func (c Config) validate() error {
	if c.Role < Writer || c.Role > Checkpointer || c.CacheKiB <= 0 || c.Connections <= 0 ||
		(c.Role != Reader && c.Connections != 1) || (c.Synchronous != Normal && c.Synchronous != Full) {
		return ErrPolicy
	}
	if c.ReadLimits != (ReadLimits{}) && (c.Role != Reader || c.ReadLimits.LengthBytes <= 0 || c.ReadLimits.Columns <= 0) {
		return fmt.Errorf("%w: read limits require Reader and two positive limits", ErrPolicy)
	}
	if c.WriterPageSize != 0 && (c.Role != Writer || c.WriterPageSize < 512 || c.WriterPageSize > 65536 || c.WriterPageSize&(c.WriterPageSize-1) != 0) {
		return fmt.Errorf("%w: WriterPageSize requires Writer and a native power-of-two page size", ErrPolicy)
	}
	return nil
}

// Connector accepts an absolute file path, never a caller-supplied DSN or SQL.
// PRAGMAs are in the immutable DSN; opt-in read limits are set once on each
// raw physical Connect. ExistingFile refuses creation on EVERY replacement;
// without it the original fresh-writer mode remains unchanged. No global hook
// or existing pool is modified.
func Connector(path string, c Config, modes ...FileMode) (driver.Connector, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if len(modes) > 1 || (len(modes) == 1 && modes[0] != ExistingFile) {
		return nil, ErrPolicy
	}
	u, err := fileURI(path)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	mode := "rw"
	if c.Role == Writer && len(modes) == 0 {
		mode = "rwc"
	}
	if c.Role == Reader {
		mode = "ro"
	}
	q.Set("mode", mode)
	q.Set("_busy_timeout", "0") // refusal, never wait in SQLite's busy handler
	q.Set("_foreign_keys", "1")
	q.Set("_dqs", "0") // disable BOTH legacy DDL/DML string fallbacks before preparation
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", strconv.Itoa(int(c.Synchronous)))
	q.Set("_query_only", "0")
	if c.Role == Reader {
		q.Set("_query_only", "1")
	}
	q.Add("_pragma", "wal_autocheckpoint(0)")
	q.Add("_pragma", fmt.Sprintf("cache_size(-%d)", c.CacheKiB))
	q.Add("_pragma", "mmap_size(0)")
	q.Add("_pragma", "ignore_check_constraints(0)")
	q.Add("_pragma", "defer_foreign_keys(0)")
	q.Add("_pragma", "writable_schema(0)")
	q.Add("_pragma", "read_uncommitted(0)")
	// FILE keeps large temporary sorts off the 2 GB RAM budget. This is a
	// spill policy, not a disk quota: GV still governs scratch bytes and IO.
	q.Add("_pragma", "temp_store(FILE)")
	u.RawQuery = q.Encode()
	base, err := sqlite.NewConnector(u.String())
	if err != nil || c.ReadLimits == (ReadLimits{}) {
		return base, err
	}
	return &limitConnector{Connector: base, limits: c.ReadLimits}, nil
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
func Open(ctx context.Context, path string, c Config, modes ...FileMode) (*sql.DB, Settings, error) {
	connector, err := Connector(path, c, modes...)
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
	// OFF is zero and preserves existing plan-golden JSON. Nonzero drift is
	// retained as evidence, and Assert refuses it rather than repairing it.
	DQSDDL int `json:",omitempty"`
	DQSDML int `json:",omitempty"`
	// ReadLimits is actual native evidence when configured, otherwise zero
	// (unobserved). The zero policy neither changes nor asserts native defaults.
	ReadLimits ReadLimits `json:",omitzero"`
	// Actual same-connection evidence only when explicitly requested. Keep
	// zero omitted so existing callers and complete Capture JSON stay exact.
	WriterPageSize int32 `json:",omitzero"`
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
	var err error
	s.DQSDDL, s.DQSDML, err = sqlite.DQS(conn)
	if err != nil || s.DQSDDL != 0 || s.DQSDML != 0 {
		return s, errors.Join(fmt.Errorf("%w: DQS DDL=%d DML=%d", ErrPolicy, s.DQSDDL, s.DQSDML), err)
	}
	enforcement, err := AssertEnforcement(ctx, conn)
	s.ForeignKeys = enforcement.ForeignKeys
	if err != nil {
		return s, err
	}
	for _, query := range []struct {
		sql  string
		dest any
	}{
		{"SELECT sqlite_version()", &s.Engine}, {"PRAGMA journal_mode", &s.JournalMode},
		{"PRAGMA synchronous", &s.Synchronous},
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
	if s.JournalMode != "wal" || s.Synchronous != int(c.Synchronous) ||
		s.AutoCheckpoint != 0 || s.CacheSize != -int(c.CacheKiB) || s.BusyTimeout != 0 || s.MmapSize != 0 || s.QueryOnly != queryOnly || s.TempStore != 1 {
		return s, ErrPolicy
	}
	if c.ReadLimits != (ReadLimits{}) {
		limits, err := readLimits(conn)
		s.ReadLimits = limits
		if err != nil {
			return s, err
		}
		if limits != c.ReadLimits {
			return s, fmt.Errorf("%w: native read limits: got %+v want %+v", ErrPolicy, limits, c.ReadLimits)
		}
	}
	if c.WriterPageSize != 0 {
		if err := conn.QueryRowContext(ctx, "PRAGMA main.page_size").Scan(&s.WriterPageSize); err != nil {
			return s, err
		}
		if s.WriterPageSize != c.WriterPageSize {
			return s, fmt.Errorf("%w: writer page size: got %d want %d", ErrPolicy, s.WriterPageSize, c.WriterPageSize)
		}
	}
	return s, nil
}
