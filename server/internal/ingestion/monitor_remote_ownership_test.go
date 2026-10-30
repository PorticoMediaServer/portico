package ingestion

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// The monitor's real branch path is driven here with a fixed clock and a watch
// seam. Both only replace where time and change hints come from; ownership,
// fallback selection, notice purging and reporting are the production code.

type monitorTestClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *monitorTestClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *monitorTestClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// monitorTestRemote answers only the adapter-identity question the monitor
// asks. Every other RemoteSources method is unreachable on this path.
type monitorTestRemote struct {
	storage.RemoteSources
	root string
}

func (m monitorTestRemote) Handles(path string) bool { return path == m.root }

func useMonitorTestClock(t *testing.T, clock *monitorTestClock) {
	t.Helper()
	previous := monitorClock
	monitorClock = clock.now
	t.Cleanup(func() { monitorClock = previous })
}

func waitForMonitor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMonitorExcludesRemoteOwnedSourcesAndKeepsLocalStartupAndFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newLoopFixture(t, "movie", "One (2000)/One (2000).mkv")
	clock := &monitorTestClock{at: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	useMonitorTestClock(t, clock)
	remoteRoot := filepath.Join(string(filepath.Separator), "remote", "library")
	f.exec(`INSERT INTO remote_sources(id,kind,name,root) VALUES('bound-source','rclone','Bound','/bound')`)
	f.exec(`INSERT INTO remote_sources(id,kind,name,root) VALUES('','rclone','Empty identity','/empty-binding')`)
	f.exec(`INSERT INTO libraries(id,name,kind,root) VALUES('lib-empty-bound','Empty bound','movie',?)`, filepath.Join(f.root, "empty-bound"))
	f.exec(`UPDATE library_sources SET root_identity='empty-bound-identity',remote_source_id='' WHERE library_id='lib-empty-bound'`)
	// Bound but adapter-local: the binding alone must make it remote-owned.
	f.exec(`INSERT INTO libraries(id,name,kind,root) VALUES('lib-bound','Bound','movie',?)`, filepath.Join(f.root, "bound"))
	f.exec(`UPDATE library_sources SET root_identity='bound-identity',remote_source_id='bound-source' WHERE library_id='lib-bound'`)
	// Adapter-remote but unbound: the adapter alone must make it remote-owned.
	f.exec(`INSERT INTO libraries(id,name,kind,root) VALUES('lib-remote','Remote','movie',?)`, remoteRoot)
	f.exec(`UPDATE library_sources SET root_identity='remote-identity' WHERE library_id='lib-remote'`)
	f.s.SetStorage(&storage.Client{Remote: monitorTestRemote{root: remoteRoot}})

	sources, request, err := f.s.monitorConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 4 {
		t.Fatalf("monitor sources=%d, want all four kept for map parity", len(sources))
	}
	if len(request.Roots) != 1 || request.Roots[0].SourceID != f.source {
		t.Fatalf("watch roots=%+v, want only the local source %q", request.Roots, f.source)
	}

	started := make(chan storage.WatchRequest, 1)
	emitters := make(chan func(storage.InventoryChange), 1)
	previousWatch := monitorWatch
	monitorWatch = func(_ *Service, ctx context.Context, request storage.WatchRequest, emit func(storage.InventoryChange)) error {
		select {
		case started <- request:
		default:
		}
		select {
		case emitters <- emit:
		default:
		}
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

	select {
	case request = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not start its watch")
	}
	if len(request.Roots) != 1 || request.Roots[0].SourceID != f.source {
		t.Fatalf("running watch roots=%+v, want only the local source", request.Roots)
	}
	var emit func(storage.InventoryChange)
	select {
	case emit = <-emitters:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not expose its change emitter")
	}
	emit(storage.InventoryChange{Ready: true})

	waitForMonitor(t, "local startup queue", func() bool {
		return f.count(`SELECT count(*) FROM jobs WHERE library_id=?`, f.library) > 0
	})
	if n := f.count(`SELECT count(*) FROM jobs WHERE library_id IN('lib-bound','lib-remote','lib-empty-bound')`); n != 0 {
		t.Fatalf("remote-owned sources queued %d startup jobs", n)
	}

	// The healthy watch interval is 15 minutes. Jump past it, then past the
	// debounce, so a local fallback report is the only possible new work.
	clock.advance(16 * time.Minute)
	deadline := time.Now().Add(8 * time.Second)
	for f.count(`SELECT count(*) FROM inventory_path_notices WHERE source_id=?`, f.source) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("local 15-minute fallback report did not run")
		}
		clock.advance(3 * time.Second)
		time.Sleep(1200 * time.Millisecond)
	}
	if n := f.count(`SELECT count(*) FROM jobs WHERE library_id IN('lib-bound','lib-remote','lib-empty-bound')`); n != 0 {
		t.Fatalf("remote-owned sources queued %d fallback jobs", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_path_notices WHERE source_id IN(SELECT id FROM library_sources WHERE library_id IN('lib-bound','lib-remote','lib-empty-bound'))`); n != 0 {
		t.Fatalf("remote-owned sources emitted %d notices", n)
	}
}

func TestMonitorPurgesPendingLocalNoticeWhenRootBecomesRemote(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newLoopFixture(t, "movie", "One (2000)/One (2000).mkv")
	clock := &monitorTestClock{at: time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC)}
	useMonitorTestClock(t, clock)
	f.exec(`INSERT INTO remote_sources(id,kind,name,root) VALUES('bound-source','rclone','Bound','/bound')`)
	f.s.SetStorage(&storage.Client{Remote: monitorTestRemote{root: filepath.Join(string(filepath.Separator), "remote", "library")}})

	started := make(chan struct{}, 1)
	watchStopped := make(chan struct{})
	emitters := make(chan func(storage.InventoryChange), 1)
	previousWatch := monitorWatch
	monitorWatch = func(_ *Service, ctx context.Context, _ storage.WatchRequest, emit func(storage.InventoryChange)) error {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case emitters <- emit:
		default:
		}
		<-ctx.Done()
		close(watchStopped)
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
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not start its watch")
	}
	var emit func(storage.InventoryChange)
	select {
	case emit = <-emitters:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not expose its change emitter")
	}

	// Fill the event buffer and one more change: the last send can only finish
	// after the monitor has consumed at least one hint, so a notice is pending.
	for n := 0; n < 513; n++ {
		emit(storage.InventoryChange{SourceID: f.source, RelativePath: "One (2000)"})
	}
	if _, err := dbwork.ExecWrite(context.Background(), f.db, dbwork.ClassInteractive, `UPDATE library_sources SET remote_source_id='bound-source' WHERE id=?`, f.source); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watchStopped:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not consume remote ownership reconfiguration")
	}
	// Past both the reconfiguration wake and the 5-minute safety tick, and far
	// past the 10-second pending-notice delay. A stale notice would emit here.
	clock.advance(6 * time.Minute)
	time.Sleep(2500 * time.Millisecond)
	if n := f.count(`SELECT count(*) FROM inventory_path_notices WHERE source_id=?`, f.source); n != 0 {
		t.Fatalf("reconfigured source emitted %d stale monitor notices", n)
	}
}
