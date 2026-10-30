package administration

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// Both storage pages read one maintained snapshot: concurrent readers share
// one walk, a failed walk never replaces the last complete snapshot, and the
// data page's projection comes from the same pass as the care page.
func TestStorageSnapshotIsSharedCoalescedAndKeptOnFailure(t *testing.T) {
	service, _, _ := newService(t)
	ctx := context.Background()
	now := time.Now()
	service.now = func() time.Time { return now }
	var walks atomic.Int32
	release := make(chan struct{})
	fail := atomic.Bool{}
	service.DataUsage = func(context.Context) (storagepaths.Usage, error) {
		n := walks.Add(1)
		if n == 1 {
			<-release
		}
		if fail.Load() {
			return storagepaths.Usage{}, errors.New("walk failed")
		}
		return storagepaths.Usage{DataDirectory: "walk", TranscodeBytes: int64(n)}, nil
	}
	var group sync.WaitGroup
	reports := make([]StorageReport, 8)
	errs := make([]error, 8)
	for i := range reports {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			reports[i], errs[i] = service.Storage(ctx, allow)
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	group.Wait()
	for i := range reports {
		if errs[i] != nil || reports[i].ObservedAt != reports[0].ObservedAt {
			t.Fatal("concurrent readers did not share one snapshot", i, errs[i])
		}
	}
	if walks.Load() != 1 {
		t.Fatal("concurrent readers walked more than once", walks.Load())
	}
	data, err := service.DataStorageUsage(ctx)
	if err != nil || data.TranscodeBytes != 1 || walks.Load() != 1 {
		t.Fatal("the data page did not read the same pass", data, err, walks.Load())
	}
	// A stale snapshot is served while a refresh runs; a failed refresh keeps it.
	fail.Store(true)
	now = now.Add(2 * storageUsageFresh)
	if data, err = service.DataStorageUsage(ctx); err != nil || data.TranscodeBytes != 1 {
		t.Fatal("stale snapshot was not served during the refresh", data, err)
	}
	waitStorageWalk(t, service)
	if data, err = service.DataStorageUsage(ctx); err != nil || data.TranscodeBytes != 1 {
		t.Fatal("a failed walk replaced the complete snapshot", data, err)
	}
	// The next complete walk is published.
	fail.Store(false)
	waitStorageWalk(t, service)
	if _, err = service.DataStorageUsage(ctx); err != nil {
		t.Fatal(err)
	}
	waitStorageWalk(t, service)
	if data, err = service.DataStorageUsage(ctx); err != nil || data.TranscodeBytes < 3 {
		t.Fatal("a complete walk was not published", data, err)
	}
}

func waitStorageWalk(t *testing.T, s *Service) {
	t.Helper()
	s.usage.mu.Lock()
	running := s.usage.running
	s.usage.mu.Unlock()
	if running != nil {
		<-running
	}
}
