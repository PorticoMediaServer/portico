package remotesources

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// SequentialAdmission binds one genuine from-start body to its real producer's
// checks: retained Reading/playback content access, or a separately bounded
// inventory metadata/configuration read. A scan tier or pool label cannot grant
// interactive content access. Missing callbacks fail closed, and metadata
// equality is not immutable identity.
type SequentialAdmission struct {
	Scan      remotemedia.FileObservation
	Check     func(context.Context) error
	Observe   func(context.Context) (remotemedia.FileObservation, error)
	OnChanged func()
}

// SequentialSession exposes only a from-start stream. It cannot implement a
// random-access port. A failed or closed stream is terminal, never replayed.
type SequentialSession struct {
	mu        sync.Mutex
	admission SequentialAdmission
	body      io.ReadCloser
	life      context.Context
	cancel    context.CancelFunc
	remaining int64
	terminal  error
	notified  bool
}

func OpenSequentialSession(ctx context.Context, endpoint *remotemedia.ServeEndpoint, admission SequentialAdmission) (*SequentialSession, error) {
	if endpoint == nil || admission.Check == nil || admission.Observe == nil || admission.OnChanged == nil || admission.Scan.Validate() != nil {
		return nil, storage.ErrRemoteConfig
	}
	admission.Scan, _ = admission.Scan.Canonical()
	life, cancel := context.WithCancel(ctx)
	s := &SequentialSession{admission: admission, life: life, cancel: cancel, remaining: admission.Scan.Size}
	good := false
	defer func() {
		if !good {
			s.Close()
		}
	}()
	if err := s.checkObservation(ctx); err != nil {
		s.fail(err)
		if notify := s.changeCallback(); notify != nil {
			notify()
		}
		return nil, err
	}
	o := admission.Scan
	relative := o.Object
	if o.Root != "" {
		relative = strings.TrimSuffix(o.Root, "/") + "/" + relative
	}
	body, err := endpoint.OpenFromStart(life, relative, o.ServeETag, o.Size, s.checkObservation)
	if err != nil {
		s.mu.Lock()
		err = s.fail(err)
		notify := s.changeCallback()
		s.mu.Unlock()
		if notify != nil {
			notify()
		}
		return nil, err
	}
	s.body = body
	if err = s.checkCurrent(ctx); err != nil {
		return nil, err
	}
	good = true
	return s, nil
}

func (s *SequentialSession) checkObservation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.life.Err(); err != nil {
		return err
	}
	if err := s.admission.Check(ctx); err != nil {
		return err
	}
	next, err := s.admission.Observe(ctx)
	if err == nil && (next.Validate() != nil || !s.admission.Scan.Equal(next)) {
		err = storage.ErrRemoteChanged
	}
	if err == nil {
		err = s.admission.Check(ctx)
	}
	if err == nil {
		err = s.life.Err()
	}
	if err == nil {
		err = ctx.Err()
	}
	return err
}
func (s *SequentialSession) checkCurrent(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.life.Err(); err != nil {
		return err
	}
	return s.admission.Check(ctx)
}

func (s *SequentialSession) fail(err error) error {
	if s.terminal == nil {
		if errors.Is(err, remotemedia.ErrObservationChanged) {
			err = storage.ErrRemoteChanged
		}
		s.terminal = err
		s.cancel()
		if s.body != nil {
			s.body.Close()
		}
	}
	return s.terminal
}
func (s *SequentialSession) changeCallback() func() {
	if !s.notified && errors.Is(s.terminal, storage.ErrRemoteChanged) {
		s.notified = true
		return s.admission.OnChanged
	}
	return nil
}

// Check checks current session/source authority without acquiring another
// provider connection while this progressive body is active.
func (s *SequentialSession) Check(ctx context.Context) error {
	s.mu.Lock()
	err := s.terminal
	if err == nil {
		if err = s.checkCurrent(ctx); err != nil {
			err = s.fail(err)
		}
	}
	notify := s.changeCallback()
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
	return err
}

// Read buffers at most one1MiB extent, rechecking current authority on both
// sides. Fresh backend identity is checked before opening and after EOF/error;
// a live body may occupy the source's only provider connection. Failed extents
// never reach caller memory. The HTTP
// body stays progressive and is never materialized into a file or whole buffer.
func (s *SequentialSession) Read(ctx context.Context, p []byte) (int, error) {
	s.mu.Lock()
	defer func() {
		notify := s.changeCallback()
		s.mu.Unlock()
		if notify != nil {
			notify()
		}
	}()
	if s.terminal != nil {
		return 0, s.terminal
	}
	if len(p) > 1<<20 {
		return 0, storage.ErrRemoteRange
	}
	stop := context.AfterFunc(ctx, s.cancel)
	defer stop()
	if err := s.checkCurrent(ctx); err != nil {
		return 0, s.fail(err)
	}
	if len(p) == 0 {
		return 0, nil
	}
	if s.remaining == 0 {
		return 0, io.EOF
	}
	data := make([]byte, min(int64(len(p)), s.remaining))
	defer clear(data)
	n, err := io.ReadFull(s.body, data)
	if err != nil {
		s.body.Close()
		if canceled := ctx.Err(); canceled != nil {
			return 0, s.fail(canceled)
		}
		if canceled := s.life.Err(); canceled != nil {
			return 0, s.fail(canceled)
		}
		err = errors.Join(storage.ErrRemoteRange, s.checkObservation(ctx))
		return 0, s.fail(err)
	}
	if int64(n) == s.remaining {
		var extra [1]byte
		if count, end := s.body.Read(extra[:]); count != 0 || end != io.EOF {
			s.body.Close()
			return 0, s.fail(errors.Join(storage.ErrRemoteRange, s.checkObservation(ctx)))
		}
		s.body.Close()
		if err = s.checkObservation(ctx); err != nil {
			return 0, s.fail(err)
		}
	}
	if err = s.checkCurrent(ctx); err != nil {
		return 0, s.fail(err)
	}
	s.remaining -= int64(n)
	copy(p, data)
	return n, nil
}

// StopAndRecheck ends a progressive stream after a consumer/decode error, then
// attempts fresh identity with the provider connection released. It never
// restarts the body or grants new I/O after cancellation/revocation.
func (s *SequentialSession) StopAndRecheck(ctx context.Context) error {
	s.mu.Lock()
	if s.body != nil {
		s.body.Close()
	}
	err := s.checkObservation(ctx)
	if err != nil {
		s.fail(err)
	} else {
		s.fail(storage.ErrRemoteOffline)
	}
	notify := s.changeCallback()
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
	return err
}

func (s *SequentialSession) Close() error {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.body != nil {
		s.body.Close()
	}
	if s.terminal == nil {
		s.terminal = storage.ErrRemoteOffline
	}
	return nil
}
