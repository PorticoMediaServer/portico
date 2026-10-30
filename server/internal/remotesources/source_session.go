package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type SourceReadPurpose string

const (
	SourceReading          SourceReadPurpose = "reading"
	SourcePlayback         SourceReadPurpose = "playback"
	SourceMediaAnalysis    SourceReadPurpose = "media_analysis"
	SourceInventory        SourceReadPurpose = "inventory"
	SourceEpisodeAutomatic SourceReadPurpose = "automatic_episode_thumbnail"
	SourceEpisodeExplicit  SourceReadPurpose = "explicit_episode_thumbnail"
)

// SourceReadBinding is the exact source object passed back to the real caller's
// purpose-specific authority resolver. A purpose/pool label grants nothing.
type SourceReadBinding struct {
	Purpose                  SourceReadPurpose
	Observation              ObservationRef
	Path, Root, ScanRevision string
	Size                     int64
	Sequential               bool
	Profile                  SourceReadProfile
	LeaseID                  int64
	// captureMode is source-issued only; it does not alter Sequential.
	captureMode captureReadMode
}

type SourceSessionAdmission struct {
	Purpose     SourceReadPurpose
	Observation ObservationRef
	// CheckTx must resolve the actual actor/session or job, library, target and
	// operation/cost against THIS binding. No default or generic Allow exists.
	CheckTx   func(context.Context, *sql.Tx, SourceReadBinding) error
	OnChanged func()
}

// SourceReadLease belongs to one actual caller session/job. It holds no public
// bearer ID and cannot be retargeted after capture or a physical-process change.
// Capture is DB-only; all provider I/O is lazy and outside caller transactions.
type SourceReadLease struct {
	service           *Service
	binding           SourceReadBinding
	admission         SourceSessionAdmission
	life              context.Context
	cancel            context.CancelFunc
	mu                sync.Mutex
	openMu            sync.Mutex
	terminal          error
	processGeneration string
	notified          bool
	stopping          bool
	children          map[io.Closer]struct{}
}

func (s *Service) CaptureReadSourceTx(ctx context.Context, tx *sql.Tx, admission SourceSessionAdmission) (*SourceReadLease, error) {
	return s.captureReadSourceTx(ctx, tx, admission, "")
}

func (s *Service) captureReadSourceTx(ctx context.Context, tx *sql.Tx, admission SourceSessionAdmission, captureMode captureReadMode) (*SourceReadLease, error) {
	if s == nil || s.db == nil || tx == nil || admission.CheckTx == nil || admission.OnChanged == nil {
		return nil, storage.ErrRemoteConfig
	}
	switch admission.Purpose {
	case SourceReading, SourcePlayback, SourceMediaAnalysis, SourceEpisodeAutomatic, SourceEpisodeExplicit:
	default:
		return nil, storage.ErrRemoteConfig
	}
	ref := admission.Observation
	if !validObservationObject(ref.Object) || ref.Revision < 1 {
		return nil, storage.ErrRemoteConfig
	}
	generation, err := observationGenerationTx(ctx, tx, ref.SourceID)
	if err != nil {
		return nil, err
	}
	if generation != ref.Generation {
		return nil, storage.ErrRemoteChanged
	}
	profile, err := SourceReadProfileTx(ctx, tx, ref.SourceID)
	if err != nil {
		return nil, err
	}
	analysis := admission.Purpose == SourceMediaAnalysis || admission.Purpose == SourceEpisodeAutomatic || admission.Purpose == SourceEpisodeExplicit
	if analysis && !profile.AllowAdvancedMediaAnalysis {
		return nil, storage.ErrRemoteAnalysisDisabled
	}
	if analysis && assets.DiscImage(ref.Object) {
		// Root64 acquires a remote disc only for actual Play. Analysis of owned
		// playback bytes needs a separate exact custody adapter, never this origin.
		return nil, mediaanalysis.ErrUnsupported
	}
	scan, err := readScanObservationTx(ctx, tx, ref.SourceID, ref.Object)
	if err != nil {
		return nil, err
	}
	if scan.Ref != ref {
		return nil, storage.ErrRemoteChanged
	}
	var root string
	if err = tx.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, ref.SourceID).Scan(&root); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, storage.ErrRemoteConfig
	}
	sequential, err := sourceObjectSequentialTx(ctx, tx, ref.SourceID, filepath.Join(root, filepath.FromSlash(ref.Object)))
	if err != nil {
		return nil, err
	}
	id := s.readLeaseSequence.Add(1)
	if id < 1 {
		return nil, storage.ErrRemoteLimit
	}
	binding := SourceReadBinding{Purpose: admission.Purpose, Observation: ref, Path: filepath.Join(root, filepath.FromSlash(ref.Object)), Root: root, ScanRevision: scan.Observation.ScanRevision, Size: scan.Observation.Facts.Size, Sequential: sequential, Profile: profile, LeaseID: id, captureMode: captureMode}
	if captureMode != "" {
		plan, err := captureReadPlanTx(ctx, tx, ref.SourceID, binding.Path)
		if err != nil {
			return nil, err
		}
		if admission.Purpose != SourcePlayback || !assets.DiscImage(ref.Object) || binding.Size <= 0 || plan.mode != captureMode || plan.sourceSequential != sequential {
			return nil, storage.ErrRemoteChanged
		}
	}
	callbackCtx, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = admission.CheckTx(callbackCtx, tx, binding); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	lease := &SourceReadLease{service: s, binding: binding, admission: admission, life: life, cancel: cancel}
	s.serveMu.Lock()
	if s.serveClosed {
		s.serveMu.Unlock()
		cancel()
		return nil, storage.ErrRemoteOffline
	}
	if s.sourceRemovals[ref.SourceID] {
		s.serveMu.Unlock()
		cancel()
		return nil, storage.ErrBusy
	}
	if s.readLeases == nil {
		s.readLeases = make(map[string]map[int64]*SourceReadLease)
	}
	if s.readLeases[ref.SourceID] == nil {
		s.readLeases[ref.SourceID] = make(map[int64]*SourceReadLease)
	}
	s.readLeases[ref.SourceID][id] = lease
	s.serveMu.Unlock()
	// Owner expiry/close retires activity even when no further extent arrives.
	// The callback does no DB, provider I/O or user notification.
	context.AfterFunc(life, func() { lease.fail(life.Err()); lease.closeChildren() })
	return lease, nil
}
func (l *SourceReadLease) Binding() SourceReadBinding { return l.binding }
func (l *SourceReadLease) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if l == nil || ctx == nil || tx == nil {
		return storage.ErrRemoteConfig
	}
	l.mu.Lock()
	terminal := l.terminal
	l.mu.Unlock()
	if terminal != nil {
		return terminal
	}
	if err := ctx.Err(); err != nil {
		return l.checkFailure(ctx, err)
	}
	if err := l.life.Err(); err != nil {
		return l.checkFailure(ctx, err)
	}
	callbackCtx, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return l.checkFailure(ctx, err)
	}
	if err := l.admission.CheckTx(callbackCtx, tx, l.binding); err != nil {
		return l.checkFailure(ctx, err)
	}
	generation, err := observationGenerationTx(ctx, tx, l.binding.Observation.SourceID)
	if err != nil {
		return l.checkFailure(ctx, err)
	}
	profile, err := SourceReadProfileTx(ctx, tx, l.binding.Observation.SourceID)
	if err != nil {
		return l.checkFailure(ctx, err)
	}
	scan, err := readScanObservationTx(ctx, tx, l.binding.Observation.SourceID, l.binding.Observation.Object)
	if err != nil {
		return l.checkFailure(ctx, err)
	}
	var root string
	err = tx.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, l.binding.Observation.SourceID).Scan(&root)
	if err != nil {
		return l.checkFailure(ctx, err)
	}
	sequential, err := sourceObjectSequentialTx(ctx, tx, l.binding.Observation.SourceID, l.binding.Path)
	if err != nil {
		return l.checkFailure(ctx, err)
	}
	if l.binding.captureMode != "" {
		plan, err := captureReadPlanTx(ctx, tx, l.binding.Observation.SourceID, l.binding.Path)
		if err != nil {
			return l.checkFailure(ctx, err)
		}
		if plan.mode != l.binding.captureMode || plan.sourceSequential != l.binding.Sequential {
			return l.fail(storage.ErrRemoteChanged)
		}
	}
	if generation != l.binding.Observation.Generation || root != l.binding.Root || profile != l.binding.Profile || scan.Ref != l.binding.Observation || scan.Observation.ScanRevision != l.binding.ScanRevision || scan.Observation.Facts.Size != l.binding.Size || sequential != l.binding.Sequential {
		return l.fail(storage.ErrRemoteChanged)
	}
	l.mu.Lock()
	epoch := l.processGeneration
	l.mu.Unlock()
	if epoch != "" {
		l.service.serveMu.Lock()
		slot := l.service.serveLeases[l.binding.Observation.SourceID]
		l.service.serveMu.Unlock()
		if slot == nil || slot.CheckLocal() != nil {
			return l.fail(storage.ErrRemoteOffline)
		}
		l.service.serveMu.Lock()
		process := slot.process
		l.service.serveMu.Unlock()
		if process == nil || process.Generation() != epoch {
			return l.fail(storage.ErrRemoteOffline)
		}
		if err = process.CheckLocal(ctx); err != nil {
			return l.checkFailure(ctx, err)
		}
		network, e := l.service.sourceNetworkTx(ctx, tx, slot.source, slot.root)
		sequential, se := sourceSequentialTx(ctx, tx, slot.source, slot.root)
		if e != nil {
			return l.fail(e)
		}
		if se != nil {
			return l.fail(se)
		}
		if network.Fingerprint != slot.networkFingerprint || sequential != slot.sequential || slot.captureMode != l.binding.captureMode {
			slot.cancel()
			return l.fail(storage.ErrRemoteChanged)
		}
	}
	return nil
}
func (l *SourceReadLease) Check(ctx context.Context) error {
	if l == nil {
		return storage.ErrRemoteConfig
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, l.service.db)
	if err != nil {
		return l.checkFailure(ctx, err)
	}
	err = l.CheckTx(ctx, snapshot.Tx())
	snapshot.Rollback()
	l.notifyChanged()
	return err
}

// checkFailure never turns an exact canceled child DB request into parent
// retirement. Actual observed authority/source failures remain terminal. This
// function is safe inside the caller's transaction and opens no nested reader.
func (l *SourceReadLease) checkFailure(ctx context.Context, err error) error {
	if (err == context.Canceled || err == context.DeadlineExceeded) && ctx.Err() == err && l.life.Err() == nil {
		l.mu.Lock()
		terminal := l.terminal
		l.mu.Unlock()
		if terminal != nil {
			return terminal
		}
		return err
	}
	return l.fail(err)
}

// childFailure is outside SQL. On request cancellation, recheck the real
// retained lifetime's current DB authority before preserving the parent. This
// does not detach I/O, open a replacement source or shield concurrent revocation.
func (l *SourceReadLease) childFailure(ctx context.Context, err error) error {
	if (err == context.Canceled || err == context.DeadlineExceeded) && l.life.Err() == nil {
		if current := l.Check(l.life); current != nil {
			return current
		}
		return err
	}
	return l.fail(err)
}

func (l *SourceReadLease) fail(err error) error {
	l.mu.Lock()
	first := l.terminal == nil
	if first {
		l.terminal = err
		l.cancel()
	}
	out := l.terminal
	l.mu.Unlock()
	if first {
		l.service.serveMu.Lock()
		readers := l.service.readLeases[l.binding.Observation.SourceID]
		if readers[l.binding.LeaseID] == l {
			delete(readers, l.binding.LeaseID)
			if len(readers) == 0 {
				delete(l.service.readLeases, l.binding.Observation.SourceID)
			}
		}
		if l.binding.captureMode != "" && len(readers) == 0 {
			if slot := l.service.serveLeases[l.binding.Observation.SourceID]; slot != nil && slot.captureMode == l.binding.captureMode && slot.generation == l.binding.Observation.Generation && slot.profile == l.binding.Profile && slot.root == l.binding.Root {
				// cancel never releases the slot: physical Wait still owns it.
				slot.retiring = true
				slot.cancel()
			}
		}
		l.service.serveMu.Unlock()
	}
	return out
}

// stopReadLeases cancels only captured logical readers. Process capacity stays
// with sourceProcessLease until physical Wait. No caller notice runs in a DB
// transaction or while service locks are held; Check delivers a changed notice.
func (s *Service) stopReadLeases(source string, reason error, keep *SourceReadProfile) {
	s.serveMu.Lock()
	var readers []*SourceReadLease
	var captured []context.CancelFunc
	for id, group := range s.readLeases {
		if source != "" && source != id {
			continue
		}
		for _, lease := range group {
			if keep == nil || lease.binding.Profile != *keep {
				readers = append(readers, lease)
			}
		}
	}
	for id, group := range s.captureReaders {
		if source != "" && id != source {
			continue
		}
		for lease := range group {
			if keep == nil || lease.profile != *keep {
				captured = append(captured, lease.cancel)
			}
		}
	}
	s.serveMu.Unlock()
	for _, cancel := range captured {
		cancel()
	}
	for _, lease := range readers {
		lease.fail(reason)
	}
}
func (l *SourceReadLease) notifyChanged() {
	l.mu.Lock()
	notify := !l.notified && errors.Is(l.terminal, storage.ErrRemoteChanged)
	if notify {
		l.notified = true
	}
	l.mu.Unlock()
	if notify {
		l.admission.OnChanged()
	}
}
func (l *SourceReadLease) changed() {
	// Retire this source's old VFS cache only after its physical process Wait.
	l.service.stopServeProcess(l.binding.Observation.SourceID)
	l.fail(storage.ErrRemoteChanged)
	l.notifyChanged()
}
func (l *SourceReadLease) Close() error {
	l.fail(storage.ErrRemoteOffline)
	l.closeChildren()
	return nil
}

func (l *SourceReadLease) closeChildren() {
	l.mu.Lock()
	children := make([]io.Closer, 0, len(l.children))
	for child := range l.children {
		children = append(children, child)
	}
	l.mu.Unlock()
	for _, child := range children {
		child.Close()
	}
}

func (l *SourceReadLease) keepChild(child io.Closer) error {
	l.mu.Lock()
	err := l.terminal
	if err == nil && (l.stopping || l.life.Err() != nil) {
		err = storage.ErrRemoteOffline
	}
	if err == nil {
		if l.children == nil {
			l.children = make(map[io.Closer]struct{})
		}
		l.children[child] = struct{}{}
	}
	l.mu.Unlock()
	if err != nil {
		child.Close()
	}
	return err
}

func (l *SourceReadLease) dropChild(child io.Closer) {
	l.mu.Lock()
	delete(l.children, child)
	l.mu.Unlock()
}
func (l *SourceReadLease) bound(ctx context.Context) (context.Context, func()) {
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.life, cancel)
	return call, func() { stop(); cancel() }
}
func (l *SourceReadLease) process(ctx context.Context) (*rcloneprogram.ServeProcess, error) {
	if err := l.Check(ctx); err != nil {
		return nil, err
	}
	l.mu.Lock()
	expected := l.processGeneration
	l.mu.Unlock()
	process, err := l.service.ensureSourceServe(ctx, l.binding, expected)
	if err != nil {
		if errors.Is(err, storage.ErrBusy) {
			return nil, err
		}
		return nil, l.childFailure(ctx, err)
	}
	l.mu.Lock()
	if l.processGeneration == "" {
		l.processGeneration = process.Generation()
	}
	changed := l.processGeneration != process.Generation()
	l.mu.Unlock()
	if changed {
		return nil, l.fail(storage.ErrRemoteChanged)
	}
	if err = l.Check(ctx); err != nil {
		return nil, err
	}
	return process, nil
}
func (l *SourceReadLease) OpenRange(ctx context.Context) (*SourceRange, error) {
	if l.binding.captureMode != "" {
		return nil, storage.ErrRemoteRange
	}
	l.openMu.Lock()
	defer l.openMu.Unlock()
	if err := l.canOpen(); err != nil {
		return nil, err
	}
	if l.binding.Sequential {
		if err := l.Check(ctx); err != nil {
			return nil, err
		}
		if err := l.service.requireWebDAVRandom(ctx, l.binding.Observation.SourceID); err != nil {
			return nil, err
		}
		return nil, storage.ErrRemoteRange
	}
	call, release := l.bound(ctx)
	defer release()
	process, err := l.process(call)
	if err != nil {
		return nil, err
	}
	session, err := l.service.OpenFreshRange(call, process, FreshReadAdmission{Observation: l.binding.Observation, ProcessGeneration: process.Generation(), Check: l.Check, OnChanged: l.changed})
	if err != nil {
		return nil, l.childFailure(ctx, err)
	}
	child := &SourceRange{lease: l, session: session}
	if err = l.keepChild(child); err != nil {
		return nil, err
	}
	return child, nil
}
func (l *SourceReadLease) OpenSequential(ctx context.Context) (*SourceSequential, error) {
	l.openMu.Lock()
	defer l.openMu.Unlock()
	if err := l.canOpen(); err != nil {
		return nil, err
	}
	if l.binding.Purpose != SourceReading && l.binding.Purpose != SourcePlayback && l.binding.Purpose != SourceMediaAnalysis {
		return nil, storage.ErrRemoteRange
	}
	if err := l.Check(ctx); err != nil {
		return nil, err
	}
	if err := l.service.requireWebDAVSequential(ctx, l.binding.Observation.SourceID); err != nil {
		return nil, err
	}
	call, release := l.bound(ctx)
	process, err := l.process(call)
	if err != nil {
		release()
		return nil, err
	}
	session, err := l.service.OpenFreshSequential(call, process, FreshReadAdmission{Observation: l.binding.Observation, ProcessGeneration: process.Generation(), Check: l.Check, OnChanged: l.changed})
	if err != nil {
		release()
		return nil, l.childFailure(ctx, err)
	}
	child := &SourceSequential{lease: l, session: session, ctx: call, release: release}
	if err = l.keepChild(child); err != nil {
		return nil, err
	}
	return child, nil
}

type SourceRange struct {
	lease   *SourceReadLease
	session *RangeSession
	closed  atomic.Bool
}

func (r *SourceRange) ReadAt(ctx context.Context, p []byte, offset int64) (int, error) {
	if r.closed.Load() {
		return 0, storage.ErrRemoteOffline
	}
	call, release := r.lease.bound(ctx)
	defer release()
	n, err := r.lease.service.readSharedSourceRange(call, r, p, offset)
	if errors.Is(err, storage.ErrBusy) {
		return n, err
	}
	if err == errSharedSourceRangeInterrupted {
		r.Close()
		if current := r.lease.Check(call); current != nil {
			return 0, current
		}
		return 0, err
	}
	if err != nil && !errors.Is(err, io.EOF) {
		closed := r.closed.Load()
		r.Close()
		if closed {
			if current := r.lease.Check(r.lease.life); current != nil {
				return n, current
			}
			return n, err
		}
		err = r.lease.childFailure(call, err)
		r.lease.notifyChanged()
	}
	return n, err
}
func (r *SourceRange) Check(ctx context.Context) error {
	if r.closed.Load() {
		return storage.ErrRemoteOffline
	}
	call, release := r.lease.bound(ctx)
	defer release()
	if err := r.session.Check(call); err != nil {
		r.Close()
		return r.lease.childFailure(call, err)
	}
	return nil
}
func (r *SourceRange) Close() error {
	r.closed.Store(true)
	err := r.session.Close()
	r.lease.dropChild(r)
	return err
}

type SourceSequential struct {
	lease   *SourceReadLease
	session *SequentialSession
	ctx     context.Context
	release func()
	once    sync.Once
	closed  atomic.Bool
}

func (r *SourceSequential) Read(p []byte) (int, error) {
	if r.closed.Load() {
		return 0, storage.ErrRemoteOffline
	}
	n, err := r.session.Read(r.ctx, p)
	if err != nil && !errors.Is(err, io.EOF) {
		closed := r.closed.Load()
		r.Close()
		if closed {
			if current := r.lease.Check(r.lease.life); current != nil {
				return n, current
			}
			return n, err
		}
		err = r.lease.childFailure(r.ctx, err)
		r.lease.notifyChanged()
	}
	return n, err
}
func (r *SourceSequential) Check(ctx context.Context) error {
	if r.closed.Load() {
		return storage.ErrRemoteOffline
	}
	call, release := r.lease.bound(ctx)
	defer release()
	if err := r.session.Check(call); err != nil {
		r.Close()
		return r.lease.childFailure(call, err)
	}
	return nil
}
func (r *SourceSequential) StopAndRecheck(ctx context.Context) error {
	if r.closed.Load() {
		return storage.ErrRemoteOffline
	}
	call, release := r.lease.bound(ctx)
	defer release()
	err := r.session.StopAndRecheck(call)
	r.Close()
	if err != nil {
		return r.lease.childFailure(call, err)
	}
	return r.lease.Check(call)
}
func (r *SourceSequential) Close() error {
	r.once.Do(func() { r.closed.Store(true); r.session.Close(); r.release(); r.lease.dropChild(r) })
	return nil
}
