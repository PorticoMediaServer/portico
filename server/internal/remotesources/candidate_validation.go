package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type sourceTestScope struct {
	done   chan struct{}
	life   context.Context
	cancel context.CancelFunc
}

// Source tests queue new operations even when the source has no live process.
// An existing process keeps its exact epoch and readers; only its operation
// budget drains. No physical reader/session is force-retired for a test.
func (s *Service) waitSourceTest(ctx context.Context, source string) (bool, error) {
	s.serveMu.Lock()
	scope := s.sourceTests[source]
	s.serveMu.Unlock()
	if scope == nil {
		return false, nil
	}
	select {
	case <-scope.done:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

// beginCandidateTestAt takes one saved source's shared operation budget for an
// existing-source connection test. Its exclusive hold drains and queues every
// path that touches the source: the serve process, direct WebDAV reads, saved
// folder lists and the temporary serve. generation is the generation the
// candidate was authorized against; a moved source fails closed.
func (s *Service) beginCandidateTestAt(ctx context.Context, source, generation string, limits ReadLimits) (*remotemedia.ExclusiveTest, func(), error) {
	cold := source == ""
	if cold {
		source = identity.Token()
	}
	s.serveMu.Lock()
	if s.sourceTests == nil {
		s.sourceTests = map[string]*sourceTestScope{}
	}
	if s.serveClosed {
		s.serveMu.Unlock()
		return nil, nil, storage.ErrRemoteOffline
	}
	if s.sourceTests[source] != nil || s.sourceRemovals[source] {
		s.serveMu.Unlock()
		return nil, nil, storage.ErrBusy
	}
	slot := s.serveLeases[source]
	if slot != nil && (slot.retiring || slot.process == nil) {
		// The idle cut holds serveMu for its decision and keeps the slot
		// registered until the physical Wait, so test admission observes it
		// under the same lock and never names a process that is already
		// stopping.
		s.serveMu.Unlock()
		return nil, nil, storage.ErrBusy
	}
	life, cancel := context.WithCancel(ctx)
	scope := &sourceTestScope{done: make(chan struct{}), life: life, cancel: cancel}
	s.sourceTests[source] = scope
	var process *rcloneprogram.ServeProcess
	if slot != nil {
		process = slot.process
	}
	s.serveMu.Unlock()
	var once sync.Once
	finish := func() {
		once.Do(func() {
			scope.cancel()
			s.serveMu.Lock()
			if s.sourceTests[source] == scope {
				delete(s.sourceTests, source)
			}
			close(scope.done)
			s.serveMu.Unlock()
		})
	}
	var gate *sourceOpsGate
	var budget *remotemedia.OperationBudget
	if !cold {
		var err error
		gate, err = s.sourceOperationsNow(ctx, source, generation)
		if err != nil {
			finish()
			return nil, nil, err
		}
		budget = gate.budget
		if budget.Counts().Capacity != limits.Connections {
			finish()
			return nil, nil, storage.ErrRemoteChanged
		}
	}
	if process != nil {
		shared, err := process.SharedOperations(ctx)
		if err != nil {
			finish()
			return nil, nil, err
		}
		if budget == nil {
			budget = shared
		}
		if shared != budget || shared.Counts().Capacity != limits.Connections {
			finish()
			return nil, nil, storage.ErrRemoteChanged
		}
	}
	if budget == nil {
		budget = remotemedia.NewOperationBudget(life, limits.Connections)
	}
	// Mark the shared budget as a stopping test before taking its exclusive
	// hold. New callers then wait for this test's physical stop even if the
	// hold itself expires first; only the cleanup below closes that barrier.
	stopStopping := func() {}
	if !cold {
		var err error
		stopStopping, err = s.beginSourceTestStopping(source, budget)
		if err != nil {
			finish()
			return nil, nil, err
		}
	}
	hold, err := budget.AcquireTest(life)
	if err != nil {
		stopStopping()
		finish()
		if errors.Is(err, remotemedia.ErrBusy) {
			err = storage.ErrBusy
		}
		return nil, nil, err
	}
	// The registration ends with the bounded hold, as before. The stopping
	// barrier is cleared only by the caller's cleanup, which runs after the
	// temporary process has been physically waited for.
	context.AfterFunc(hold.Context(), finish)
	return hold, func() { hold.Close(); stopStopping(); finish() }, nil
}

// beginCandidateTest derives the current generation for callers that have no
// candidate receipt, then uses the same shared path.
func (s *Service) beginCandidateTest(ctx context.Context, source string, limits ReadLimits) (*remotemedia.ExclusiveTest, func(), error) {
	generation := ""
	if source != "" {
		snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
		if err != nil {
			return nil, nil, err
		}
		generation, err = observationGenerationTx(ctx, snapshot.Tx(), source)
		snapshot.Rollback()
		if err != nil {
			return nil, nil, err
		}
	}
	return s.beginCandidateTestAt(ctx, source, generation, limits)
}

type candidateServeLease struct {
	life  context.Context
	check func(context.Context) error
}

func (l *candidateServeLease) Context() context.Context { return l.life }
func (l *candidateServeLease) Check(ctx context.Context) error {
	if err := l.life.Err(); err != nil {
		return err
	}
	return l.check(ctx)
}
func (l *candidateServeLease) Release() {}

func (s *Service) candidateProfile(ctx context.Context, current *mounts.Mount) (SourceReadProfile, error) {
	if current == nil {
		p, err := SourceReadPreset("list")
		return p, err
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return SourceReadProfile{}, err
	}
	defer snapshot.Rollback()
	return SourceReadProfileTx(ctx, snapshot.Tx(), current.ID)
}

func (s *Service) testCandidate(ctx context.Context, input mounts.SourceCandidateInput, raw []byte, program rcloneprogram.Program, digest string, approvals []remotemedia.Approval, profile SourceReadProfile, authorize func(*sql.Tx) error, observations *[]webDAVSetupObservation) error {
	var err error
	check := func(ctx context.Context) error {
		snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
		if err != nil {
			return err
		}
		defer snapshot.Rollback()
		if err := authorize(snapshot.Tx()); err != nil {
			return err
		}
		if input.Current != nil {
			current := input.Current
			var revision, generation int64
			var root string
			if err := snapshot.Tx().QueryRowContext(ctx, `SELECT c.revision,b.generation,m.mount_path FROM managed_mounts m JOIN mount_controls c ON c.mount_id=m.id JOIN mount_backend_configs b ON b.mount_id=m.id WHERE m.id=? AND c.removal_pending=0`, current.ID).Scan(&revision, &generation, &root); err != nil {
				return err
			}
			actual, err := SourceReadProfileTx(ctx, snapshot.Tx(), current.ID)
			if err != nil {
				return err
			}
			if revision != current.ControlRevision || generation != current.BackendRevision || root != current.MountPath || actual != profile {
				return storage.ErrRemoteChanged
			}
		}
		snapshot.Rollback()
		return s.checkImportedApprovals(ctx, importedSourceConfiguration{Approvals: approvals})
	}
	if err = check(ctx); err != nil {
		return err
	}
	source, generation := "", "1"
	if input.Current != nil {
		source = input.Current.ID
		generation = strconv.FormatInt(input.Current.BackendRevision, 10)
	}
	policy := remotemedia.Policy{Resolver: s.resolver, SourceEndpoints: true}
	policy.Approvals = append(policy.Approvals, approvals...)
	observe, err := candidateWebDAVObserver(raw, input.Remote, profile, policy, check, observations)
	if err != nil {
		return err
	}
	return s.runCandidateServe(ctx, source, generation, profile, raw, input.Remote, program, digest, policy, check, observe)
}

type candidateMetadata struct {
	client *remotemedia.RcloneMetadata
	file   *remotemedia.DAVEntry
}

type candidateObserver func(context.Context, *rcloneprogram.ServeProcess, *remotemedia.ExclusiveTest, map[string]candidateMetadata) (func(context.Context) error, func(), error)

// providerTestFailure marks a failure at a direct provider-I/O boundary, such
// as the post-stop DAV diagnosis. It never marks a private RC call: a local
// rclone control response, local connect failure or RC status cannot supply a
// provider fact. Setup, program, custody and local database/file errors are
// never marked either.
type providerTestFailure struct{ err error }

func (e *providerTestFailure) Error() string { return e.err.Error() }
func (e *providerTestFailure) Unwrap() error { return e.err }

func providerTest(err error) error {
	if err == nil {
		return nil
	}
	return &providerTestFailure{err: err}
}

// ErrSavedSourceTestLocal marks a candidate test failure at the authenticated
// private RC boundary. It is a local control failure, never a provider fact, and
// never contact evidence.
var ErrSavedSourceTestLocal = errors.New("saved source test could not observe the provider")

// runCandidateServe holds source I/O exclusively through both phases. The
// observer's second phase runs only after physical Wait, under the SAME hold.
func (s *Service) runCandidateServe(ctx context.Context, source, generation string, profile SourceReadProfile, raw []byte, remote string, program rcloneprogram.Program, digest string, policy remotemedia.Policy, check func(context.Context) error, observe candidateObserver) error {
	hold, finish, err := s.beginCandidateTestAt(ctx, source, generation, profile.Limits)
	if err != nil {
		return err
	}
	defer finish()
	life := hold.Context()
	if source == "" {
		source = identity.Token()
	}
	egress, err := s.startRcloneEgress(life, raw, remote, policy, check)
	if err != nil {
		return err
	}
	defer egress.Close()
	checkSource := check
	check = func(ctx context.Context) error {
		if err := checkSource(ctx); err != nil {
			return err
		}
		if egress.CheckSMBPins != nil {
			return egress.CheckSMBPins(ctx)
		}
		return ctx.Err()
	}
	port, err := privateServePort()
	if err != nil {
		return err
	}
	metadata, err := privateServePort()
	if err != nil {
		return err
	}
	spec := rcloneprogram.ServeCommandSpec{SourceID: source, SourceGeneration: generation, ApprovedProgramDigest: digest, PrivateDirectory: s.private, Configuration: egress.TransientConfiguration(raw), Remote: remote, Port: port, MetadataPort: metadata, Username: identity.Token(), Password: identity.Token(), Connections: profile.Limits.Connections, RequestsPerSecond: profile.Limits.RequestsPerSecond, BytesPerSecond: profile.Limits.BytesPerSecond, SharedOperations: hold.Operations(), Enforce: func(cmd *exec.Cmd) error {
		if err := check(life); err != nil {
			return err
		}
		cmd.Env = append(cmd.Env, "RCLONE_HTTP_PROXY="+egress.HTTPProxy, "RCLONE_SFTP_HTTP_PROXY="+egress.SFTPProxy)
		return nil
	}}
	process, err := rcloneprogram.StartServe(life, program, spec, &candidateServeLease{life: life, check: check})
	if err != nil {
		if life.Err() != nil {
			return life.Err()
		}
		return mapError(err)
	}
	stopped := false
	stopProcess := func() error {
		if stopped {
			return nil
		}
		stopped = true
		process.Stop()
		err := process.Wait(context.Background())
		if err != nil && err != rcloneprogram.ErrServeStopped {
			return err
		}
		return nil
	}
	defer stopProcess()
	if err = check(life); err != nil {
		return err
	}
	metadataClient, err := process.Metadata(life)
	if err != nil {
		return err
	}
	leaves, err := configuredBackends(raw, remote)
	if err != nil {
		return err
	}
	webdav := map[string]bool{}
	for _, leaf := range leaves {
		webdav[leaf.Remote] = leaf.Kind == "webdav"
	}
	checkRoot := func(client *remotemedia.RcloneMetadata, name string) (candidateMetadata, error) {
		result := candidateMetadata{client: client}
		var err error
		if webdav[name] {
			result.file, err = client.CheckRootFile(life, check)
		} else {
			err = client.CheckRoot(life, check)
		}
		return result, err
	}
	rootMetadata, err := checkRoot(metadataClient, remote)
	if err != nil {
		// The root/leaf checks use the authenticated private RC. Local connect,
		// RC auth or RC status failures are not provider facts and stay local.
		if life.Err() != nil {
			return life.Err()
		}
		if processErr := process.CheckLocal(life); processErr != nil {
			return processErr
		}
		if checkErr := check(life); checkErr != nil {
			return checkErr
		}
		return fmt.Errorf("%w: %w", ErrSavedSourceTestLocal, mapError(err))
	}
	checked := map[string]bool{remote: true}
	leafMetadata := map[string]candidateMetadata{remote: rootMetadata}
	for _, leaf := range leaves {
		if checked[leaf.Remote] {
			continue
		}
		checked[leaf.Remote] = true
		client, err := remotemedia.NewRcloneMetadata(metadata, spec.Username, spec.Password, hold.Operations(), leaf.Remote)
		if err != nil {
			return err
		}
		defer client.Close()
		metadata, err := checkRoot(client, leaf.Remote)
		if err != nil {
			// Same private-RC boundary: keep the failure local, never a provider
			// offline/credential fact.
			if life.Err() != nil {
				return life.Err()
			}
			if processErr := process.CheckLocal(life); processErr != nil {
				return processErr
			}
			if checkErr := check(life); checkErr != nil {
				return checkErr
			}
			return fmt.Errorf("%w: %w", ErrSavedSourceTestLocal, mapError(err))
		}
		leafMetadata[leaf.Remote] = metadata
	}
	var afterStop func(context.Context) error
	if observe != nil {
		var cleanup func()
		afterStop, cleanup, err = observe(life, process, hold, leafMetadata)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return err
		}
	}
	if err = stopProcess(); err != nil {
		return err
	}
	if err = check(life); err != nil {
		return err
	}
	if afterStop != nil {
		if err = afterStop(life); err != nil {
			if life.Err() != nil {
				return life.Err()
			}
			if checkErr := check(life); checkErr != nil {
				return checkErr
			}
			return providerTest(err)
		}
	}
	return check(life)
}
