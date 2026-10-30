package remotesources

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// RangeAdmission is supplied by the trusted source/profile owner from persisted
// scan facts. Check must re-resolve effective library/server/source authority for
// the captured operation and cost before opening bytes and before publication.
// Observe must bypass cached served metadata. Neither callback is optional.
type RangeAdmission struct {
	Scan      remotemedia.FileObservation
	Check     func(context.Context) error
	Observe   func(context.Context) (remotemedia.FileObservation, error)
	OnChanged func()
}

// RangeSession keeps one observation and source incarnation for its lifetime.
// It intentionally exposes no mediasource.Version: stat equality cannot grant
// immutable-version authority. Failure is terminal; owners must reacquire after
// rescan rather than reconnecting this session to a replacement.
type RangeSession struct {
	mu        sync.Mutex
	admission RangeAdmission
	readRange func(context.Context, string, string, int64, int64, int64) ([]byte, error)
	terminal  error
	notified  bool
	life      context.Context
	cancel    context.CancelFunc
}

func OpenRangeSession(ctx context.Context, endpoint *remotemedia.ServeEndpoint, admission RangeAdmission) (*RangeSession, error) {
	if endpoint == nil || admission.Check == nil || admission.Observe == nil || admission.OnChanged == nil || admission.Scan.ValidateForRange() != nil {
		return nil, storage.ErrRemoteConfig
	}
	// Retain an owned copy so a caller cannot mutate a hash slice after capture.
	admission.Scan, _ = admission.Scan.Canonical()
	life, cancel := context.WithCancel(context.Background())
	s := &RangeSession{admission: admission, life: life, cancel: cancel}
	s.readRange = func(ctx context.Context, relative, tag string, size, offset, length int64) ([]byte, error) {
		return endpoint.ReadRange(ctx, relative, tag, size, offset, length, s.checkObservation)
	}
	if err := s.Check(ctx); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

func (s *RangeSession) fail(err error) error {
	if s.terminal == nil {
		if errors.Is(err, remotemedia.ErrObservationChanged) {
			err = storage.ErrRemoteChanged
		}
		s.terminal = err
	}
	return s.terminal
}

func (s *RangeSession) check(ctx context.Context) error {
	if s.terminal != nil {
		return s.terminal
	}
	if s.life != nil && s.life.Err() != nil {
		return s.fail(storage.ErrRemoteOffline)
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	if err := s.checkObservation(ctx); err != nil {
		return s.fail(err)
	}
	return nil
}

// This callback does not mutate session state: a Transport may invoke it on a
// dialing goroutine, including an automatic stale-connection reconnect.
func (s *RangeSession) checkObservation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.life != nil && s.life.Err() != nil {
		return storage.ErrRemoteOffline
	}
	if err := s.admission.Check(ctx); err != nil {
		if errors.Is(err, storage.ErrRemoteChanged) {
			return remotemedia.ErrObservationChanged
		}
		return err
	}
	next, err := s.admission.Observe(ctx)
	if err == nil {
		err = s.admission.Scan.Check(next)
	}
	if err == nil {
		err = s.admission.Check(ctx)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if errors.Is(err, storage.ErrRemoteChanged) {
			return remotemedia.ErrObservationChanged
		}
		return err
	}
	return nil
}

func (s *RangeSession) Check(ctx context.Context) error {
	s.mu.Lock()
	err := s.check(ctx)
	notify := s.changeCallback()
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
	return err
}

// Called with mu held; mark before invoking outside the lock so a rescan
// callback may inspect or close this session without deadlocking.
func (s *RangeSession) changeCallback() func() {
	if !s.notified && errors.Is(s.terminal, storage.ErrRemoteChanged) {
		s.notified = true
		return s.admission.OnChanged
	}
	return nil
}

// ReadAt buffers one finite range before exposing bytes. Every request uses a
// loopback connection and is fenced on both sides and on every dial/reconnect;
// no short-body retry,
// sequential prefix materialization, or current-object fallback is permitted.
func (s *RangeSession) ReadAt(parent context.Context, p []byte, offset int64) (int, error) {
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
	if len(p) > 1<<20 || offset < 0 || offset > s.admission.Scan.Size {
		return 0, storage.ErrRemoteRange
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if s.life != nil {
		stop := context.AfterFunc(s.life, cancel)
		defer stop()
	}
	if err := s.check(ctx); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	length := min(int64(len(p)), s.admission.Scan.Size-offset)
	if length == 0 {
		return 0, io.EOF
	}
	o := s.admission.Scan
	relative := o.Object
	if o.Root != "" {
		relative = strings.TrimSuffix(o.Root, "/") + "/" + relative
	}
	data, err := s.readRange(ctx, relative, o.ServeETag, o.Size, offset, length)
	if err != nil {
		return 0, s.fail(err)
	}
	if err = s.check(ctx); err != nil {
		clear(data)
		return 0, err
	}
	n := copy(p, data)
	clear(data)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (s *RangeSession) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal == nil {
		s.terminal = storage.ErrRemoteOffline
	}
	return nil
}
