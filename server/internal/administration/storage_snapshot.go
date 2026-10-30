package administration

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// storageUsageFresh is how long a complete snapshot is served without asking
// for a new walk. Older snapshots are still served while one runs.
const storageUsageFresh = time.Minute

// storageUsageSnapshot is the one maintained storage measurement both storage
// pages read. One walker runs at a time, off every request; callers that need
// a measurement before the first one completes wait for that same walker. Only
// a complete walk is published: a failed or cancelled walk never replaces the
// last complete snapshot.
type storageUsageSnapshot struct {
	mu       sync.Mutex
	complete *storageUsageResult
	taken    time.Time
	running  chan struct{}
	lastErr  error
}

type storageUsageResult struct {
	report  StorageReport
	data    storagepaths.Usage
	hasData bool
}

// storageSnapshot returns the last complete snapshot, starting a walk when it
// is missing or stale. With no snapshot yet, it waits for the walk, as long as
// ctx allows; the walk itself is not tied to the caller.
func (s *Service) storageSnapshot(ctx context.Context) (storageUsageResult, error) {
	u := &s.usage
	u.mu.Lock()
	if u.complete == nil || s.now().Sub(u.taken) > storageUsageFresh {
		s.startStorageWalkLocked()
	}
	if u.complete != nil {
		out := *u.complete
		u.mu.Unlock()
		return out, nil
	}
	running := u.running
	u.mu.Unlock()
	select {
	case <-ctx.Done():
		return storageUsageResult{}, ctx.Err()
	case <-running:
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.complete == nil {
		return storageUsageResult{}, errors.Join(ErrUnavailable, u.lastErr)
	}
	return *u.complete, nil
}

// startStorageWalkLocked starts the single walker unless one is running.
func (s *Service) startStorageWalkLocked() {
	u := &s.usage
	if u.running != nil {
		return
	}
	done := make(chan struct{})
	u.running = done
	supervise.Go("administration.storage-walk", func() {
		defer close(done)
		// A walk that panics still ends: the next read starts a fresh one.
		var result storageUsageResult
		err := errors.New("storage walk stopped")
		defer func() {
			u.mu.Lock()
			defer u.mu.Unlock()
			u.running, u.lastErr = nil, err
			if err == nil {
				u.complete, u.taken = &result, s.now()
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		result, err = s.walkStorage(ctx)
	})
}

func (s *Service) walkStorage(ctx context.Context) (storageUsageResult, error) {
	var out storageUsageResult
	var err error
	if s.StateDirectory() != "" {
		if out.report, err = s.measureStorage(ctx); err != nil {
			return out, err
		}
	}
	if s.DataUsage != nil {
		if out.data, err = s.DataUsage(ctx); err != nil {
			return out, err
		}
		out.hasData = true
	}
	return out, nil
}

// DataStorageUsage is the data storage page's projection of the same snapshot
// the storage care page reads.
func (s *Service) DataStorageUsage(ctx context.Context) (storagepaths.Usage, error) {
	if s.DataUsage == nil {
		return storagepaths.Usage{}, ErrUnavailable
	}
	snapshot, err := s.storageSnapshot(ctx)
	if err != nil {
		return storagepaths.Usage{}, err
	}
	if !snapshot.hasData {
		return storagepaths.Usage{}, ErrUnavailable
	}
	return snapshot.data, nil
}
