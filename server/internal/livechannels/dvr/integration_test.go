package dvr

import (
	"context"
	"sync"
	"testing"
)

func TestStoragePolicyCacheDoesNotRegressOnReceiptReplayOrSlowRefresh(t *testing.T) {
	s := &Store{}
	newer := StoragePolicy{Revision: 3, FloorBytes: 2048, CapBytes: 8192}
	s.cacheStoragePolicy(newer)
	s.cacheStoragePolicy(StoragePolicy{Revision: 2, FloorBytes: 0, CapBytes: 0})
	s.cacheStoragePolicy(StoragePolicy{Revision: 3, FloorBytes: 0, CapBytes: 0})
	if got := s.StorageLimits(); got != newer {
		t.Fatalf("live limits regressed: %+v", got)
	}
	// Newer deliberate relaxation still works; this is a revision fence, not max().
	relaxed := StoragePolicy{Revision: 4}
	s.cacheStoragePolicy(relaxed)
	if got := s.StorageLimits(); got != relaxed {
		t.Fatal(got)
	}
}

func TestStoragePolicyCacheConcurrentPublishKeepsHighestCommittedRevision(t *testing.T) {
	s := &Store{}
	var wg sync.WaitGroup
	for revision := int64(1); revision <= 256; revision++ {
		wg.Add(1)
		go func(rev int64) {
			defer wg.Done()
			s.cacheStoragePolicy(StoragePolicy{Revision: rev, FloorBytes: rev, CapBytes: rev * 2})
		}(revision)
	}
	wg.Wait()
	if got := s.StorageLimits(); got != (StoragePolicy{Revision: 256, FloorBytes: 256, CapBytes: 512}) {
		t.Fatal(got)
	}
}

func TestStaleWorkerCompletionCannotEraseSuccessorOrRemainingPhysicalActivity(t *testing.T) {
	for _, first := range []string{"old", "new"} {
		t.Run(first, func(t *testing.T) {
			s := &Store{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.trackWorker("recording", "old", cancel)
			s.trackWorker("recording", "new", cancel)
			s.untrackWorker("recording", first)
			if len(s.active["recording"]) != 1 {
				t.Fatal("physical activity lost", s.active)
			}
			for _, stop := range s.active["recording"] {
				stop()
			}
			if ctx.Err() == nil {
				t.Fatal("remaining worker cannot be cancelled")
			}
			s.untrackWorker("recording", "old")
			s.untrackWorker("recording", "new")
			if len(s.active) != 0 {
				t.Fatal("completed claims retained")
			}
		})
	}
}
