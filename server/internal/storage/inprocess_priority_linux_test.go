//go:build linux

package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func niceOf(t *testing.T, tid int) int {
	t.Helper()
	p, err := unix.Getpriority(unix.PRIO_PROCESS, tid)
	if err != nil {
		t.Fatalf("getpriority: %v", err)
	}
	return 20 - p // Linux returns 20-nice for PRIO_PROCESS
}

// observingHelper replaces the in-process handler for the test with one that
// reports the thread it ran on and that thread's nice value.
func observingHelper(t *testing.T) {
	t.Helper()
	useLowPriorityThreads(t)
	previous := inProcessHelper
	inProcessHelper = func(_ io.Reader, out io.Writer) error {
		tid := unix.Gettid()
		p, err := unix.Getpriority(unix.PRIO_PROCESS, tid)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%d %d\n", tid, 20-p)
		return err
	}
	t.Cleanup(func() { inProcessHelper = previous })
}

func observeInProcessThread(t *testing.T, background bool) (tid, nice int) {
	released := make(chan struct{})
	var line string
	if err := runInProcess(context.Background(), []byte("{}"), func(r io.Reader) error {
		raw, err := io.ReadAll(r)
		line = string(raw)
		return err
	}, func() { close(released) }, background); err != nil {
		t.Errorf("runInProcess: %v", err)
		return 0, 0
	}
	<-released
	_, _ = fmt.Sscanf(line, "%d %d", &tid, &nice)
	return tid, nice
}

// A background helper process ran at nice 19 with idle IO. Served in process,
// the same work runs on a dedicated low-priority thread; foreground work keeps
// the server's priority.
func TestInProcessBackgroundWorkKeepsItsLowPriorityThread(t *testing.T) {
	observingHelper(t)
	base := niceOf(t, unix.Gettid())
	if _, nice := observeInProcessThread(t, false); nice != base {
		t.Fatalf("foreground in-process work ran at nice %d, want the server's %d", nice, base)
	}
	for i := 0; i < 3; i++ {
		if _, nice := observeInProcessThread(t, true); nice != 19 {
			t.Fatalf("background in-process work ran at nice %d, want 19", nice)
		}
	}
}

// Linux sends a child's parent-death signal when the thread that started it
// exits; bubblewrap's --die-with-parent relies on it, so a sandbox (a decoder,
// a transcode, a probe worker) started on any runtime thread dies with that
// thread. An earlier M1 build retired a thread per background operation, and a
// scan's probe workers died with them (six sandboxes for six films instead of
// one). Background operations must run on a fixed set of threads that never
// exit.
func TestInProcessBackgroundWorkNeverRetiresAThread(t *testing.T) {
	observingHelper(t)
	threads := map[int]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tid, _ := observeInProcessThread(t, true)
			mu.Lock()
			threads[tid] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(threads) > lowPriorityThreads {
		t.Fatalf("background work ran on %d threads, want at most the %d dedicated ones", len(threads), lowPriorityThreads)
	}
	for tid := range threads {
		if _, err := os.Stat("/proc/self/task/" + strconv.Itoa(tid)); err != nil {
			t.Fatalf("background thread %d exited: %v", tid, err)
		}
	}
}

// A panic on a low-priority thread reaches the caller, and the thread stays:
// a locked thread that exited would deliver parent-death signals to every
// sandbox started on it.
func TestLowPriorityPanicReachesTheCallerAndKeepsTheThread(t *testing.T) {
	tids := map[int]bool{}
	var mu sync.Mutex
	record := func() {
		mu.Lock()
		tids[unix.Gettid()] = true
		mu.Unlock()
	}
	for i := 0; i < lowPriorityThreads; i++ {
		runLowPriority(record)
	}
	before := len(tids)

	func() {
		defer func() {
			if got := recover(); got != "boom" {
				t.Fatalf("recovered %v, want the job's panic", got)
			}
		}()
		runLowPriority(func() { panic("boom") })
		t.Fatal("runLowPriority returned after its job panicked")
	}()

	for i := 0; i < 4*lowPriorityThreads; i++ {
		runLowPriority(record)
	}
	for tid := range tids {
		if _, err := os.Stat("/proc/self/task/" + strconv.Itoa(tid)); err != nil {
			t.Errorf("low-priority thread %d is gone after a job panicked: %v", tid, err)
		}
	}
	if len(tids) > lowPriorityThreads || before == 0 {
		t.Errorf("saw %d low-priority threads, want at most %d", len(tids), lowPriorityThreads)
	}
}
