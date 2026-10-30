package remotesources

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// OpenDiscConditional consumes this original playback lease after its outside-
// SQL context check, on both success and failure. The caller must have admitted
// the real original Play operation/physical object in CaptureReadSourceTx's
// mandatory callback. It supplies neither scan authority nor a full-capture
// fallback. The current direct WebDAV adapter alone supplies a strong origin
// version; native/weak range access is deliberately not promoted to one.
func (l *SourceReadLease) OpenDiscConditional(ctx context.Context) (_ storage.RemoteObject, err error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || l == nil || l.service == nil {
		return nil, storage.ErrRemoteConfig
	}
	l.openMu.Lock()
	defer l.openMu.Unlock()
	var base storage.RemoteObject
	defer func() {
		if err != nil {
			if base != nil {
				base.Close()
			}
			l.Close()
		}
	}()
	if l.binding.Purpose != SourcePlayback || l.binding.Sequential || !strings.EqualFold(filepath.Ext(l.binding.Observation.Object), ".iso") {
		return nil, storage.ErrRemoteRange
	}
	view, err := dbwork.BeginSnapshot(ctx, l.service.db)
	if err != nil {
		return nil, err
	}
	err = l.CheckTx(ctx, view.Tx())
	var recorded RecordedScanObservation
	if err == nil {
		recorded, err = readScanObservationTx(ctx, view.Tx(), l.binding.Observation.SourceID, l.binding.Observation.Object)
	}
	view.Rollback()
	if err != nil {
		return nil, err
	}
	b, relative, ok := l.service.find(l.binding.Path)
	if !ok || b.Removed || b.ID != l.binding.Observation.SourceID || b.Root != l.binding.Root || relative != l.binding.Observation.Object || b.Kind != "webdav" {
		return nil, storage.ErrRemoteRange
	}
	// No provider work occurred above. The existing acquisition retains the
	// actual source network/pacing/connection/cache guards and legacy references.
	// No alternate lane or relaxed reference predicate is used for this handoff.
	call, release := l.bound(ctx)
	base, err = l.service.acquireBinding(call, l.binding.Path, "playback", b, relative, func(current context.Context, generation string) error {
		if generation != l.binding.Observation.Generation {
			return storage.ErrRemoteChanged
		}
		return l.Check(current)
	})
	release()
	if err != nil {
		return nil, err
	}
	actual, ok := base.(*object)
	if !ok || actual.binding.ID != b.ID || actual.binding.Root != b.Root || actual.relative != relative || actual.generation != l.binding.Observation.Generation {
		return nil, storage.ErrRemoteChanged
	}
	version, snapshot := base.Version(), base.Snapshot()
	evidence := version.Evidence()
	facts := recorded.Observation.Facts
	if version.ID() == "" || evidence.Kind != mediasource.StrongETag || base.Access() != "finite_random" ||
		evidence.Scope != remoteVersionScope(b, l.binding.Observation.Generation) || evidence.Object != snapshot.ObjectID ||
		evidence.Revision != facts.ProviderETag || evidence.Size != l.binding.Size || snapshot.Size != l.binding.Size ||
		snapshot.Directory || snapshot.ErrorCode != "" || facts.ModifiedKnown && snapshot.ModifiedNS != facts.ModifiedNS {
		return nil, storage.ErrRemoteChanged
	}
	if err = l.Check(ctx); err != nil {
		return nil, err
	}
	out := &discConditionalObject{lease: l, object: base, version: version, snapshot: snapshot}
	// A profile/source/actor retirement must close the actual retained origin,
	// even when the helper is idle/back-pressured and issues no further reads.
	context.AfterFunc(l.life, func() { out.Close() })
	if err = out.check(ctx); err != nil {
		out.Close()
		return nil, err
	}
	return out, nil
}

type discConditionalObject struct {
	lease    *SourceReadLease
	object   storage.RemoteObject
	version  mediasource.Version
	snapshot storage.Snapshot
	closed   atomic.Bool
	once     sync.Once
	closeErr error
}

func (o *discConditionalObject) Snapshot() storage.Snapshot   { return o.snapshot }
func (o *discConditionalObject) Version() mediasource.Version { return o.version }
func (o *discConditionalObject) Access() string               { return "finite_random" }

func (o *discConditionalObject) check(ctx context.Context) error {
	if o.closed.Load() {
		return storage.ErrRemoteOffline
	}
	if err := o.lease.Check(ctx); err != nil {
		return err
	}
	if o.object.Version().ID() != o.version.ID() || o.object.Snapshot() != o.snapshot {
		return o.lease.fail(storage.ErrRemoteChanged)
	}
	return ctx.Err()
}

// CheckTx is a DB-only check of THIS retained object/lease, including its
// terminal state. Stored conditional version equality alone cannot revive a
// source the original lease already retired. No provider observation is done.
func (o *discConditionalObject) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if o == nil || ctx == nil || tx == nil || o.closed.Load() {
		return storage.ErrRemoteOffline
	}
	if err := o.lease.CheckTx(ctx, tx); err != nil {
		return err
	}
	if o.object.Version().ID() != o.version.ID() || o.object.Snapshot() != o.snapshot {
		return o.lease.fail(storage.ErrRemoteChanged)
	}
	return ctx.Err()
}

func (o *discConditionalObject) Observe(ctx context.Context) (storage.Snapshot, error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return storage.Snapshot{}, storage.ErrRemoteConfig
	}
	if err := o.check(ctx); err != nil {
		return storage.Snapshot{}, err
	}
	current, err := o.object.Observe(ctx)
	if err != nil {
		return storage.Snapshot{}, err
	}
	if current != o.snapshot {
		return storage.Snapshot{}, o.lease.fail(storage.ErrRemoteChanged)
	}
	if err = o.check(ctx); err != nil {
		return storage.Snapshot{}, err
	}
	return o.snapshot, nil
}

func (o *discConditionalObject) OpenRange(parent context.Context, offset, length int64) (io.ReadCloser, error) {
	if parent == nil || dbwork.Snapshot(parent) != nil {
		return nil, storage.ErrRemoteConfig
	}
	if err := o.check(parent); err != nil {
		return nil, err
	}
	ctx, release := o.lease.bound(parent)
	body, err := o.object.OpenRange(ctx, offset, length)
	if err != nil {
		release()
		return nil, err
	}
	if err = o.check(ctx); err != nil {
		body.Close()
		release()
		return nil, err
	}
	return &discConditionalRange{ctx: ctx, source: o, body: body, release: release}, nil
}

func (o *discConditionalObject) Close() error {
	o.once.Do(func() {
		o.closed.Store(true)
		o.closeErr = o.object.Close()
		if err := o.lease.Close(); o.closeErr == nil {
			o.closeErr = err
		}
	})
	return o.closeErr
}

type discConditionalRange struct {
	ctx     context.Context
	source  *discConditionalObject
	body    io.ReadCloser
	release func()
	mu      sync.Mutex
	once    sync.Once
	err     error
	buffer  [64 << 10]byte
}

func (r *discConditionalRange) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if err := r.source.check(r.ctx); err != nil {
		r.Close()
		return 0, err
	}
	buffer := r.buffer[:min(len(p), len(r.buffer))]
	n, err := r.body.Read(buffer)
	// Never copy a successfully fetched extent after the original actor/source
	// was retired while Read was blocked. The helper sees no retired bytes.
	if current := r.source.check(r.ctx); current != nil {
		r.Close()
		return 0, current
	}
	if err != nil && err != io.EOF {
		r.Close()
		return 0, err
	}
	copy(p, buffer[:n])
	return n, err
}

func (r *discConditionalRange) Close() error {
	r.once.Do(func() { r.release(); r.err = r.body.Close() })
	return r.err
}
