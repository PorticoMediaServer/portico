package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type object struct {
	service                    *Service
	binding                    binding
	relative, generation, lane string
	snapshot                   storage.Snapshot
	conditional                *remotemedia.VersionedClient
	native                     *mounts.NativeBackend
	life                       context.Context
	cancel                     context.CancelFunc
	release                    func()
	once                       sync.Once
	mu                         sync.Mutex
	stream                     *io.PipeReader
	finished                   chan error
	cache                      *os.File
	cached, ceiling, floor     int64
	terminal                   error
	rangeCache                 *rangeCache
	sourceOps                  *sourceOpsGate
}

func (s *Service) Acquire(ctx context.Context, path, lane string) (storage.RemoteObject, error) {
	if lane != "playback" && lane != "analysis" {
		return nil, storage.ErrRemoteConfig
	}
	b, relative, ok := s.find(path)
	if !ok || b.Removed || relative == "" {
		return nil, storage.ErrRemoteOffline
	}
	return s.acquireBinding(ctx, path, lane, b, relative, nil)
}

// acquireBinding retains an already selected source rather than resolving the
// path again. beforeProvider, when present, narrows direct DAV setup after its
// configuration is opened and before any stat/version request reaches origin.
func (s *Service) acquireBinding(ctx context.Context, path, lane string, b binding, relative string, beforeProvider func(context.Context, string) error) (storage.RemoteObject, error) {
	if beforeProvider != nil && b.Kind != "webdav" {
		return nil, storage.ErrRemoteRange
	}
	if err := s.requireWebDAVRandom(ctx, b.ID); err != nil {
		return nil, err
	}
	pool := s.playback
	if lane == "analysis" {
		pool = s.inventory
	}
	select {
	case pool <- struct{}{}:
	default:
		return nil, storage.ErrBusy
	}
	life, cancel := context.WithCancel(context.Background())
	token := identity.Token()
	o := &object{service: s, binding: b, relative: relative, lane: lane, life: life, cancel: cancel, ceiling: 4 << 30, floor: 2 << 30}
	if lane == "analysis" {
		o.ceiling = 16 << 20
	}
	s.mu.Lock()
	s.serveMu.Lock()
	if s.sourceRemovals[b.ID] {
		s.serveMu.Unlock()
		s.mu.Unlock()
		cancel()
		<-pool
		return nil, storage.ErrBusy
	}
	if s.active[b.ID] == nil {
		s.active[b.ID] = map[string]activeRead{}
	}
	s.active[b.ID][token] = activeRead{cancel, lane}
	s.serveMu.Unlock()
	s.mu.Unlock()
	o.release = func() {
		s.mu.Lock()
		delete(s.active[b.ID], token)
		if len(s.active[b.ID]) == 0 {
			delete(s.active, b.ID)
		}
		s.mu.Unlock()
		<-pool
	}
	good := false
	defer func() {
		if !good {
			o.Close()
		}
	}()
	var err error
	if b.Kind == "webdav" {
		var d *remotemedia.DAV
		d, o.generation, err = s.dav(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		if beforeProvider != nil {
			if err = beforeProvider(ctx, o.generation); err != nil {
				return nil, err
			}
		}
		gate, gateErr := s.sourceOperations(ctx, b.ID, o.generation)
		if gateErr != nil {
			return nil, gateErr
		}
		o.sourceOps = gate
		// One permit covers the setup pair: the stat and the version discovery
		// both reach the provider, and a direct read must count against the
		// source's saved connection ceiling exactly as a served read does.
		entry, e := func() (remotemedia.DAVEntry, error) {
			gated, grelease, gerr := gate.budget.Enter(ctx)
			if gerr != nil {
				return remotemedia.DAVEntry{}, sourceOpsError(ctx, gerr)
			}
			defer grelease()
			// The stat and the one-byte version discovery are two provider
			// requests, so each takes its own saved request slot.
			if waitErr := gate.pacer.waitRequest(gated); waitErr != nil {
				return remotemedia.DAVEntry{}, sourceOpsError(ctx, waitErr)
			}
			found, statErr := d.Stat(gated, relative)
			if statErr != nil {
				return remotemedia.DAVEntry{}, mapError(statErr)
			}
			if found.Directory || found.Size <= 0 {
				return remotemedia.DAVEntry{}, storage.ErrRemoteRange
			}
			o.snapshot = davSnapshot(found, b.Root)
			if waitErr := gate.pacer.waitRequest(gated); waitErr != nil {
				return remotemedia.DAVEntry{}, sourceOpsError(ctx, waitErr)
			}
			o.conditional, statErr = d.OpenVersion(gated, relative, remoteVersionScope(b, o.generation), o.snapshot.ObjectID)
			if errors.Is(statErr, mediasource.ErrIdentityRequired) || errors.Is(statErr, remotemedia.ErrRepresentationRange) {
				return remotemedia.DAVEntry{}, storage.ErrRemoteRange
			}
			if statErr != nil {
				return remotemedia.DAVEntry{}, mapError(statErr)
			}
			return found, nil
		}()
		if e != nil {
			return nil, e
		}
		ev := o.conditional.Version().Evidence()
		if ev.Size != entry.Size || ev.Revision != entry.ETag {
			return nil, storage.ErrRemoteChanged
		}
	} else {
		o.native, err = s.mounts.Native(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		o.generation = o.native.Generation()
		entry, e := o.native.StatForLane(ctx, relative, lane)
		if e != nil {
			return nil, e
		}
		if entry.IsDir || entry.Size <= 0 {
			return nil, storage.ErrRemoteRange
		}
		o.snapshot = entry.Snapshot(b.Root, relative)
		_, _, cache, floor, e := s.mounts.BackendInfo(ctx, b.ID)
		if e == nil && lane == "playback" && cache > 0 {
			o.ceiling = cache
		}
		if e == nil {
			o.floor = floor
		}
		o.cache, err = os.CreateTemp(s.private, "acquisition-*")
		if err != nil {
			return nil, storage.ErrRemoteLimit
		}
		if err = o.cache.Chmod(0600); err != nil {
			return nil, err
		}
		// A single retained cat stream feeds an owned immutable-prefix cache. Seeking
		// never reopens a mutable provider object based solely on matching size/mtime.
		// This deliberately reports sequential access, not a fabricated strong ETag.
		var writer *io.PipeWriter
		o.stream, writer = io.Pipe()
		o.finished = make(chan error, 1)
		supervise.Go("remotesources.acquire.read", func() {
			err := o.native.Read(life, relative, lane, 0, 0, func(r io.Reader) error {
				n, e := io.Copy(writer, io.LimitReader(r, o.snapshot.Size+1))
				if n > o.snapshot.Size {
					return storage.ErrRemoteChanged
				}
				if e == nil && n != o.snapshot.Size {
					return io.ErrUnexpectedEOF
				}
				return e
			})
			_ = writer.CloseWithError(err)
			o.finished <- err
		})
	}
	if err = s.check(ctx, b, o.generation); err != nil {
		return nil, err
	}
	if b.Kind == "webdav" {
		if cache, cacheErr := s.openRangeCache(ctx, b, o.generation, o.snapshot); cacheErr == nil && cache != nil {
			o.rangeCache = cache
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	o.snapshot = inventorySnapshot(b, o.generation, o.snapshot)
	if lane == "playback" {
		var expected string
		e := s.db.QueryRowContext(ctx, `SELECT revision FROM remote_object_refs WHERE path=?`, path).Scan(&expected)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if expected != "" && expected != o.snapshot.Revision {
			return nil, storage.ErrRemoteChanged
		}
	}
	s.noteSourceContact(b.ID, b.MountID, o.generation, time.Now(), nil)
	good = true
	return o, nil
}
func (o *object) Snapshot() storage.Snapshot { return o.snapshot }
func (o *object) Version() mediasource.Version {
	if o.conditional != nil {
		return o.conditional.Version()
	}
	return mediasource.Version{}
}
func (o *object) Access() string {
	if o.conditional != nil {
		return "finite_random"
	}
	return "finite_sequential"
}
func (o *object) bound(parent context.Context) (context.Context, func(), error) {
	if o.life.Err() != nil {
		return nil, nil, storage.ErrRemoteChanged
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	stop := context.AfterFunc(o.life, cancel)
	return ctx, func() { stop(); cancel() }, nil
}
func (o *object) Observe(parent context.Context) (storage.Snapshot, error) {
	ctx, release, err := o.bound(parent)
	if err != nil {
		return storage.Snapshot{}, err
	}
	defer release()
	if err = o.service.check(ctx, o.binding, o.generation); err != nil {
		return storage.Snapshot{}, err
	}
	if o.conditional != nil {
		gated, grelease, gateErr := o.sourceOps.budget.Enter(ctx)
		if gateErr != nil {
			return storage.Snapshot{}, sourceOpsError(ctx, gateErr)
		}
		if waitErr := o.sourceOps.pacer.waitRequest(gated); waitErr != nil {
			grelease()
			return storage.Snapshot{}, sourceOpsError(ctx, waitErr)
		}
		response, e := o.conditional.Open(gated, "GET", "bytes=0-0")
		if e != nil {
			grelease()
			return storage.Snapshot{}, mapError(e)
		}
		_, err = io.Copy(io.Discard, response.Body)
		closeErr := response.Body.Close()
		grelease()
		if err == nil {
			err = closeErr
		}
	} else {
		var entry mounts.NativeEntry
		entry, err = o.native.StatForLane(ctx, o.relative, o.lane)
		if err == nil {
			next := entry.Snapshot(o.binding.Root, o.relative)
			if o.generation+":"+next.Revision != o.snapshot.Revision || next.Size != o.snapshot.Size {
				err = storage.ErrRemoteChanged
			}
		}
	}
	if err != nil {
		o.cancel()
		return storage.Snapshot{}, mapError(err)
	}
	return o.snapshot, nil
}
func (o *object) OpenRange(parent context.Context, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length <= 0 || offset > o.snapshot.Size || length > o.snapshot.Size-offset {
		return nil, storage.ErrRemoteRange
	}
	ctx, release, err := o.bound(parent)
	if err != nil {
		return nil, err
	}
	if err = o.service.check(ctx, o.binding, o.generation); err != nil {
		release()
		return nil, err
	}
	if err = o.service.requireWebDAVRandom(ctx, o.binding.ID); err != nil {
		release()
		return nil, err
	}
	if o.conditional != nil {
		if cached, cacheErr := o.fromRangeCache(ctx, offset, length, release); cacheErr != nil {
			release()
			return nil, cacheErr
		} else if cached != nil {
			return cached, nil
		}
		// A provider range read holds one permit until its body closes; a cache
		// hit above made no provider request and was not charged.
		gated, grelease, gateErr := o.sourceOps.budget.Enter(ctx)
		if gateErr != nil {
			release()
			return nil, sourceOpsError(ctx, gateErr)
		}
		if waitErr := o.sourceOps.pacer.waitRequest(gated); waitErr != nil {
			grelease()
			release()
			return nil, sourceOpsError(ctx, waitErr)
		}
		response, e := o.conditional.Open(gated, http.MethodGet, fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
		if e != nil {
			grelease()
			release()
			return nil, mapError(e)
		}
		return &readCloser{Reader: &pacedReader{Reader: response.Body, pacer: o.sourceOps.pacer, ctx: gated}, close: func() error { err := response.Body.Close(); grelease(); release(); return err }}, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.terminal != nil {
		release()
		return nil, o.terminal
	}
	end := offset + length
	if end > o.ceiling {
		release()
		return nil, storage.ErrRemoteLimit
	}
	if !mounts.CacheSpaceAvailable(o.service.private, o.floor) {
		release()
		return nil, storage.ErrRemoteLimit
	}
	// Losing a sequential read is terminal for this acquisition. It may never
	// reopen the source or continue under the old acquisition ID after timeout.
	stop := context.AfterFunc(ctx, func() { o.cancel(); _ = o.stream.CloseWithError(ctx.Err()) })
	defer stop()
	if end > o.cached {
		n, e := io.CopyN(o.cache, o.stream, end-o.cached)
		o.cached += n
		if e != nil {
			o.terminal = mapError(e)
			o.cancel()
			release()
			return nil, o.terminal
		}
	}
	if o.cached == o.snapshot.Size && o.finished != nil {
		// Require the physical stream's exact EOF, including the overrun check,
		// before returning the last bytes. Never silently ignore a late error.
		var extra [1]byte
		n, e := o.stream.Read(extra[:])
		if n != 0 || e != io.EOF {
			o.terminal = storage.ErrRemoteChanged
		}
		if e = <-o.finished; e != nil {
			o.terminal = mapError(e)
		}
		o.finished = nil
		if o.terminal != nil {
			o.cancel()
			release()
			return nil, o.terminal
		}
	}

	if ctx.Err() != nil {
		release()
		return nil, ctx.Err()
	}
	reader := io.NewSectionReader(o.cache, offset, length)
	return &readCloser{Reader: reader, close: func() error { release(); return nil }}, nil
}

// fromRangeCache serves a read from this source's own cache. A nil reader and
// nil error is a legitimate miss; a provider, floor, identity or cancellation
// failure is returned as itself and never retried as a miss.
func (o *object) fromRangeCache(ctx context.Context, offset, length int64, release func()) (io.ReadCloser, error) {
	cache := o.rangeCache
	if cache == nil || offset < 0 {
		return nil, nil
	}
	var configured, floor int64
	if err := cache.service.db.QueryRowContext(ctx, `SELECT cache_bytes,cache_floor_bytes FROM remote_source_settings WHERE source_id=?`, cache.source).Scan(&configured, &floor); err != nil || configured <= 0 {
		// The source's cache was switched off after this acquisition opened.
		return nil, nil
	}
	end := offset + length
	if end > configured {
		// A read larger than the cache's whole size is a legitimate miss.
		return nil, nil
	}
	cache.guard.mu.Lock()
	if err := ctx.Err(); err != nil {
		cache.guard.mu.Unlock()
		return nil, err
	}
	if cache.service.cacheGuardRetired(cache.guard) || !cache.currentFileLocked() {
		cache.guard.mu.Unlock()
		return nil, storage.ErrRemoteChanged
	}
	if offset > cache.guard.cached {
		cache.guard.mu.Unlock()
		return nil, nil
	}
	if end > cache.guard.cached {
		gated, grelease, gateErr := o.sourceOps.budget.Enter(ctx)
		if gateErr != nil {
			cache.guard.mu.Unlock()
			return nil, sourceOpsError(ctx, gateErr)
		}
		if waitErr := o.sourceOps.pacer.waitRequest(gated); waitErr != nil {
			grelease()
			cache.guard.mu.Unlock()
			return nil, sourceOpsError(ctx, waitErr)
		}
		response, err := o.conditional.Open(gated, http.MethodGet, fmt.Sprintf("bytes=%d-%d", cache.guard.cached, end-1))
		if err != nil {
			grelease()
			cache.guard.mu.Unlock()
			return nil, mapError(err)
		}
		appendErr := cache.append(ctx, &pacedReader{Reader: response.Body, pacer: o.sourceOps.pacer, ctx: gated}, end)
		closeErr := response.Body.Close()
		grelease()
		if appendErr != nil {
			cache.guard.mu.Unlock()
			return nil, appendErr
		}
		if closeErr != nil {
			cache.guard.mu.Unlock()
			return nil, mapError(closeErr)
		}
		if end > cache.guard.cached {
			cache.guard.mu.Unlock()
			return nil, storage.ErrRemoteChanged
		}
	}
	reader := cache.holdReaderLocked(ctx, offset, length, release)
	cache.guard.mu.Unlock()
	return reader, nil
}

func (o *object) Close() error {
	o.once.Do(func() {
		o.cancel()
		if o.conditional != nil {
			o.conditional.Close()
		}
		if o.stream != nil {
			_ = o.stream.Close()
		}
		o.mu.Lock()
		if o.cache != nil {
			name := o.cache.Name()
			_ = o.cache.Close()
			_ = os.Remove(name)
		}
		if o.rangeCache != nil {
			commit, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = o.rangeCache.commit(commit)
			cancel()
			o.rangeCache.unpin()
			o.rangeCache = nil
		}
		o.mu.Unlock()
		if o.release != nil {
			o.release()
		}
	})
	return nil
}

type readCloser struct {
	io.Reader
	close func() error
	once  sync.Once
	err   error
}

func (r *readCloser) Close() error { r.once.Do(func() { r.err = r.close() }); return r.err }

// pacedReader delays delivered bytes at the source's saved byte rate. Each read
// is capped to one bounded chunk, so a large caller buffer cannot bypass a slow
// rate on its first read; a failed or cancelled pacing wait clears the bytes it
// would have delivered so the consumer cannot use unpaced data. The permit
// context owns it, so retirement or cancellation ends the delay exactly as it
// ends the body.
type pacedReader struct {
	io.Reader
	pacer *sourceRatePacer
	ctx   context.Context
}

func (r *pacedReader) Read(p []byte) (int, error) {
	if len(p) > directPacedChunk {
		p = p[:directPacedChunk]
	}
	n, err := r.Reader.Read(p)
	if n > 0 {
		if waitErr := r.pacer.waitBytes(r.ctx, int64(n)); waitErr != nil {
			clear(p[:n])
			return 0, waitErr
		}
	}
	return n, err
}

// sourceOpsError keeps a retired source gate distinct from the caller's own
// cancellation: the wait ends with the generation, not with a policy refusal,
// so a direct read reports the same changed-source error as a served one.
func sourceOpsError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return storage.ErrRemoteChanged
	}
	return err
}
