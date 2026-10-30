package dbwork

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Policy is the validated SQLite runtime preset. It is intentionally
// conservative: SQLite serialises writers, so a large pool only multiplies lock
// competition and each connection's private page cache. The preset keeps the
// configured page-cache allowance at 36 MiB (6 foreground + 2 background + 1
// writer connections x 4 MiB). Every allowed foreground connection stays idle
// between bursts: closing one discards both its page cache and prepared
// bytecode, forcing schema/statement parsing on the next request burst.
// Retaining six instead of four idle handles raises resting usage to the
// already-permitted burst peak, not the concurrent capacity or memory ceiling.
// Database mmap is disabled; SQLite's allocator, retained statements and
// temporary sorts still use memory outside the Go runtime's soft limit.
type Policy struct {
	MaxOpenConns      int
	MaxIdleConns      int
	CacheSizeKiB      int64
	MmapSizeBytes     int64
	ConnMaxLifetime   time.Duration
	BusyTimeoutMilli  int
	WALAutocheckpoint int
	// WriterBusyTimeoutMilli is the writer connection's own busy timeout.
	// The gate has already admitted exactly one writer, so the writer should
	// never wait on SQLite's lock; when it does, an outside opener or an
	// ungated write holds it, and a short timeout reports that as a typed,
	// retryable writer_capacity instead of a hidden 15 s stall.
	WriterBusyTimeoutMilli int
}

const (
	safeMaxOpenConns = 6
	// Retain the already-allowed reader peak and its per-connection bytecode.
	safeMaxIdleConns      = safeMaxOpenConns
	safeCacheSizeKiB      = 4 * 1024 // 4 MiB per connection
	safeMmapSizeBytes     = 0        // mmap deliberately off; it is untracked memory
	safeConnMaxLifetime   = 30 * time.Minute
	safeBusyTimeoutMilli  = 15000
	safeWALAutocheckpoint = 1000
	// safeWriterBusyTimeoutMilli (F §3) applies only to the writer connection,
	// from the moment it is created; readers keep safeBusyTimeoutMilli.
	safeWriterBusyTimeoutMilli = 25
)

// ErrPolicy reports a resource policy that would exceed the validated envelope.
var ErrPolicy = errors.New("sqlite resource policy out of range")

// DefaultPolicy is the only policy production opens with. It deliberately takes
// no configuration input: the server has no validated host-memory budget, and
// unrelated settings such as an upload limit must never be used as a proxy for
// one. Raising these numbers is an evidence-backed change, made here, with the
// load test re-run.
func DefaultPolicy() Policy {
	return Policy{
		MaxOpenConns:      safeMaxOpenConns,
		MaxIdleConns:      safeMaxIdleConns,
		CacheSizeKiB:      safeCacheSizeKiB,
		MmapSizeBytes:     safeMmapSizeBytes,
		ConnMaxLifetime:   safeConnMaxLifetime,
		BusyTimeoutMilli:  safeBusyTimeoutMilli,
		WALAutocheckpoint: safeWALAutocheckpoint,

		WriterBusyTimeoutMilli: safeWriterBusyTimeoutMilli,
	}
}

// Validate is a hard invariant check, not advice. It runs before the handle is
// handed to the rest of the server so a bad preset fails startup rather than
// producing an unexplained memory profile in the field.
func (p Policy) Validate() error {
	if p.MaxOpenConns < 1 || p.MaxOpenConns > safeMaxOpenConns {
		return fmt.Errorf("%w: max open connections %d", ErrPolicy, p.MaxOpenConns)
	}
	if p.MaxIdleConns < 0 || p.MaxIdleConns > safeMaxIdleConns || p.MaxIdleConns > p.MaxOpenConns {
		return fmt.Errorf("%w: max idle connections %d", ErrPolicy, p.MaxIdleConns)
	}
	if p.CacheSizeKiB < 256 || p.CacheSizeKiB > safeCacheSizeKiB {
		return fmt.Errorf("%w: cache size %d KiB", ErrPolicy, p.CacheSizeKiB)
	}
	if p.MmapSizeBytes != 0 {
		return fmt.Errorf("%w: mmap must be disabled, got %d", ErrPolicy, p.MmapSizeBytes)
	}
	if p.ConnMaxLifetime <= 0 {
		return fmt.Errorf("%w: connection lifetime %s", ErrPolicy, p.ConnMaxLifetime)
	}
	if p.BusyTimeoutMilli < 1000 || p.BusyTimeoutMilli > 30000 {
		return fmt.Errorf("%w: busy timeout %d ms", ErrPolicy, p.BusyTimeoutMilli)
	}
	if p.WALAutocheckpoint < 1 || p.WALAutocheckpoint > 10000 {
		return fmt.Errorf("%w: wal autocheckpoint %d", ErrPolicy, p.WALAutocheckpoint)
	}
	if p.WriterBusyTimeoutMilli < 10 || p.WriterBusyTimeoutMilli > 1000 {
		return fmt.Errorf("%w: writer busy timeout %d ms", ErrPolicy, p.WriterBusyTimeoutMilli)
	}
	return nil
}

// Apply installs the pool half of the policy on an open handle. The pragma half
// travels in the DSN so every pooled connection inherits it, including one the
// driver reopens after an error.
func (p Policy) Apply(db *sql.DB) error {
	if err := p.Validate(); err != nil {
		return err
	}
	db.SetMaxOpenConns(p.MaxOpenConns)
	db.SetMaxIdleConns(p.MaxIdleConns)
	db.SetConnMaxLifetime(p.ConnMaxLifetime)
	return nil
}

// DSN builds the modernc driver DSN for path. Every per-connection pragma is
// expressed as a `_pragma=` parameter because the driver replays those on each
// new connection; a one-off PRAGMA statement would only reach the connection it
// happened to run on, which is exactly the bug a pool reintroduces.
func (p Policy) DSN(path string) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	values := url.Values{}
	// Every transaction takes SQLite's write lock at BEGIN rather than on its
	// first write. A deferred transaction that reads and then writes can be told
	// SQLITE_BUSY_SNAPSHOT — its read snapshot is behind the current WAL head and
	// there is no way to upgrade it — which reaches a caller as a failure it did
	// nothing to deserve. Taking the lock up front converts that into a wait the
	// gate has already made short, and the busy timeout covers the rest. Reads
	// that only read never open a transaction at all, so they pay nothing for it.
	values.Set("_txlock", "immediate")
	values.Add("_pragma", "foreign_keys(ON)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", p.BusyTimeoutMilli))
	values.Add("_pragma", "journal_mode(WAL)")
	values.Add("_pragma", "synchronous(NORMAL)")
	values.Add("_pragma", fmt.Sprintf("wal_autocheckpoint(%d)", p.WALAutocheckpoint))
	values.Add("_pragma", "temp_store(MEMORY)")
	values.Add("_pragma", fmt.Sprintf("cache_size(-%d)", p.CacheSizeKiB))
	values.Add("_pragma", fmt.Sprintf("mmap_size(%d)", p.MmapSizeBytes))
	return sqliteFileURI(path) + "?" + values.Encode(), nil
}

// readerDSN is DSN marked as a read pool, so the observed driver refuses a
// change made through it (see writer_discipline.go).
func (p Policy) readerDSN(path string) (string, error) {
	dsn, err := p.DSN(path)
	if err != nil {
		return "", err
	}
	return withRole(dsn, "reader"), nil
}

// sqliteFileURI treats path as a literal filename, including any %, ? or #.
// Escape only the path; the driver's separately encoded policy query follows it.
func sqliteFileURI(path string) string {
	return "file:" + (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath()
}

// Writer commits only append the WAL. An automatic checkpoint runs before
// Commit returns, so even a small scan transaction could copy the entire
// 64 MiB backlog while holding the gate needed by sign-in. The maintenance
// controller owns copying and pressure resets outside that commit path.
const writerWALAutocheckpoint = 0

// WriterDSN is DSN for the one writer connection: no automatic checkpoint, and
// its own short busy timeout.
func (p Policy) WriterDSN(path string) (string, error) {
	dsn, err := p.DSN(path)
	if err != nil {
		return "", err
	}
	filename, query, _ := strings.Cut(dsn, "?")
	query = strings.Replace(query, url.QueryEscape(fmt.Sprintf("wal_autocheckpoint(%d)", p.WALAutocheckpoint)), url.QueryEscape(fmt.Sprintf("wal_autocheckpoint(%d)", writerWALAutocheckpoint)), 1)
	query = strings.Replace(query, url.QueryEscape(fmt.Sprintf("busy_timeout(%d)", p.BusyTimeoutMilli)), url.QueryEscape(fmt.Sprintf("busy_timeout(%d)", p.WriterBusyTimeoutMilli)), 1)
	return withRole(filename+"?"+query, "writer"), nil
}

// RuntimePragmas is the same set as statements. It is executed once after open
// so the values are also observable through a diagnostics read and so the very
// first connection is configured even if a driver ever stops replaying the DSN.
func (p Policy) RuntimePragmas() []string {
	return []string{
		"PRAGMA foreign_keys=ON",
		fmt.Sprintf("PRAGMA busy_timeout=%d", p.BusyTimeoutMilli),
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		fmt.Sprintf("PRAGMA wal_autocheckpoint=%d", p.WALAutocheckpoint),
		"PRAGMA temp_store=MEMORY",
		fmt.Sprintf("PRAGMA cache_size=-%d", p.CacheSizeKiB),
		fmt.Sprintf("PRAGMA mmap_size=%d", p.MmapSizeBytes),
	}
}
