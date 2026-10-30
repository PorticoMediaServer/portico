//go:build !windows

package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFIFOPlaybackSourceRejectedWithoutBlocking(t *testing.T) {
	c, _, _ := playbackFixture(t)
	p := filepath.Join(t.TempDir(), "blocked-fifo")
	if e := syscall.Mkfifo(p, 0600); e != nil {
		t.Fatal(e)
	}
	c.Timeout = 100 * time.Millisecond
	start := time.Now()
	if _, e := c.OpenPlayback(context.Background(), p, 0, 0); !errors.Is(e, ErrPlaybackSource) {
		t.Fatalf("FIFO source rejection %v", e)
	}
	if time.Since(start) > time.Second {
		t.Fatal("FIFO source rejection blocked")
	}
	waitReaderExit(t, c)
}
func TestBlockedSourceOpenDeadlineDoesNotBlockServer(t *testing.T) {
	c, p, info := playbackFixture(t)
	script := filepath.Join(t.TempDir(), "blocked-open")
	// Consume the request so startup reaches the initial-frame read, then
	// deliberately withhold that frame until the supervisor cancels the helper.
	if e := os.WriteFile(script, []byte("#!/bin/sh\nIFS= read -r request || exit 1\nsleep 30\n"), 0700); e != nil {
		t.Fatal(e)
	}
	c.Binary = script
	c.Timeout = 100 * time.Millisecond
	start := time.Now()
	if _, e := c.OpenPlayback(context.Background(), p, info.Size(), info.ModTime().UnixNano()); !errors.Is(e, ErrPlaybackTimeout) {
		t.Fatalf("blocked initial frame %v", e)
	}
	if time.Since(start) > time.Second {
		t.Fatal("open failed to cancel")
	}
	waitReaderExit(t, c)
}
func TestBlockedReadDeadlineAndProcessReaping(t *testing.T) {
	c, p, info := playbackFixture(t)
	script := filepath.Join(t.TempDir(), "blocked-reader")
	if e := os.WriteFile(script, []byte("#!/bin/sh\nprintf '\\000\\000\\000\\000\\000'\nsleep 30\n"), 0700); e != nil {
		t.Fatal(e)
	}
	c.Binary = script
	c.Timeout = 2 * time.Second
	f, e := c.OpenPlayback(context.Background(), p, info.Size(), info.ModTime().UnixNano())
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	f.timeout = 100 * time.Millisecond
	start := time.Now()
	if n, e := f.Read(make([]byte, 10)); n != 0 || !errors.Is(e, ErrPlaybackTimeout) {
		t.Fatalf("read %d %v", n, e)
	}
	if time.Since(start) > time.Second {
		t.Fatal("read did not cancel")
	}
	waitReaderExit(t, c)
}
func TestCanceledSupervisorKeepsAdmissionUntilConsumerAndWaitReturn(t *testing.T) {
	s := &Supervisor{Limit: 1}
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, "playback:blocked", exec.Command("sh", "-c", "sleep 30"), func(io.Reader) error { close(entered); <-release; return nil })
	}()
	<-entered
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if s.Active() != 1 {
		t.Fatal("premature admission release")
	}
	if e := s.Run(context.Background(), "playback:other", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil }); !errors.Is(e, ErrBusy) {
		t.Fatal("quarantine lost", e)
	}
	close(release)
	waitReaderExit(t, &Client{Supervisor: s})
}
