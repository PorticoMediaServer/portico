// Package schematest supplies independent canonical database files to tests.
// Import it only from test files. It owns no runtime pool or writer authority.
package schematest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

const copyBufferBytes = 64 << 10

type template struct {
	once       sync.Once
	err        error
	dir, path  string
	engine     string
	size       int64
	digest     [sha256.Size]byte
	bootstraps atomic.Int64
	clones     atomic.Int64
}

var process struct {
	sync.RWMutex
	ran    bool
	active *template
}

// Run owns the template lifetime, including cleanup before its caller exits.
// Use os.Exit(schematest.Run(m)) in TestMain. No file or pool is created unless
// a test requests Clone; the first test does not own the template's directory.
func Run(m *testing.M) (code int) {
	process.Lock()
	if m == nil || process.ran {
		process.Unlock()
		fmt.Fprintln(os.Stderr, "schematest: Run requires one nonnil test process")
		return 2
	}
	t := &template{}
	process.ran, process.active = true, t
	process.Unlock()
	defer func() {
		process.Lock()
		process.active = nil
		process.Unlock()
		if t.dir != "" {
			fmt.Fprintf(os.Stderr, "schematest: schema=%s engine=%s bytes=%d sha256=%x bootstraps=%d clones=%d\n", schema.SHA256, t.engine, t.size, t.digest, t.bootstraps.Load(), t.clones.Load())
			if err := os.RemoveAll(t.dir); err != nil {
				fmt.Fprintln(os.Stderr, "schematest cleanup:", err)
				if code == 0 {
					code = 1
				}
			}
		}
	}()
	return m.Run()
}

// Clone returns a complete, independent file in t.TempDir, not the template.
// Open it through sqliteconn.Open/Connector and Assert at normal acquisition
// boundaries. No connection, raw driver or writable shared database escapes.
func Clone(t testing.TB) string {
	t.Helper()
	process.RLock()
	base := process.active
	process.RUnlock()
	if base == nil {
		t.Fatal("schematest.Clone requires schematest.Run in TestMain")
	}
	base.once.Do(func() { base.err = base.build() })
	if base.err != nil {
		t.Fatalf("canonical schema template: %v", base.err)
	}
	if err := noSidecars(base.path); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	if err := copyFile(base.path, path, base.size, base.digest); err != nil {
		t.Fatalf("copy canonical schema template: %v", err)
	}
	base.clones.Add(1)
	return path
}

func (t *template) build() error {
	// Honour the job's scratch root on Windows as well as Unix; Go's native
	// Windows TempDir otherwise consults TMP/TEMP rather than TMPDIR.
	dir, err := os.MkdirTemp(os.Getenv("TMPDIR"), "portico-schema-template-")
	if err != nil {
		return err
	}
	t.dir, err = filepath.Abs(dir)
	if err != nil {
		return errors.Join(err, os.RemoveAll(dir))
	}
	t.path = filepath.Join(t.dir, "canonical.sqlite")
	settings, err := t.bootstrapFile(t.path)
	if err != nil {
		return err
	}
	// bootstrapFile has closed EVERY handle, including the physical pool. A
	// live/partial WAL is never silently omitted from an allegedly fresh copy.
	if err := noSidecars(t.path); err != nil {
		return err
	}
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.CopyBuffer(h, struct{ io.Reader }{f}, make([]byte, copyBufferBytes))
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return err
	}
	if n <= 0 {
		return errors.New("empty canonical template")
	}
	t.size, t.engine = n, settings.Engine
	copy(t.digest[:], h.Sum(nil))
	return nil
}

func (t *template) bootstrapFile(path string) (settings sqliteconn.Settings, err error) {
	ctx := context.Background()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}
	db, settings, err := sqliteconn.Open(ctx, path, cfg)
	if err != nil {
		return settings, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return settings, err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	if settings, err = sqliteconn.Assert(ctx, conn, cfg); err != nil {
		return settings, err
	}
	t.bootstraps.Add(1)
	if err = schema.InitializeEmpty(ctx, conn); err != nil {
		return settings, err
	}
	if err = schema.AssertExisting(ctx, conn); err != nil {
		return settings, err
	}
	var integrity string
	if err = conn.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return settings, errors.Join(errors.New("canonical template integrity check"), err)
	}
	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return settings, err
	}
	if rows.Next() {
		err = errors.New("canonical template foreign key violation")
	}
	return settings, errors.Join(err, rows.Err(), rows.Close())
}

func noSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		_, err := os.Lstat(path + suffix)
		if err == nil {
			return fmt.Errorf("canonical template has %s sidecar", suffix)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func copyVerified(dst io.Writer, src io.Reader, size int64, digest [sha256.Size]byte) error {
	if size <= 0 || size == math.MaxInt64 {
		return errors.New("invalid template size")
	}
	h := sha256.New()
	// Hide optional fast-copy interfaces: all copying uses this bounded buffer.
	n, err := io.CopyBuffer(io.MultiWriter(dst, h), io.LimitReader(src, size+1), make([]byte, copyBufferBytes))
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("template copy size: got %d want %d", n, size)
	}
	if got := h.Sum(nil); string(got) != string(digest[:]) {
		return errors.New("template copy digest mismatch")
	}
	return nil
}

func copyFile(source, destination string, size int64, digest [sha256.Size]byte) (err error) {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	created := false
	defer func() {
		err = errors.Join(err, src.Close())
		if err != nil && created {
			err = errors.Join(err, os.Remove(destination))
		}
	}()
	stat, err := src.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() || stat.Size() != size {
		return errors.New("template source is not the expected regular file")
	}
	dst, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	created = true
	err = errors.Join(copyVerified(dst, src, size, digest), dst.Close())
	return err
}
