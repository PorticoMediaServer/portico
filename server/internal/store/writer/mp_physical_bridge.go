package writer

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/storage/sourceops"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Private composition port for the existing storage.Supervisor. Keeping its
// concrete type outside writer avoids importing legacy dbwork's global SQLite
// function registrations. Trusted composition supplies the actual supervisor;
// this port is not exported or a caller-selected runner/authority constructor.
type mpPhysicalRunner interface {
	RunOwnedLinear(context.Context, string, *exec.Cmd, func(io.Reader) error, func(), func()) error
}

// One writer-owned registry survives supervisor replacement. Revocation denies
// old proof disclosure; it never frees a live root's capacity or exclusion.
type mpPhysicalRegistry struct {
	mu         sync.Mutex
	current    *mpPhysicalSupervisor
	executions map[mpPhysicalOccurrence]*mpPhysicalExecution
}
type mpPhysicalOccurrence struct {
	media      string
	generation int64
}
type mpPhysicalRoot struct {
	media, execution string
	generation       int64
}

func (r mpPhysicalRoot) occurrence() mpPhysicalOccurrence {
	return mpPhysicalOccurrence{r.media, r.generation}
}
func (r mpPhysicalRoot) valid() bool {
	return mpWorkerID(r.media) && mpWorkerID(r.execution) && r.generation > 0
}

type mpPhysicalSupervisor struct {
	self        *mpPhysicalSupervisor
	writer      *Writer
	life        context.Context
	server      string
	incarnation [16]byte
	registry    *mpPhysicalRegistry
	revoked     bool // registry.mu
}

// The launch identity contains no future retirement request or revision.
type mpPhysicalExecution struct {
	owner             *mpPhysicalSupervisor
	physical          mpPhysicalRoot
	readers, windows  *sourceops.Scope
	files             *os.Root
	cleanup           []string
	cleanupCursor     int
	filesClosed       bool
	retired, returned chan struct{}
	runErr            error // published by close(returned)
	joinMu            sync.Mutex
	joined            bool
	digest            [32]byte
}

func newMPPhysicalSupervisor(w *Writer, life context.Context, server string) (*mpPhysicalSupervisor, error) {
	if w == nil || life == nil || life.Err() != nil || !mpWorkerID(server) {
		return nil, ErrInvalid
	}
	s := &mpPhysicalSupervisor{writer: w, life: life, server: server}
	if _, err := rand.Read(s.incarnation[:]); err != nil {
		return nil, err
	}
	registry := w.mpPhysical.Load()
	if registry == nil {
		candidate := &mpPhysicalRegistry{executions: make(map[mpPhysicalOccurrence]*mpPhysicalExecution)}
		if w.mpPhysical.CompareAndSwap(nil, candidate) {
			registry = candidate
		} else {
			registry = w.mpPhysical.Load()
		}
	}
	s.registry = registry
	s.self = s
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if prior := registry.current; prior != nil {
		prior.revoked = true
	}
	registry.current = s
	return s, nil
}

// registry.mu is held by callers. Constructors and minting stay private.
func (s *mpPhysicalSupervisor) check(w *Writer) error {
	if s == nil || s.self != s || s.writer != w || w == nil || w.mpPhysical.Load() != s.registry || s.registry == nil || s.registry.current != s || s.life == nil || s.life.Err() != nil || s.revoked || s.incarnation == [16]byte{} {
		return ErrDisclosureDenied
	}
	return nil
}
func (s *mpPhysicalSupervisor) revoke() {
	s.registry.mu.Lock()
	s.revoked = true
	s.registry.mu.Unlock()
}

// Trusted composition transfers cmd and the bounded owned-root manifest here.
// mediaexec builds cmd; storage owns Start/Wait/permit retirement. There is no
// public constructor, exit callback, caller boolean, PID or expiry proof. This
// bridge is not wired to product helpers. No physical IO occurs in a W turn.
func (s *mpPhysicalSupervisor) start(root mpPhysicalRoot, supervisor mpPhysicalRunner, cmd *exec.Cmd, files *os.Root, cleanup []string) (*mpPhysicalExecution, error) {
	if s == nil || s.registry == nil {
		return nil, ErrInvalid
	}
	registry := s.registry
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := s.check(s.writer); err != nil {
		return nil, err
	}
	if !root.valid() || supervisor == nil || reflect.ValueOf(supervisor).Kind() != reflect.Ptr || reflect.ValueOf(supervisor).IsNil() || cmd == nil || cmd.Process != nil || len(cleanup) > 128 || (len(cleanup) > 0 && files == nil) {
		return nil, ErrInvalid
	}
	for _, p := range cleanup {
		if !validMPPhysicalPath(p) {
			return nil, ErrInvalid
		}
	}
	if registry.executions[root.occurrence()] != nil {
		return nil, ErrConflict
	}
	if len(registry.executions) >= 128 {
		return nil, &Refusal{Code: RefusalCapacity}
	}
	e := &mpPhysicalExecution{owner: s, physical: root, readers: sourceops.New(), windows: sourceops.New(), files: files, cleanup: append([]string(nil), cleanup...), retired: make(chan struct{}), returned: make(chan struct{})}
	registry.executions[root.occurrence()] = e
	retired, returned := e.retired, e.returned
	supervise.Go("writer.mp.physical.run", func() {
		e.runErr = supervisor.RunOwnedLinear(s.life, "mp-physical:"+root.execution, cmd, func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err }, nil, func() { close(retired) })
		close(returned)
	})
	return e, nil
}

// Actual return, supervisor retirement, reader exit and window exit are four
// independent barriers. Caller cancellation returns no proof and refunds no
// ownership. A later join can finish after the actual owner exits.
func (e *mpPhysicalExecution) join(ctx context.Context) (out *mpPhysicalObservation, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if e == nil || e.owner == nil || ctx == nil {
		return nil, ErrInvalid
	}
	e.joinMu.Lock()
	defer e.joinMu.Unlock()
	e.readers.Close()
	e.windows.Close()
	for _, done := range []<-chan struct{}{e.returned, e.retired} {
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err = e.readers.Wait(ctx); err != nil {
		return nil, err
	}
	if err = e.windows.Wait(ctx); err != nil {
		return nil, err
	}
	if !e.joined {
		if e.files != nil {
			for e.cleanupCursor < len(e.cleanup) {
				err = e.files.Remove(e.cleanup[e.cleanupCursor])
				// Once all writers/readers have joined, absence under the exclusively
				// owned root is already clean (including a helper that never wrote it).
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
				err = nil
				e.cleanupCursor++
			}
			if !e.filesClosed {
				if err = e.files.Close(); err != nil {
					return nil, err
				}
				e.filesClosed = true
			}
		}
		e.digest = mpPhysicalCommitment(e)
		e.joined = true
	}
	s := e.owner
	registry := s.registry
	registry.mu.Lock()
	defer registry.mu.Unlock()
	// All physical barriers and owned cleanup have finished. A terminal
	// infrastructure/no-start error can never mint a proof; retaining its slot
	// would make a transient launch failure permanently consume capacity.
	// Revoked issuers have the same terminal disclosure boundary after exit.
	var exit *exec.ExitError
	if e.runErr != nil && !errors.As(e.runErr, &exit) {
		if registry.executions[e.physical.occurrence()] == e {
			delete(registry.executions, e.physical.occurrence())
		}
		return nil, e.runErr
	}
	if err = s.check(s.writer); err != nil {
		if registry.executions[e.physical.occurrence()] == e {
			delete(registry.executions, e.physical.occurrence())
		}
		return nil, err
	}
	if registry.executions[e.physical.occurrence()] != e {
		return nil, ErrDisclosureDenied
	}
	out = &mpPhysicalObservation{owner: s, execution: e, root: e.physical, incarnation: s.incarnation, digest: e.digest}
	out.self = out
	return out, nil
}
