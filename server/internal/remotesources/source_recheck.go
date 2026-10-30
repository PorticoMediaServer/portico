package remotesources

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// StopAndRecheck terminates one retained reader after a consumer/decode error.
// It closes its children before fresh metadata so a cap1 progressive stream
// cannot retain the provider slot. Only the already captured physical epoch is
// inspected: no replacement process, media reopen or new200 body is permitted.
// The original invocation context is used; cancellation grants no detached I/O.
func (l *SourceReadLease) StopAndRecheck(ctx context.Context) error {
	if l == nil {
		return storage.ErrRemoteConfig
	}
	l.openMu.Lock()
	defer l.openMu.Unlock()
	l.mu.Lock()
	terminal, epoch := l.terminal, l.processGeneration
	l.stopping = true
	l.mu.Unlock()
	defer l.Close()
	l.closeChildren()
	if terminal != nil {
		return terminal
	}
	if err := l.Check(ctx); err != nil {
		return err
	}
	if epoch == "" {
		return l.fail(storage.ErrRemoteOffline)
	}
	process, err := l.service.ensureSourceServe(ctx, l.binding, epoch)
	if err != nil {
		return l.fail(err)
	}
	_, fence, err := l.service.freshFileAdmission(ctx, process, FreshReadAdmission{Observation: l.binding.Observation, ProcessGeneration: epoch, Check: l.Check, OnChanged: l.changed})
	if err == nil {
		var fresh remotemedia.FileObservation
		fresh, err = fence.Observe(ctx)
		if err == nil && !fence.Scan.Equal(fresh) {
			err = storage.ErrRemoteChanged
		}
		if err == nil {
			err = fence.Check(ctx)
		}
	}
	if errors.Is(err, storage.ErrRemoteChanged) || errors.Is(err, remotemedia.ErrObservationChanged) {
		l.changed()
		return storage.ErrRemoteChanged
	}
	if err != nil {
		return l.fail(err)
	}
	return nil
}

func (l *SourceReadLease) canOpen() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminal != nil {
		return l.terminal
	}
	if l.stopping || l.life.Err() != nil {
		return storage.ErrRemoteOffline
	}
	return nil
}
