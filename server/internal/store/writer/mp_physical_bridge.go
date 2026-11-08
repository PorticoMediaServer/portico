package writer

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/storage/sourceops"
)

// Trusted composition only: no exported constructor, callbacks that report
// exit, caller booleans, PID checks, timeout or lease-based proof. This bridge
// owns the supervisor invocation and joins physical scopes outside W's turn.
// mediaexec still builds the command; storage still owns Start/Wait/retirement.
// This first instance is deliberately not wired to product helpers.
type mpPhysicalSupervisor struct {
	self        *mpPhysicalSupervisor
	writer      *Writer
	life        context.Context
	server      string
	incarnation [16]byte
	mu          sync.Mutex
	revoked     bool
	executions  map[mpPhysicalKey]*mpPhysicalExecution
}

type mpPhysicalKey struct {
	media, execution, request string
	generation, revision      int64
}

type mpPhysicalExecution struct {
	owner             *mpPhysicalSupervisor
	key               mpPhysicalKey
	readers, windows  *sourceops.Scope
	root              *os.Root
	cleanup           []string
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
	s := &mpPhysicalSupervisor{writer: w, life: life, server: server, executions: make(map[mpPhysicalKey]*mpPhysicalExecution)}
	if _, err := rand.Read(s.incarnation[:]); err != nil {
		return nil, err
	}
	s.self = s
	if prior := w.mpPhysical.Swap(s); prior != nil {
		prior.revoke()
	}
	return s, nil
}

func (s *mpPhysicalSupervisor) check(w *Writer) error {
	if s == nil || s.self != s || s.writer != w || w == nil || w.mpPhysical.Load() != s || s.life == nil || s.life.Err() != nil || s.revoked || s.incarnation == [16]byte{} {
		return ErrDisclosureDenied
	}
	return nil
}
func (s *mpPhysicalSupervisor) revoke() { s.mu.Lock(); s.revoked = true; s.mu.Unlock() }

// Root and its bounded removal manifest transfer exclusively to this bridge.
// An empty manifest proves no file cleanup, only the joined execution scopes.
// A failed removal or Close never yields a cleanup commitment. The caller must
// not Start/Wait cmd or release the transferred root independently.
func (s *mpPhysicalSupervisor) start(key mpPhysicalKey, supervisor *storage.Supervisor, cmd *exec.Cmd, root *os.Root, cleanup []string) (*mpPhysicalExecution, error) {
	if s == nil {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(s.writer); err != nil {
		return nil, err
	}
	if len(s.executions) >= 128 {
		return nil, &Refusal{Code: RefusalCapacity}
	}
	if !key.valid() || supervisor == nil || cmd == nil || cmd.Process != nil || len(cleanup) > 128 || (len(cleanup) > 0 && root == nil) {
		return nil, ErrInvalid
	}
	for _, p := range cleanup {
		if !validMPPhysicalPath(p) {
			return nil, ErrInvalid
		}
	}
	// One root occurrence, including across different retirement requests.
	for k := range s.executions {
		if k.media == key.media && k.generation == key.generation {
			return nil, ErrConflict
		}
	}
	e := &mpPhysicalExecution{owner: s, key: key, readers: sourceops.New(), windows: sourceops.New(), root: root, cleanup: append([]string(nil), cleanup...), retired: make(chan struct{}), returned: make(chan struct{})}
	s.executions[key] = e
	retired := e.retired
	go func() {
		e.runErr = supervisor.RunOwnedLinear(s.life, "mp-physical:"+key.execution, cmd, func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err }, nil, func() { close(retired) })
		close(e.returned)
	}()
	return e, nil
}

// Closing scopes bars new readers/windows before waiting for existing debt.
// Cancellation returns no proof and retains all physical ownership. Retrying
// join after cancellation may observe a later actual physical return.
func (e *mpPhysicalExecution) join(ctx context.Context) (out *mpPhysicalObservation, err error) {
	defer func() {
		out, err = finishCommand(ctx, ctx, out, err)
	}()
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
	// Non-zero process exit is still an actual Wait. Infrastructure/no-start
	// failures are not accepted by this first narrow bridge.
	var exit *exec.ExitError
	if e.runErr != nil && !errors.As(e.runErr, &exit) {
		return nil, e.runErr
	}
	if err = e.readers.Wait(ctx); err != nil {
		return nil, err
	}
	if err = e.windows.Wait(ctx); err != nil {
		return nil, err
	}
	if !e.joined {
		if e.root != nil {
			for _, p := range e.cleanup {
				if err = e.root.Remove(p); err != nil {
					return nil, err
				}
			}
			if err = e.root.Close(); err != nil {
				return nil, err
			}
		}
		e.digest = mpPhysicalCommitment(e)
		e.joined = true
	}
	s := e.owner
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.check(s.writer); err != nil {
		return nil, err
	}
	if s.executions[e.key] != e {
		return nil, ErrDisclosureDenied
	}
	out = &mpPhysicalObservation{owner: s, execution: e, key: e.key, incarnation: s.incarnation, digest: e.digest}
	out.self = out
	return out, nil
}
