package ingestion

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// A first import lists folders for minutes, and every folder it adds changes
// the watch request, so the monitor replaces a healthy watcher on almost every
// tick. Each replacement is ready again within a second. That is not an outage:
// it must not end in a whole-library catch-up rescan once the listing settles.
func TestMonitorReplacingAHealthyWatcherIsNotAnOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	f := newLoopFixture(t, "movie", "One (2000)/One (2000).mkv")
	clock := &monitorTestClock{at: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	useMonitorTestClock(t, clock)
	f.s.SetStorage(&storage.Client{})
	var starts atomic.Int64
	previousWatch := monitorWatch
	monitorWatch = func(_ *Service, ctx context.Context, _ storage.WatchRequest, emit func(storage.InventoryChange)) error {
		starts.Add(1)
		emit(storage.InventoryChange{Ready: true})
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { monitorWatch = previousWatch })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.s.runMonitor(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("monitor did not stop")
		}
	}()
	var incarnation string
	if err := f.db.QueryRow(`SELECT incarnation FROM library_sources WHERE id=?`, f.source).Scan(&incarnation); err != nil {
		t.Fatal(err)
	}
	waitForMonitor(t, "the first watch", func() bool { return starts.Load() > 0 })
	// A folder every 300 ms (3 s of monitor time): 180 s of listing, so the
	// request changes on every tick.
	for i := 0; i < 60; i++ {
		folder := fmt.Sprintf("Film %02d", i)
		if _, err := dbwork.ExecWrite(context.Background(), f.db, dbwork.ClassBackgroundMedia, `INSERT INTO inventory_directory_cache(source_id,incarnation,relative_path,parent_path) VALUES(?,?,?,'.')`, f.source, incarnation, folder); err != nil {
			t.Fatal(err)
		}
		if _, err := dbwork.ExecWrite(context.Background(), f.db, dbwork.ClassBackgroundMedia, `UPDATE library_sources SET generation=generation WHERE id=?`, f.source); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		clock.advance(3 * time.Second)
	}
	for i := 0; i < 3; i++ {
		time.Sleep(1100 * time.Millisecond)
		clock.advance(5 * time.Second)
	}
	if starts.Load() < 5 {
		t.Fatalf("the watcher was replaced only %d times; the fixture did not change the watch request", starts.Load())
	}
	if n := f.count(`SELECT count(*) FROM inventory_path_notices WHERE source_id=?`, f.source) + f.count(`SELECT count(*) FROM jobs WHERE library_id=?`, f.library); n != 0 {
		t.Fatalf("replacing a healthy watcher %d times queued %d rescans", starts.Load(), n)
	}
}
