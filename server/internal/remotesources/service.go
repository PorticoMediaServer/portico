// Package remotesources binds remote sources to the baseline storage interface.
// It owns source configuration, private listing continuations and acquisitions;
// library identity, scan scheduling, reconciliation and playback authority remain
// with their existing owners.
package remotesources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type Source struct {
	ID                string     `json:"id"`
	Kind              SourceKind `json:"kind"`
	Name              string     `json:"name"`
	RootPath          string     `json:"rootPath"`
	Generation        int64      `json:"generation"`
	State             string     `json:"state"`
	ErrorCode         string     `json:"errorCode,omitempty"`
	RangeSupport      string     `json:"rangeSupport"`
	Origin            string     `json:"origin,omitempty"`
	InsecureLocal     bool       `json:"insecureLocal"`
	CredentialPresent bool       `json:"credentialPresent"`
	MountID           string     `json:"mountId,omitempty"`
}
type Service struct {
	db                  *sql.DB
	mounts              *mounts.Service
	private             string
	commands            sync.Mutex
	serveMu             sync.Mutex
	serveLeases         map[string]*sourceProcessLease
	sourceTests         map[string]*sourceTestScope
	sourceRemovals      map[string]bool
	readLeases          map[string]map[int64]*SourceReadLease
	captureReaders      map[string]map[*captureRetirement]struct{}
	serveClosed         bool
	serveLife           context.Context
	readLeaseSequence   atomic.Int64
	sharedRangeMu       sync.Mutex
	sharedRanges        map[sourceRangeKey]*sourceRangeRead
	sharedRangeCount    map[string]int
	sharedRangeJoiners  atomic.Uint64
	mu                  sync.Mutex
	active              map[string]map[string]activeRead
	bindings            atomic.Value
	listings            sync.Mutex
	listingClaims       map[string]bool
	cacheMu             sync.Mutex
	cacheLife           sync.RWMutex
	cacheBudgets        map[string]*sourceCacheBudget
	cacheGuards         map[string]*cacheGuard
	cacheReconcile      struct{ source, key string }
	nativeListings      map[string]*nativeListing
	nativeClosed        bool
	opsMu               sync.Mutex
	opsGates            map[string]*sourceOpsGate
	inventory, playback chan struct{}
	resolver            remotemedia.Resolver
	// contacts coalesces provider contact evidence between worker wakes; it
	// never opens the database per read, chunk or file.
	contactsMu       sync.Mutex
	contacts         map[contactKey]sourceContact
	contactsReserved int
	contactSeq       uint64
	// captureActive names source acquisitions whose original caller is still
	// live; captureGrowth retains the shared disk reservation until builder
	// physical cleanup calls ReleaseTx or bounded no-journal recovery settles it.
	captureMu       sync.Mutex
	captureRecovery string
	capturesActive  map[string]bool
	captureGrowth   map[string]*diskGrowthReservation
	// Listing-cache prune windows; only the Run goroutine touches them, and a
	// restart begins again from the start, which is one bounded pass.
	pruneListings    listingPruneCursor
	pruneChecks      orphanPruneCursor
	pruneScanCursors orphanPruneCursor
}
type activeRead struct {
	cancel context.CancelFunc
	lane   string
}
type binding struct {
	ID, Kind, Root string
	MountID        string
	Removed        bool
}

func New(db *sql.DB, state string, managed *mounts.Service) (*Service, error) {
	if db == nil || managed == nil {
		return nil, storage.ErrRemoteConfig
	}
	private := storagepaths.Path(state, "remote-sources")
	if err := os.MkdirAll(private, 0700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(private)
	if err != nil || canonical != private {
		return nil, storage.ErrRemoteConfig
	}
	if err = os.Chmod(private, 0700); err != nil {
		return nil, err
	}

	s := &Service{db: db, mounts: managed, private: private, active: map[string]map[string]activeRead{}, inventory: make(chan struct{}, 2), playback: make(chan struct{}, 8)}
	if err = s.RefreshMounts(context.Background()); err != nil {
		return nil, err
	}
	managed.NativeBusy = s.PlaybackActive
	managed.GuardSourceRemoval = s.guardSourceRemoval
	managed.NativeInvalidated = s.invalidate
	managed.PrepareSourceCandidate = s.prepareSourceCandidate
	managed.PrepareSourceEgress = s.prepareMountEgress
	// No acquisition survives a server process. Remove only our private spool names.
	if files, e := os.ReadDir(private); e == nil {
		for _, file := range files {
			if !file.IsDir() && strings.HasPrefix(file.Name(), "acquisition-") {
				_ = os.Remove(filepath.Join(private, file.Name()))
			}
		}
	}
	return s, nil
}
func (s *Service) RefreshMounts(ctx context.Context) error {
	rows, err := s.mounts.List()
	if err != nil {
		return err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	for _, m := range rows {
		_, err = tx.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation,range_support,credential_present,mount_id) VALUES(?,'rclone',?,?,?,'sequential',1,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,generation=excluded.generation,removed=?,state=CASE WHEN ? THEN 'removing' WHEN remote_sources.generation!=excluded.generation THEN 'ready' ELSE remote_sources.state END,error_code=CASE WHEN remote_sources.generation!=excluded.generation THEN '' ELSE remote_sources.error_code END WHERE remote_sources.name IS NOT excluded.name OR remote_sources.generation IS NOT excluded.generation OR remote_sources.removed IS NOT ?`, m.ID, m.Name, m.MountPath, m.BackendRevision, m.ID, m.RemovalPending, m.RemovalPending, m.RemovalPending)
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE remote_sources SET removed=1,state='removed' WHERE kind IN('rclone','sftp','smb') AND (removed!=1 OR state!='removed') AND NOT EXISTS(SELECT 1 FROM managed_mounts m WHERE m.id=remote_sources.mount_id)`); err != nil {
		return err
	}
	if err = gated.Commit(); err != nil {
		return err
	}
	return s.refreshBindings(ctx)
}
func (s *Service) refreshBindings(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,root,mount_id,removed FROM remote_sources`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []binding{}
	for rows.Next() {
		var b binding
		if err = rows.Scan(&b.ID, &b.Kind, &b.Root, &b.MountID, &b.Removed); err != nil {
			return err
		}
		out = append(out, b)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	s.bindings.Store(out)
	return nil
}
func (s *Service) find(path string) (binding, string, bool) {
	if !filepath.IsAbs(path) {
		return binding{}, "", false
	}
	rows, _ := s.bindings.Load().([]binding)
	for _, b := range rows {
		relative, err := filepath.Rel(b.Root, path)
		if err == nil && filepath.IsLocal(relative) {
			if relative == "." {
				relative = ""
			}
			return b, filepath.ToSlash(relative), true
		}
	}
	return binding{}, "", false
}
func (s *Service) Handles(path string) bool { _, _, ok := s.find(path); return ok }
func (s *Service) List(ctx context.Context) ([]Source, error) {
	if err := s.RefreshMounts(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,name,root,generation,state,error_code,range_support,origin,insecure,credential_present,mount_id FROM remote_sources WHERE removed=0 ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var x Source
		if err = rows.Scan(&x.ID, &x.Kind, &x.Name, &x.RootPath, &x.Generation, &x.State, &x.ErrorCode, &x.RangeSupport, &x.Origin, &x.InsecureLocal, &x.CredentialPresent, &x.MountID); err != nil {
			return nil, err
		}
		if x.ErrorCode == "" && s.sourcePacerBackoff(x.ID, strconv.FormatInt(x.Generation, 10)) {
			x.State, x.ErrorCode = "rate_limited", "rate_limited"
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) get(ctx context.Context, id string) (Source, error) {
	var x Source
	err := s.db.QueryRowContext(ctx, `SELECT id,kind,name,root,generation,state,error_code,range_support,origin,insecure,credential_present,mount_id FROM remote_sources WHERE id=? AND removed=0`, id).Scan(&x.ID, &x.Kind, &x.Name, &x.RootPath, &x.Generation, &x.State, &x.ErrorCode, &x.RangeSupport, &x.Origin, &x.InsecureLocal, &x.CredentialPresent, &x.MountID)
	if errors.Is(err, sql.ErrNoRows) {
		return x, storage.ErrRemoteOffline
	}
	return x, err
}
func (s *Service) dav(ctx context.Context, id string) (*remotemedia.DAV, string, error) {
	var sealed []byte
	var generation int64
	err := s.db.QueryRowContext(ctx, `SELECT config,generation FROM remote_sources WHERE id=? AND kind='webdav' AND removed=0`, id).Scan(&sealed, &generation)
	if err != nil {
		return nil, "", storage.ErrRemoteOffline
	}
	raw, err := s.mounts.Open(sealed)
	if err != nil {
		return nil, "", storage.ErrRemoteCredentials
	}
	defer clear(raw)
	var config remotemedia.DAVConfig
	if json.Unmarshal(raw, &config) != nil {
		return nil, "", storage.ErrRemoteConfig
	}
	d, err := remotemedia.NewDAV(config, s.resolver)
	return d, strconv.FormatInt(generation, 10), mapError(err)
}
func (s *Service) generation(ctx context.Context, b binding) (string, error) {
	if managedSourceKind(b.Kind) {
		n, err := s.mounts.Native(ctx, b.ID)
		if err != nil {
			return "", err
		}
		return n.Generation(), nil
	}
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT generation FROM remote_sources WHERE id=? AND removed=0`, b.ID).Scan(&n)
	if err != nil {
		return "", storage.ErrRemoteOffline
	}
	return strconv.FormatInt(n, 10), nil
}
func (s *Service) check(ctx context.Context, b binding, generation string) error {
	got, err := s.generation(ctx, b)
	if err != nil {
		return err
	}
	if got != generation {
		return storage.ErrRemoteChanged
	}
	return nil
}
func mapError(err error) error {
	switch {
	case errors.Is(err, rcloneprogram.ErrServeProgram):
		return storage.ErrRemoteBinary
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, mediasource.ErrSourceChanged):
		return storage.ErrRemoteChanged
	case errors.Is(err, remotemedia.ErrObservationChanged):
		return storage.ErrRemoteChanged
	case errors.Is(err, remotemedia.ErrDenied):
		return storage.ErrRemoteCredentials
	case errors.Is(err, remotemedia.ErrPolicy), errors.Is(err, remotemedia.ErrDAVResponse):
		return storage.ErrRemoteConfig
	case errors.Is(err, remotemedia.ErrDAVToken):
		return storage.ErrRemoteCursor
	case errors.Is(err, remotemedia.ErrRepresentationRange):
		return storage.ErrRemoteRange
	case errors.Is(err, remotemedia.ErrBusy):
		return storage.ErrBusy
	default:
		return err
	}
}
func ErrorCode(err error) string {
	if errors.Is(err, errUnknownWebDAVRange) {
		return "range_unknown"
	}
	var response *remotemedia.ResponseFailure
	if errors.As(err, &response) {
		if response.Kind == "throttled" {
			return "rate_limited"
		}
		if response.Kind == "quota" {
			return "quota_exceeded"
		}
	}
	switch {
	case errors.Is(err, storage.ErrRemoteChanged), errors.Is(err, mediasource.ErrSourceChanged):
		return "source_changed"
	case errors.Is(err, storage.ErrRemoteCredentials), errors.Is(err, remotemedia.ErrDenied):
		return "credentials_required"
	case errors.Is(err, storage.ErrRemoteBinary):
		return "binary_changed"
	case errors.Is(err, storage.ErrRemoteRange):
		return "range_unsupported"
	case errors.Is(err, storage.ErrRemoteConfig):
		return "invalid_configuration"
	case errors.Is(err, storage.ErrRemoteCursor):
		return "cursor_expired"
	case errors.Is(err, storage.ErrRemoteLimit):
		return "budget_exceeded"
	case errors.Is(err, storage.ErrBusy), errors.Is(err, remotemedia.ErrBusy):
		return "capacity_busy"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "offline"
	}
}
func (s *Service) health(live bool, b binding, generation string, err error) {
	// Scheduling/capacity contention is not an asynchronous provider report.
	// Leave the last actual source health intact instead of claiming throttling.
	if errors.Is(err, storage.ErrBusy) || errors.Is(err, remotemedia.ErrBusy) {
		return
	}
	// live is the original permit/process custody captured before its own
	// cleanup. A retired custody means no provider answer, so neither source
	// state nor contact evidence may be recorded from this outcome.
	if !live {
		return
	}
	code, state := "", "healthy"
	if err != nil {
		code = ErrorCode(err)
		state = "degraded"
		if code == "credentials_required" || code == "binary_changed" || code == "offline" || code == "rate_limited" {
			state = code
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, rcloneprogram.ErrServeStopped) {
		return
	}
	// Contact history is coalesced in memory here and written by the worker
	// wake, never once per read or file. An unknown or non-provider error is
	// ignored by the recorder, and a stopping serve runtime is retirement, not
	// a provider failure.
	if !b.Removed && !s.serveStopping() {
		s.noteSourceContact(b.ID, b.MountID, generation, time.Now(), err)
	}
	_, _ = dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassBackgroundMedia, `UPDATE remote_sources SET state=?,error_code=? WHERE id=? AND CASE WHEN kind IN('rclone','sftp','smb') THEN (SELECT CAST(m.generation AS TEXT) FROM mount_backend_configs m WHERE m.mount_id=remote_sources.mount_id) ELSE CAST(generation AS TEXT) END=? AND removed=0`, state, code, b.ID, generation)
}
func davSnapshot(e remotemedia.DAVEntry, root string) storage.Snapshot {
	modified := int64(0)
	if t, err := httpTime(e.Modified); err == nil {
		modified = t.UnixNano()
	}
	raw, _ := json.Marshal([]any{e.Relative, e.ETag, e.Size, e.Modified, e.ContentType, e.Directory})
	return storage.Snapshot{Path: filepath.Join(root, filepath.FromSlash(e.Relative)), Name: filepath.Base(e.Relative), Size: e.Size, ModifiedNS: modified, Directory: e.Directory, ObjectID: identity.Digest(e.Relative), Revision: identity.Digest(string(raw))}
}
func httpTime(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		if t, e := time.Parse(layout, v); e == nil {
			return t, nil
		}
	}
	return time.Time{}, storage.ErrRemoteConfig
}
func (s *Service) Stat(ctx context.Context, path string) (storage.Snapshot, error) {
	b, relative, ok := s.find(path)
	if !ok || b.Removed {
		return storage.Snapshot{}, storage.ErrRemoteOffline
	}
	return s.statSourceMetadata(ctx, b, relative)
}
func (s *Service) InspectRoot(ctx context.Context, path string) (storage.Snapshot, error) {
	v, err := s.Stat(ctx, path)
	if err == nil && !v.Directory {
		err = storage.ErrRemoteConfig
	}
	if err == nil {
		v = inventoryRootSnapshot(v)
	}
	return v, err
}
func (s *Service) PlaybackActive(id string) bool {
	s.mu.Lock()
	for _, r := range s.active[id] {
		if r.lane == "playback" {
			s.mu.Unlock()
			return true
		}
	}
	s.mu.Unlock()
	s.serveMu.Lock()
	defer s.serveMu.Unlock()
	for _, lease := range s.readLeases[id] {
		if lease.binding.Purpose == SourceReading || lease.binding.Purpose == SourcePlayback {
			return true
		}
	}
	return false
}
func (s *Service) invalidate(id string) {
	s.retireSourceOperations(id)
	s.stopServeProcess(id)
	s.stopReadLeases(id, storage.ErrRemoteChanged, nil)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.active[id] {
		r.cancel()
	}
}

// One source generation owns one operation budget. The rclone serve process,
// direct WebDAV reads, direct saved-source folder lists and an existing-source
// connection test all take the same permits, so the saved Connections ceiling
// is one aggregate bound and a test's exclusive hold drains every path. The
// budget is rebuilt when the generation or the saved profile revision moves,
// cancelled on retirement, and never shared across sources. Capacity only:
// saved TPS/BPS pacing stays with the rclone serve process for now.
//
// A gate can also be stopping: a connection test has released its hold but its
// temporary process has not finished its physical Wait. stopping gates are kept
// (cancelled, retired) so no caller builds an independent budget until the
// test's own cleanup closes that barrier.
type sourceOpsGate struct {
	generation string
	revision   int64
	budget     *remotemedia.OperationBudget
	pacer      *sourceRatePacer
	cancel     context.CancelFunc
	stopping   chan struct{}
	retired    bool
}

// sourceRatePacer spaces direct WebDAV provider requests at the source's saved
// requests-per-second and bytes-per-second. Only direct callers use it: the
// rclone serve applies the same saved limits with its own options, and nobody
// claims the two independent pacers add up to one aggregate rate. Every wait
// uses the caller's original context, so cancellation, retirement and the
// connection-test hold end it exactly as they end the provider request.
type sourceRatePacer struct {
	requests *sourceRateBucket
	bytes    *sourceRateBucket
}

const directPacedChunk = 64 << 10

func newSourceRatePacer(limits ReadLimits) *sourceRatePacer {
	return &sourceRatePacer{
		requests: newSourceRateBucket(float64(limits.RequestsPerSecond), 1),
		bytes:    newSourceRateBucket(float64(limits.BytesPerSecond), directPacedChunk),
	}
}

// waitRequest takes one request token. Its burst is exactly one request, so a
// direct operation starts immediately and later ones are spaced.
func (p *sourceRatePacer) waitRequest(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.requests.take(ctx, 1)
}

// waitBytes takes n byte tokens before those bytes are delivered. Its burst is
// one bounded directPacedChunk, so the first buffer cannot bypass a slow rate
// with an arbitrarily large read; pacedReader caps each read to that chunk.
func (p *sourceRatePacer) waitBytes(ctx context.Context, n int64) error {
	if p == nil || n <= 0 {
		return nil
	}
	return p.bytes.take(ctx, float64(n))
}

// sourceRateBucket is a cancellable token bucket with no reserved future
// timestamps: a wait that is cancelled never deducts tokens, so an abandoned
// caller cannot accumulate debt that starves later legitimate ones. Rate zero
// means unlimited.
type sourceRateBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
	rate   float64
	burst  float64
}

func newSourceRateBucket(rate, burst float64) *sourceRateBucket {
	return &sourceRateBucket{tokens: burst, rate: rate, burst: burst}
}

func (b *sourceRateBucket) take(ctx context.Context, n float64) error {
	if b == nil || b.rate <= 0 || n <= 0 {
		return nil
	}
	for {
		b.mu.Lock()
		now := time.Now()
		if !b.last.IsZero() {
			b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
		}
		b.last = now
		if b.tokens >= n {
			b.tokens -= n
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((n - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		if wait <= 0 {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
		timer.Stop()
	}
}

// sourceOperations gives a caller the source's shared operation budget, but
// only after any registered connection test has finished physically. It reuses
// the existing test registration and stopping barrier rather than starting a
// second gate while an exclusive hold is still cleaning up, and re-checks after
// resolving so a test that registered meanwhile cannot be bypassed.
func (s *Service) sourceOperations(ctx context.Context, source, generation string) (*sourceOpsGate, error) {
	for {
		if waited, err := s.waitSourceTest(ctx, source); err != nil {
			return nil, err
		} else if waited {
			continue
		}
		gate, err := s.sourceOperationsNow(ctx, source, generation)
		if err != nil {
			return nil, err
		}
		if waited, err := s.waitSourceTest(ctx, source); err != nil {
			return nil, err
		} else if waited {
			continue
		}
		return gate, nil
	}
}

// sourceOperationsNow resolves or rebuilds the gate, waiting for any stopping
// test on it to finish its physical stop first. The test that owns the current
// registration reaches this before marking itself stopping.
func (s *Service) sourceOperationsNow(ctx context.Context, source, generation string) (*sourceOpsGate, error) {
	if s == nil || s.db == nil || source == "" || generation == "" {
		return nil, storage.ErrRemoteConfig
	}
	for {
		snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
		if err != nil {
			return nil, err
		}
		current, err := observationGenerationTx(ctx, snapshot.Tx(), source)
		if err == nil && current != generation {
			err = storage.ErrRemoteChanged
		}
		var profile SourceReadProfile
		if err == nil {
			profile, err = SourceReadProfileTx(ctx, snapshot.Tx(), source)
		}
		snapshot.Rollback()
		if err != nil {
			return nil, err
		}
		s.opsMu.Lock()
		gate := s.opsGates[source]
		if gate != nil && gate.stopping != nil {
			stopping := gate.stopping
			s.opsMu.Unlock()
			select {
			case <-stopping:
				// The test's temporary process has finished; resolve again.
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if gate != nil && !gate.retired && gate.generation == generation && gate.revision == profile.Revision {
			s.opsMu.Unlock()
			return gate, nil
		}
		life, cancel := context.WithCancel(context.Background())
		budget := remotemedia.NewOperationBudget(life, profile.Limits.Connections)
		if budget == nil {
			cancel()
			s.opsMu.Unlock()
			return nil, storage.ErrRemoteConfig
		}
		if gate != nil {
			gate.cancel()
		}
		if s.opsGates == nil {
			s.opsGates = map[string]*sourceOpsGate{}
		}
		gate = &sourceOpsGate{generation: generation, revision: profile.Revision, budget: budget, pacer: newSourceRatePacer(profile.Limits), cancel: cancel}
		s.opsGates[source] = gate
		s.opsMu.Unlock()
		return gate, nil
	}
}

// beginSourceTestStopping marks this exact budget as belonging to a test whose
// physical stop is still pending. New callers wait on the returned barrier and
// the test cleanup closes it only after the temporary process has been waited
// for, never on hold cancellation alone.
func (s *Service) beginSourceTestStopping(source string, budget *remotemedia.OperationBudget) (func(), error) {
	s.opsMu.Lock()
	gate := s.opsGates[source]
	if gate == nil || gate.budget != budget || gate.stopping != nil {
		s.opsMu.Unlock()
		return nil, storage.ErrBusy
	}
	stopping := make(chan struct{})
	gate.stopping = stopping
	s.opsMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.opsMu.Lock()
			if current := s.opsGates[source]; current == gate && current.stopping == stopping {
				close(stopping)
				current.stopping = nil
			}
			s.opsMu.Unlock()
		})
	}, nil
}

// retireSourceOperationsIfStale retires the gate only when its captured
// profile revision differs from the committed one. A repeated same-revision
// profile notification (an idempotent recovery or replay) must keep a matching
// live budget, exactly as the serve lease is kept when lease.profile == profile.
// Stopping-barrier custody is unchanged: a stale stopping gate is kept retired
// until its cleanup closes the barrier.
func (s *Service) retireSourceOperationsIfStale(source string, revision int64) {
	s.opsMu.Lock()
	if gate := s.opsGates[source]; gate != nil && gate.revision != revision {
		gate.cancel()
		if gate.stopping != nil {
			gate.retired = true
			s.opsMu.Unlock()
			return
		}
		delete(s.opsGates, source)
	}
	s.opsMu.Unlock()
}

// retireSourceOperations ends this source's operation gate so waiters stop with
// the generation. A stopping test's gate is kept, retired, until its cleanup
// closes the barrier so no caller can build a fresh gate beside it.
func (s *Service) retireSourceOperations(source string) {
	s.opsMu.Lock()
	if gate := s.opsGates[source]; gate != nil {
		gate.cancel()
		if gate.stopping != nil {
			gate.retired = true
			s.opsMu.Unlock()
			return
		}
		delete(s.opsGates, source)
	}
	s.opsMu.Unlock()
}

func (s *Service) retireAllSourceOperations() {
	s.opsMu.Lock()
	for source, gate := range s.opsGates {
		gate.cancel()
		delete(s.opsGates, source)
	}
	s.opsMu.Unlock()
}

// Run reconciles mounts and sweeps the listing cache. Both are consequences of
// writes — a source added, a scan finished, a job cancelled — so the loop is
// woken by commits rather than by a thirty-second clock that issued three delete
// transactions on an idle server forever. The safety tick is the backstop.
func (s *Service) Run(ctx context.Context) {
	if s.startReadRuntime(ctx) != nil {
		return
	}
	defer s.stopServeProcesses()
	defer s.stopNativeListings()
	defer s.retireAllSourceOperations()
	defer func() {
		s.mu.Lock()
		for _, reads := range s.active {
			for _, r := range reads {
				r.cancel()
			}
		}
		s.mu.Unlock()
	}()
	wake := worker.NewSignal()
	stopWake := registerRemoteSourceWake(wake)
	defer stopWake()
	worker.Run(ctx, "remotesources", wake, func(ctx context.Context) time.Duration {
		_ = s.RefreshMounts(ctx)
		// Idle serve processes are retired from the same bounded wake; active
		// readers, permits and tests keep their process.
		s.retireIdleServes(time.Now())
		// Provider contact evidence is coalesced in memory and written here in
		// bounded batches; a read or file never writes per operation. If a batch
		// remains, wake promptly instead of waiting for the full safety tick.
		contactPending := s.flushSourceContacts(ctx)
		// Bounded source recovery for a capture charge whose builder journal row
		// never appeared. The builder owns all owned-scratch/artifact cleanup.
		s.recoverCaptureCharges(ctx)
		// One bounded page per wake, resumable until every row has been checked.
		s.reconcileSourceCachePage(ctx)
		// A removed source's files become unavailable a bounded slice per wake.
		removalPending, _ := s.markRemovedSourceAssets(ctx)
		// Completed/failed old scan listings are disposable adapter cache; current
		// pending cursors keep their snapshots regardless of wall-clock age. The
		// prune advances bounded windows, so a remaining batch wakes promptly.
		prunePending := s.pruneListingCache(ctx)
		if removalPending {
			return time.Millisecond
		}
		if contactPending || prunePending {
			return time.Second
		}
		return 0
	})
}
func safeOrigin(root string) string {
	u, e := url.Parse(root)
	if e != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
