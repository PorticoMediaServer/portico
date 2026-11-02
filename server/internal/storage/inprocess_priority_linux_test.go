//go:build linux

package storage

import (
	"context"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A background helper process ran at nice 19 with idle IO. Served in process,
// the same work runs on its own lowered thread, which the runtime discards
// afterwards so no other goroutine inherits the priority.
func TestInProcessBackgroundWorkKeepsItsLowPriorityThread(t *testing.T) {
	type seen struct {
		tid, nice int
	}
	observe := func(background bool) seen {
		got := make(chan seen, 1)
		previous := inProcessHelper
		inProcessHelper = func(_ io.Reader, out io.Writer) error {
			tid := unix.Gettid()
			// Getpriority returns 20-nice for PRIO_PROCESS on Linux.
			p, err := unix.Getpriority(unix.PRIO_PROCESS, tid)
			if err != nil {
				t.Errorf("getpriority: %v", err)
			}
			got <- seen{tid: tid, nice: 20 - p}
			_, err = out.Write([]byte("{}\n"))
			return err
		}
		t.Cleanup(func() { inProcessHelper = previous })
		released := make(chan struct{})
		err := runInProcess(context.Background(), []byte("{}"), func(r io.Reader) error {
			_, err := io.Copy(io.Discard, r)
			return err
		}, func() { close(released) }, background)
		if err != nil {
			t.Fatalf("runInProcess: %v", err)
		}
		<-released
		inProcessHelper = previous
		return <-got
	}
	base, err := unix.Getpriority(unix.PRIO_PROCESS, unix.Gettid())
	if err != nil {
		t.Fatal(err)
	}
	if foreground := observe(false); foreground.nice != 20-base {
		t.Fatalf("foreground in-process work ran at nice %d, want the server's %d", foreground.nice, 20-base)
	}
	background := observe(true)
	if background.nice != 19 {
		t.Fatalf("background in-process work ran at nice %d, want 19", background.nice)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat("/proc/self/task/" + strconv.Itoa(background.tid)); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lowered thread %d is still alive; the runtime reuses it for other goroutines", background.tid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
