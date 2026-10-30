package ingestion

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// A Run must own the monitor, its worker loop and every quantum it starts. An
// iteration panic then unwinds only after those children stop, so the outer
// supervise.Loop can restart the scanner without reusing a stale generation.
func TestRunOwnsChildrenSoPanicReachesSupervisor(t *testing.T) {
	f := newLoopFixture(t, "movie")
	job := f.queue()
	service := f.s

	previousMonitor, previousQuantum, previousHook := monitorRun, quantumRun, iterationHook
	t.Cleanup(func() { monitorRun, quantumRun, iterationHook = previousMonitor, previousQuantum, previousHook })

	monitorStarts := make(chan struct{}, 8)
	monitorStops := make(chan struct{}, 8)
	quantumStarts := make(chan string, 8)
	quantumStops := make(chan struct{}, 8)
	monitorRun = func(s *Service, ctx context.Context) {
		monitorStarts <- struct{}{}
		<-ctx.Done()
		monitorStops <- struct{}{}
	}
	quantumRun = func(s *Service, ctx context.Context, id, library string) bool {
		quantumStarts <- id
		<-ctx.Done()
		quantumStops <- struct{}{}
		return false
	}
	var iterations atomic.Int32
	iterationHook = func(context.Context) {
		// The first two iterations start the monitor and the queued job's
		// quantum; the third panics while both are still running.
		if iterations.Add(1) == 3 {
			panic("injected iteration panic")
		}
	}

	parent, cancelParent := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		supervise.Loop(parent, "test.ingestion", service.Run)
	}()
	defer func() {
		cancelParent()
		waitRunSignal(t, done, "supervisor cleanup")
	}()

	waitRunSignal(t, monitorStarts, "first monitor start")
	waitJob := func(what string) {
		t.Helper()
		select {
		case id := <-quantumStarts:
			if id != job.ID {
				t.Fatalf("%s selected a different job", what)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}
	waitJob("first quantum start")
	// The panic unwinds Run: its own generation stops, so the first quantum and
	// monitor join without the parent being cancelled.
	waitRunSignal(t, quantumStops, "first quantum join")
	waitRunSignal(t, monitorStops, "first monitor join")
	if parent.Err() != nil {
		t.Fatal("Run cleanup canceled the supplied parent")
	}
	// The supervisor restarts the scanner; the durable queued job is picked up
	// again by the new generation.
	waitJob("restarted quantum start")

	cancelParent()
	waitRunSignal(t, done, "supervisor exit")
	waitRunSignal(t, quantumStops, "restarted quantum join")
	waitRunSignal(t, monitorStops, "restarted monitor join")
}

func waitRunSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
