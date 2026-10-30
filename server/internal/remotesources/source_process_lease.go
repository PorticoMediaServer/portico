package remotesources

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"time"

	"os"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// sourceProcessLease owns the one physical serve-process slot for a source.
// It is capacity/custody, not actor, target, operation or cost authorization.
// The command supervisor consumes it and releases only after physical Wait.
type sourceProcessLease struct {
	service            *Service
	source             string
	generation         string
	root               string
	profile            SourceReadProfile
	transport          SourceTransport
	life               context.Context
	cancel             context.CancelFunc
	released           atomic.Bool
	process            *rcloneprogram.ServeProcess
	egress             *mounts.SourceEgress
	cache              string
	sequential         bool
	captureMode        captureReadMode
	networkFingerprint string
	capabilities       backendCapabilities
	lastUsed           atomic.Int64
	// retiring is set under serveMu with the cancellation, so an admission
	// either happened before the cut (and touched lastUsed) or observes it.
	retiring bool
}

// serveIdleRetire stops an idle serve process after a bounded quiet period, so
// an unused remote costs nothing. It never cancels an active reader or an
// operation in flight; the next start re-creates the process from the saved
// generation/profile under the same shared budget.
const serveIdleRetire = 10 * time.Minute

func (l *sourceProcessLease) touch() {
	if l != nil {
		l.lastUsed.Store(time.Now().UnixNano())
	}
}

func (l *sourceProcessLease) idleFor(now time.Time) time.Duration {
	if l == nil {
		return 0
	}
	last := l.lastUsed.Load()
	if last == 0 {
		return serveIdleRetire
	}
	return now.Sub(time.Unix(0, last))
}

// retireIdleServes stops the current serve process of a source that has had no
// admitted work or reader for the quiet period. The decision and the
// cancellation share serveMu, and retiring keeps the slot registered until its
// supervisor releases it after the physical Wait, so an admission either
// registers its use under the same lock first or observes retiring and gets
// Busy. A registered reader, inventory listing, test, removal, admitted shared
// budget operation or live process all keep it.
func (s *Service) retireIdleServes(now time.Time) {
	busy := map[string]bool{}
	s.mu.Lock()
	for source, reads := range s.active {
		if len(reads) > 0 {
			busy[source] = true
		}
	}
	s.mu.Unlock()
	// The decision and the cancellation both happen under serveMu, and the
	// retiring flag keeps the slot registered until its supervisor releases it
	// after the physical Wait. An admission either wins the same lock first
	// (touching lastUsed) or observes retiring and returns Busy.
	s.serveMu.Lock()
	for source, slot := range s.serveLeases {
		if slot == nil || slot.retiring || slot.process == nil || slot.CheckLocal() != nil || busy[source] || s.sourceRemovals[source] || s.sourceTests[source] != nil {
			continue
		}
		if len(s.readLeases[source]) > 0 || slot.idleFor(now) < serveIdleRetire {
			continue
		}
		if slot.process.OperationCounts().Active > 0 {
			continue
		}
		slot.retiring = true
		slot.cancel()
	}
	s.serveMu.Unlock()
}

func (s *Service) reserveSourceProcess(ctx context.Context, source, generation string, profileRevision int64) (*sourceProcessLease, error) {
	return s.reserveSourceProcessMode(ctx, source, generation, profileRevision, "")
}

func (s *Service) reserveSourceProcessMode(ctx context.Context, source, generation string, profileRevision int64, captureMode captureReadMode) (*sourceProcessLease, error) {
	if s == nil || s.db == nil || source == "" || generation == "" || profileRevision < 0 {
		return nil, storage.ErrRemoteConfig
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer snapshot.Rollback()
	current, err := observationGenerationTx(ctx, snapshot.Tx(), source)
	if err != nil {
		return nil, err
	}
	if current != generation {
		return nil, storage.ErrRemoteChanged
	}
	profile, err := SourceReadProfileTx(ctx, snapshot.Tx(), source)
	if err != nil {
		return nil, err
	}
	if profile.Revision != profileRevision {
		return nil, storage.ErrRemoteChanged
	}
	transport, err := TransportTx(ctx, snapshot.Tx(), source)
	if err != nil {
		return nil, err
	}
	var root string
	if err = snapshot.Tx().QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, source).Scan(&root); err != nil || !filepath.IsAbs(root) {
		return nil, storage.ErrRemoteConfig
	}
	s.serveMu.Lock()
	defer s.serveMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.serveClosed {
		return nil, storage.ErrRemoteOffline
	}
	if s.serveLeases == nil {
		s.serveLeases = make(map[string]*sourceProcessLease)
	}
	if s.serveLeases[source] != nil || s.sourceTests[source] != nil || s.sourceRemovals[source] {
		return nil, storage.ErrBusy
	}
	life, cancel := context.WithCancel(ctx)
	lease := &sourceProcessLease{service: s, source: source, generation: generation, root: root, profile: profile, transport: transport, life: life, cancel: cancel, captureMode: captureMode}
	lease.touch()
	s.serveLeases[source] = lease
	return lease, nil
}

func (l *sourceProcessLease) Context() context.Context { return l.life }

// CheckLocal is safe inside a caller-owned transaction: it never opens another
// DB snapshot, performs remote I/O, or waits for capacity. The caller's actual
// authority reader must compare its captured generation/profile/root there.
func (l *sourceProcessLease) CheckLocal() error {
	if l == nil || l.released.Load() {
		return storage.ErrRemoteOffline
	}
	return l.life.Err()
}

func (l *sourceProcessLease) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := l.CheckLocal(); err != nil {
		return err
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, l.service.db)
	if err != nil {
		return err
	}
	defer snapshot.Rollback()
	current, err := observationGenerationTx(ctx, snapshot.Tx(), l.source)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		l.cancel()
		return err
	}
	profile, err := SourceReadProfileTx(ctx, snapshot.Tx(), l.source)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		l.cancel()
		return err
	}
	transport, err := TransportTx(ctx, snapshot.Tx(), l.source)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		l.cancel()
		return err
	}
	if transport != l.transport {
		l.cancel()
		return storage.ErrRemoteChanged
	}
	var root string
	err = snapshot.Tx().QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, l.source).Scan(&root)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || root != l.root || current != l.generation || profile != l.profile {
		l.cancel()
		return storage.ErrRemoteChanged
	}
	var native *nativeSourceAuthority
	var imported *importedSourceConfiguration
	if l.networkFingerprint != "" {
		network, e := l.service.sourceNetworkTx(ctx, snapshot.Tx(), l.source, l.root)
		sequential, se := sourceSequentialTx(ctx, snapshot.Tx(), l.source, l.root)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil || se != nil || network.Fingerprint != l.networkFingerprint || sequential != l.sequential {
			l.cancel()
			return storage.ErrRemoteChanged
		}
		native = network.Native
		imported = network.Imported
	}
	// DNS/provider checks happen outside the snapshot. A reconnect is admitted
	// only against this source's saved native approval. SFTP's policy tunnel
	// repeats this at each numeric dial; SMB reconnects retain the launch pin.
	snapshot.Rollback()
	if native != nil {
		_, err = (remotemedia.Policy{Resolver: l.service.resolver}).CheckNativeEndpoint(ctx, native.endpoint, native.config.Approval)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			l.cancel()
			return err
		}
	}
	if imported != nil {
		if err = l.service.checkImportedApprovals(ctx, *imported); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			l.cancel()
			return err
		}
	}
	if l.egress != nil && l.egress.CheckSMBPins != nil {
		if err = l.egress.CheckSMBPins(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			l.cancel()
			return err
		}
	}
	return l.CheckLocal()
}

func (l *sourceProcessLease) Release() {
	if !l.released.CompareAndSwap(false, true) {
		return
	}
	l.cancel()
	if l.egress != nil {
		l.egress.Close()
	}
	if l.cache != "" {
		_ = os.RemoveAll(l.cache)
	}
	l.service.serveMu.Lock()
	process := l.process
	if l.service.serveLeases[l.source] == l {
		delete(l.service.serveLeases, l.source)
	}
	var readers []*SourceReadLease
	for _, reader := range l.service.readLeases[l.source] {
		readers = append(readers, reader)
	}
	l.service.serveMu.Unlock()
	// A physical exit ends only readers bound to that exact process epoch.
	// A provisional reader which never opened bytes has no retired epoch.
	if process != nil {
		for _, reader := range readers {
			reader.mu.Lock()
			same := reader.processGeneration == process.Generation()
			reader.mu.Unlock()
			if same {
				reader.fail(storage.ErrRemoteOffline)
			}
		}
	}
}

func (s *Service) stopServeProcess(source string) {
	s.serveMu.Lock()
	defer s.serveMu.Unlock()
	if lease := s.serveLeases[source]; lease != nil {
		lease.cancel()
	}
}

func (s *Service) stopServeProcesses() {
	s.serveMu.Lock()
	s.serveClosed = true
	for _, scope := range s.sourceTests {
		scope.cancel()
	}
	for _, lease := range s.serveLeases {
		lease.cancel()
	}
	s.serveMu.Unlock()
	s.stopReadLeases("", storage.ErrRemoteOffline, nil)
}

// ReadProfileCommitted applies a committed profile change to process custody.
// It never changes root/source identity and never releases a physical slot.
// A missed notification is also caught by Check's actual DB/profile recheck.
func (s *Service) ReadProfileCommitted(ctx context.Context, source string, revision int64) error {
	if s == nil || s.db == nil || source == "" || revision < 0 {
		return storage.ErrRemoteConfig
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	defer snapshot.Rollback()
	profile, err := SourceReadProfileTx(ctx, snapshot.Tx(), source)
	snapshot.Rollback()
	if err != nil {
		return err
	}
	if profile.Revision != revision {
		return storage.ErrRemoteChanged
	}
	s.retireReadProfile(source, profile)
	return nil
}

func (s *Service) retireReadProfile(source string, profile SourceReadProfile) {
	// The profile revision is part of the shared budget's identity. Retire it
	// only when the committed revision actually moved, mirroring the serve
	// lease's own conditional retirement so a same-revision replay keeps a
	// matching live budget (and its StartServe after-function) intact.
	s.retireSourceOperationsIfStale(source, profile.Revision)
	s.serveMu.Lock()
	if lease := s.serveLeases[source]; lease != nil && lease.profile != profile {
		lease.cancel()
	}
	s.serveMu.Unlock()
	s.stopReadLeases(source, storage.ErrRemoteChanged, &profile)
}
