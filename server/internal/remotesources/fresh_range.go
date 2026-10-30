package remotesources

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// FreshReadAdmission binds the caller's real captured operation/actor/job
// authority to a persisted observation and one actual source-process epoch.
// Check must perform authority-only checks, without range/metadata I/O, so it
// can safely be re-entered from a transport dial. There is no default checker.
type FreshReadAdmission struct {
	Observation       ObservationRef
	ProcessGeneration string
	Check             func(context.Context) error
	OnChanged         func()
}

// OpenFreshRange composes normalized scan facts with the live upstream-rclone
// metadata and range paths. It never copies old provider facts into a fresh
// observation or returns an immutable Version. Undetectable origin changes
// retain the explicitly accepted section15 limitation.
func (s *Service) OpenFreshRange(ctx context.Context, process *rcloneprogram.ServeProcess, admission FreshReadAdmission) (*RangeSession, error) {
	endpoint, fence, err := s.freshFileAdmission(ctx, process, admission)
	if err != nil {
		return nil, err
	}
	return OpenRangeSession(ctx, endpoint, fence)
}

// OpenFreshSequential is a distinct interactive from-start transport. The
// caller supplies its actual retained Reading/playback authority, not automatic
// scan tier/window permission. It never adapts a200 response into ReadAt.
func (s *Service) OpenFreshSequential(ctx context.Context, process *rcloneprogram.ServeProcess, admission FreshReadAdmission) (*SequentialSession, error) {
	endpoint, fence, err := s.freshFileAdmission(ctx, process, admission)
	if err != nil {
		return nil, err
	}
	return OpenSequentialSession(ctx, endpoint, SequentialAdmission{Scan: fence.Scan, Check: fence.Check, Observe: fence.Observe, OnChanged: fence.OnChanged})
}

func (s *Service) freshFileAdmission(ctx context.Context, process *rcloneprogram.ServeProcess, admission FreshReadAdmission) (endpoint *remotemedia.ServeEndpoint, out RangeAdmission, err error) {
	if s == nil || s.db == nil || process == nil || admission.Check == nil || admission.OnChanged == nil || admission.ProcessGeneration == "" {
		return nil, RangeAdmission{}, storage.ErrRemoteConfig
	}
	notify := sync.OnceFunc(admission.OnChanged)
	defer func() {
		if errors.Is(err, storage.ErrRemoteChanged) || errors.Is(err, remotemedia.ErrObservationChanged) {
			notify()
		}
	}()
	if process.Generation() != admission.ProcessGeneration {
		return nil, RangeAdmission{}, storage.ErrRemoteChanged
	}
	ref := admission.Observation
	id, generation := process.Source()
	if ref.SourceID != id || ref.Generation != generation || !validObservationObject(ref.Object) || ref.Revision < 1 {
		return nil, RangeAdmission{}, storage.ErrRemoteChanged
	}
	var root string
	if err := s.db.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, id).Scan(&root); err != nil {
		return nil, RangeAdmission{}, storage.ErrRemoteOffline
	}
	if !filepath.IsAbs(root) {
		return nil, RangeAdmission{}, storage.ErrRemoteConfig
	}
	scan, err := s.ScanObservation(ctx, filepath.Join(root, filepath.FromSlash(ref.Object)))
	if err != nil {
		return nil, RangeAdmission{}, err
	}
	if scan.Ref != ref {
		return nil, RangeAdmission{}, storage.ErrRemoteChanged
	}
	check := func(ctx context.Context) error {
		if err := admission.Check(ctx); err != nil {
			return err
		}
		if err := process.Check(ctx); err != nil {
			return err
		}
		var currentRoot string
		if err := s.db.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, id).Scan(&currentRoot); err != nil || currentRoot != root {
			return storage.ErrRemoteChanged
		}
		return s.CheckScanObservation(ctx, ref)
	}
	if err = check(ctx); err != nil {
		return nil, RangeAdmission{}, err
	}
	endpoint, err = process.Endpoint(ctx)
	if err != nil {
		return nil, RangeAdmission{}, err
	}
	observe := func(ctx context.Context) (remotemedia.FileObservation, error) {
		return observeProcessFile(ctx, process, ref.Object, check)
	}
	return endpoint, RangeAdmission{Scan: scan.Observation.Facts, Check: check, Observe: observe, OnChanged: notify}, nil
}
