package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

// scanOpening owns ONE actual existing-file executor. It supplies no public
// Writer, source principal, run/lease capture, birth permit or fresh profile.
// The private issuer must compose those behind this same opening later.
type scanOpening struct {
	mu        sync.Mutex
	executor  *executor
	ownership *stateownership.Lease
	binding   *scanFileBinding
	lifetime  context.Context
	cancel    context.CancelFunc
	stopOwner func() bool
	closed    bool
	driver    *ScanDriver // sole issuer; protected by mu, retained after close
}

type scanFileBinding struct {
	path      string
	identity  os.FileInfo
	device    uint64
	inode     uint64
	ownership *stateownership.Lease
	lifetime  context.Context
	invalid   atomic.Bool
}

func (b *scanFileBinding) check() error {
	if b == nil || b.identity == nil || b.ownership == nil || b.lifetime == nil || b.invalid.Load() {
		return ErrClosed
	}
	err := b.lifetime.Err()
	if err == nil {
		err = b.ownership.Check()
	}
	if err == nil {
		var current os.FileInfo
		current, err = os.Lstat(b.path)
		if err == nil && (!current.Mode().IsRegular() || !os.SameFile(b.identity, current)) {
			err = ErrConflict
		}
		if err == nil {
			_, _, err = stateownership.SingleLinkedFileIdentity(current)
		}
	}
	if err != nil {
		b.invalid.Store(true) // putting an old pathname back never revives it
		return errors.Join(ErrClosed, err)
	}
	return nil
}

func (b *scanFileBinding) attest(conn driver.Conn) error {
	if err := b.check(); err != nil {
		return err
	}
	getter, ok := conn.(interface {
		SQLiteMainFileIdentity() (uint64, uint64, error)
	})
	if !ok {
		b.invalid.Store(true)
		return errors.Join(ErrClosed, ErrInvalid)
	}
	device, inode, err := getter.SQLiteMainFileIdentity()
	if err == nil && (device != b.device || inode != b.inode) {
		err = ErrConflict
	}
	if err != nil {
		b.invalid.Store(true)
		return errors.Join(ErrClosed, err)
	}
	return b.check()
}

// openScanStore is trusted startup, never a worker/public constructor. Cache
// budget belongs to the private opening profile. It opens only an EXISTING
// canonical file: no InitializeEmpty, schema fallback, repair or seed replay.
// Every physical replacement checks the same immutable file/ownership binding.
func openScanStore(ctx context.Context, owner *stateownership.Lock, path string, cacheKiB int32) (out *scanOpening, err error) {
	if ctx == nil || owner == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) || cacheKiB <= 0 {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lease, err := owner.ClaimStore()
	if err != nil {
		return nil, err
	}
	o := &scanOpening{ownership: lease}
	o.lifetime, o.cancel = context.WithCancel(ctx)
	o.stopOwner = context.AfterFunc(lease.Lifetime(), o.cancel)
	defer func() {
		if out == nil {
			err = errors.Join(err, o.close())
		}
	}()
	lockPath, err := lease.LockPath()
	if err != nil {
		return nil, err
	}
	if lockPath != filepath.Join(filepath.Dir(path), "server.lock") || path == lockPath {
		return nil, ErrInvalid
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	device, inode, err := stateownership.SingleLinkedFileIdentity(info)
	if err != nil {
		return nil, err
	}
	o.binding = &scanFileBinding{path: strings.Clone(path), identity: info, device: device, inode: inode, ownership: lease, lifetime: o.lifetime}
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: cacheKiB, Synchronous: sqliteconn.Full}
	connector, err := sqliteconn.Connector(path, cfg, sqliteconn.ExistingFile)
	if err != nil {
		return nil, err
	}
	w := &executor{config: cfg, now: time.Now, fileBinding: o.binding}
	o.executor = w
	w.openPool(connector, nil)
	conn, err := w.db.Conn(o.lifetime)
	if err != nil {
		return nil, err
	}
	_, err = sqliteconn.Assert(o.lifetime, conn, cfg)
	if err == nil {
		err = schema.AssertExisting(o.lifetime, conn)
	}
	err = errors.Join(err, conn.Close())
	if err != nil {
		return nil, err
	}
	if err := errors.Join(o.binding.check(), ctx.Err(), o.lifetime.Err()); err != nil {
		return nil, err
	}
	return o, nil
}

// close revokes ingress first. Busy/faulted physical owners keep the native
// lock; a timeout/error cannot drop it. A later owner retry may finish joining.
func (o *scanOpening) close() error {
	if o == nil {
		return nil
	}
	if o.cancel == nil || o.ownership == nil || o.stopOwner == nil {
		return ErrInvalid
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.cancel()
	if o.executor != nil {
		if err := o.executor.close(); err != nil {
			return err
		}
		stats := o.executor.db.Stats()
		o.executor.physicalFaults.mu.Lock()
		failed := o.executor.physicalFaults.failed
		o.executor.physicalFaults.mu.Unlock()
		if failed || stats.InUse != 0 || stats.OpenConnections != 0 {
			return ErrResolveRequired
		}
		if keeper := o.executor.keeper.Load(); keeper != nil && !keeper.KeeperClosed() {
			return ErrBusy // retain process/file ownership through CP's final close
		}
	}
	if err := o.ownership.Close(); err != nil {
		return err
	}
	o.stopOwner()
	o.closed = true
	return nil
}
