package writer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

func scanOpeningFixture(t *testing.T) (string, *stateownership.Lock) {
	t.Helper()
	w, path := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), "INSERT INTO search_revision(singleton,revision) VALUES(1,733)"); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	return path, scanOpeningOwner(t, path)
}

func scanOpeningOwner(t *testing.T, path string) *stateownership.Lock {
	t.Helper()
	owner, err := stateownership.Acquire(filepath.Join(filepath.Dir(path), "server.lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	return owner
}

func openScanFixture(t *testing.T, path string, owner *stateownership.Lock) *scanOpening {
	t.Helper()
	o, err := openScanStore(t.Context(), owner, path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.close(); err != nil {
			t.Error(err)
		}
	})
	return o
}

func TestScanOpeningCanonicalExistingFileAndSingleStore(t *testing.T) {
	path, owner := scanOpeningFixture(t)
	o := openScanFixture(t, path, owner)
	if duplicate, err := openScanStore(t.Context(), owner, path, 1024); duplicate != nil || !errors.Is(err, stateownership.ErrBusy) {
		t.Fatal("second store opening", err)
	}
	conn, err := o.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := sqliteconn.Assert(t.Context(), conn, o.executor.config)
	if err != nil || actual.Synchronous != int(sqliteconn.Full) || actual.DQSDDL != 0 || actual.DQSDML != 0 {
		t.Fatal(actual, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := o.executor.db.QueryRowContext(t.Context(), "SELECT revision FROM search_revision WHERE singleton=1").Scan(&revision); err != nil || revision != 733 {
		t.Fatal("restart changed native contents", revision, err)
	}
	if err := o.close(); err != nil {
		t.Fatal(err)
	}
	if stats := o.executor.db.Stats(); stats.OpenConnections != 0 || stats.InUse != 0 {
		t.Fatal("physical pool did not close", stats)
	}
	// Only actual close releases the store claim. No old executor is revived.
	next := openScanFixture(t, path, owner)
	if next.executor == o.executor {
		t.Fatal("restart reused the old executor")
	}
}

func TestScanOpeningRefusesMissingWrongScopeAndLayout(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "foreign", "wrong_scope", "lock_file"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.sqlite")
			owner := scanOpeningOwner(t, path)
			if kind == "empty" || kind == "foreign" {
				cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}
				db, _, err := sqliteconn.Open(t.Context(), path, cfg)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "foreign" {
					if _, err := db.ExecContext(t.Context(), "CREATE TABLE foreign_marker(id INTEGER PRIMARY KEY)"); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "wrong_scope" {
				path = filepath.Join(t.TempDir(), "different.sqlite")
			}
			if kind == "lock_file" {
				path = filepath.Join(filepath.Dir(path), "server.lock")
			}
			value, err := openScanStore(t.Context(), owner, path, 1024)
			if value != nil || err == nil {
				t.Fatal("invalid opening returned a store", kind, err)
			}
			if (kind == "empty" || kind == "foreign") && !errors.Is(err, schema.ErrLayoutMismatch) {
				t.Fatal("noncanonical file was not refused by canonical assertion", err)
			}
			if kind == "missing" {
				for _, name := range []string{path, path + "-wal", path + "-shm"} {
					if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("restart created missing database", name, err)
					}
				}
			}
			// A proved startup refusal releases only its own store claim.
			retry, err := owner.ClaimStore()
			if err != nil {
				t.Fatal("startup refusal stranded store ownership", err)
			}
			if err := retry.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScanOpeningRefusesCanceledAndCopiedNativeOwnership(t *testing.T) {
	path, owner := scanOpeningFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if v, err := openScanStore(ctx, owner, path, 1024); v != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled opening", err)
	}
	copyOfOwner := *owner
	if v, err := openScanStore(t.Context(), &copyOfOwner, path, 1024); v != nil || !errors.Is(err, stateownership.ErrInvalid) {
		t.Fatal("copied native issuer", err)
	}
	if v, err := openScanStore(t.Context(), nil, path, 1024); v != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("missing native issuer", err)
	}
	if v, err := openScanStore(t.Context(), owner, path, 0); v != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown cache budget", err)
	}
	o := openScanFixture(t, path, owner)
	if err := o.binding.check(); err != nil {
		t.Fatal(err)
	}
}

func TestScanOpeningRejectsReplacementAndNeverResurrectsOldBinding(t *testing.T) {
	path, owner := scanOpeningFixture(t)
	o := openScanFixture(t, path, owner)
	o.executor.db.SetMaxIdleConns(0)
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := o.executor.db.PingContext(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatal("replacement used another physical database", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".old", path); err != nil {
		t.Fatal(err)
	}
	if err := o.executor.db.PingContext(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatal("old binding was resurrected", err)
	}
}

func TestScanOpeningBusyCloseRetainsPhysicalStoreClaim(t *testing.T) {
	path, owner := scanOpeningFixture(t)
	o := openScanFixture(t, path, owner)
	conn, err := o.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Real acquired native connection plus the actual executor's physical owner
	// mutex; no command/grant/Applied or numerical physical recipe is invented.
	o.executor.mu.Lock()
	err = o.close()
	o.executor.mu.Unlock()
	if !errors.Is(err, ErrBusy) || o.lifetime.Err() == nil {
		t.Fatal("busy close failed to revoke and retain", err)
	}
	if v, err := owner.ClaimStore(); v != nil || !errors.Is(err, stateownership.ErrBusy) {
		t.Fatal("busy physical owner lost exclusivity", err)
	}
	if err := owner.Close(); !errors.Is(err, stateownership.ErrBusy) {
		t.Fatal("root close released the native holding", err)
	}
	if native, err := stateownership.Acquire(filepath.Join(filepath.Dir(path), "server.lock")); native != nil || !errors.Is(err, stateownership.ErrHeld) {
		if native != nil {
			_ = native.Close()
		}
		t.Fatal("physical lock released before native close", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.close(); err != nil {
		t.Fatal(err)
	}
	native, err := stateownership.Acquire(filepath.Join(filepath.Dir(path), "server.lock"))
	if err != nil {
		t.Fatal("final physical close did not release native lock", err)
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScanOpeningIdleConnectionCannotBypassReplacedFileFence(t *testing.T) {
	path, owner := scanOpeningFixture(t)
	o := openScanFixture(t, path, owner)
	if o.executor.db.Stats().OpenConnections != 1 {
		t.Fatal("no existing physical idle connection")
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Skipf("platform cannot rename an open database: %v", err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Only a deterministic admission test; no latency or production grant is
	// inferred from this synthetic quantum/clock/custody.
	now := time.Now()
	o.executor.now = func() time.Time { return now }
	custody := &testCustody{}
	tx, err := o.executor.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if tx != nil {
		_, _ = tx.rollback(nil)
		t.Fatal("idle old connection admitted BEGIN on replaced file")
	}
	if !errors.Is(err, ErrClosed) || custody.checks != 1 || custody.returns != 1 || custody.fences != 1 {
		t.Fatal("replaced file did not refuse before native acquisition/BEGIN", err, custody)
	}
	if stats := o.executor.db.Stats(); stats.InUse != 0 {
		t.Fatal("failed admission retained a physical borrow", stats)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatal("replaced file was initialized or repaired", err)
	}
}
