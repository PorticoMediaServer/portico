package assets

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These regressions pin PlaybackInput's ownership and borrowed-lifetime
// contract. They create their own files only; no source, pins or helper runs.

func playbackTempFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "playback-input-*")
	if err != nil {
		t.Fatal(err)
	}
	// A failed write or assertion below must not leak the descriptor.
	t.Cleanup(func() { _ = file.Close() })
	if _, err = file.WriteString("source bytes"); err != nil {
		t.Fatal(err)
	}
	return file
}

func fileClosed(t *testing.T, file *os.File) bool {
	t.Helper()
	if _, err := file.Stat(); err != nil {
		return true
	}
	return false
}

// The retained closer owns the descriptor and the pins together: it must see the
// original file still open, then release exactly that one source once.
func TestPlaybackInputReleaseSourceSeesFileOpenAndRunsOnce(t *testing.T) {
	file := playbackTempFile(t)
	extra := playbackTempFile(t)
	var calls, sawOpen int32
	input := &PlaybackInput{File: file, Extra: []*os.File{extra}, ReleaseSource: func() error {
		atomic.AddInt32(&calls, 1)
		if !fileClosed(t, file) {
			atomic.StoreInt32(&sawOpen, 1)
		}
		return file.Close()
	}}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("released the original source %d times", got)
	}
	if atomic.LoadInt32(&sawOpen) != 1 {
		t.Fatal("ReleaseSource ran after the original file had already been closed")
	}
	if !fileClosed(t, file) {
		t.Fatal("the original file stayed open after its owner released it")
	}
	if !fileClosed(t, extra) {
		t.Fatal("the input-owned Extra file stayed open")
	}
	if err := input.Close(); err != nil {
		t.Fatalf("repeated close returned %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("repeated close released the source again: %d", got)
	}
}

// Extra stays owned by the input even when there is no File and no retained
// closer, and it closes exactly once.
func TestPlaybackInputExtraClosesOnceWithoutFile(t *testing.T) {
	first, second := playbackTempFile(t), playbackTempFile(t)
	input := &PlaybackInput{Extra: []*os.File{first, second}}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	for _, file := range []*os.File{first, second} {
		if !fileClosed(t, file) {
			t.Fatal("an input-owned Extra file stayed open")
		}
	}
	if err := input.Close(); err != nil {
		t.Fatalf("repeated close returned %v", err)
	}
}

// Concurrent and repeated Close calls release one original source, and a handle
// installed afterwards (a successor or another owner's reference) is never closed
// by this input.
func TestPlaybackInputConcurrentCloseReleasesOriginalOnce(t *testing.T) {
	file := playbackTempFile(t)
	var calls int32
	input := &PlaybackInput{File: file, ReleaseSource: func() error {
		atomic.AddInt32(&calls, 1)
		return file.Close()
	}}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = input.Close() }(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("concurrent closes released the source %d times", got)
	}
	for _, err := range errs {
		if err != nil {
			t.Fatalf("a concurrent close returned %v", err)
		}
	}
	successor := playbackTempFile(t)
	input.File = successor
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if fileClosed(t, successor) {
		t.Fatal("a repeated close released a successor handle")
	}
}

// Cancelling either the caller or the retained source cancels the borrowed run,
// and a borrowed run never releases the source itself.
func TestPlaybackInputBorrowContextJoinsLifetimeAndCaller(t *testing.T) {
	lifetime, retire := context.WithCancel(context.Background())
	defer retire()
	var released int32
	input := &PlaybackInput{Lifetime: lifetime, ReleaseSource: func() error { atomic.AddInt32(&released, 1); return nil }}
	run, release := input.BorrowContext(context.Background())
	defer release()
	select {
	case <-run.Done():
		t.Fatal("borrow was cancelled before either source retired")
	default:
	}
	retire()
	select {
	case <-run.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("retiring the original lifetime did not cancel its borrow")
	}
	if atomic.LoadInt32(&released) != 0 {
		t.Fatal("borrowing a source released it")
	}
	// A borrow taken after the source retired is cancelled immediately.
	late, releaseLate := input.BorrowContext(context.Background())
	defer releaseLate()
	select {
	case <-late.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a retired lifetime did not cancel a later borrow")
	}
	// The caller alone cancels its own borrow without retiring the source.
	live, retireLive := context.WithCancel(context.Background())
	defer retireLive()
	liveInput := &PlaybackInput{Lifetime: live}
	caller, cancelCaller := context.WithCancel(context.Background())
	borrowed, releaseBorrowed := liveInput.BorrowContext(caller)
	defer releaseBorrowed()
	cancelCaller()
	select {
	case <-borrowed.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the caller did not cancel its borrow")
	}
	if live.Err() != nil {
		t.Fatal("cancelling the caller retired the original source")
	}
}

// Retiring one borrowed operation leaves the original source and every other
// borrow of it alive; release is safe to call twice.
func TestPlaybackInputBorrowReleaseLeavesSourceAndOtherBorrowLive(t *testing.T) {
	lifetime, retire := context.WithCancel(context.Background())
	defer retire()
	input := &PlaybackInput{Lifetime: lifetime}
	runA, releaseA := input.BorrowContext(context.Background())
	runB, releaseB := input.BorrowContext(context.Background())
	defer releaseB()
	releaseA()
	releaseA()
	if runA.Err() == nil {
		t.Fatal("a released borrow was still runnable")
	}
	if lifetime.Err() != nil {
		t.Fatal("retiring one borrow cancelled the original source")
	}
	select {
	case <-runB.Done():
		t.Fatal("retiring one borrow cancelled another borrow")
	default:
	}
}

// The ordinary File-only input keeps closing its own descriptor once.
func TestPlaybackInputCloseWithoutReleaseSourceClosesFile(t *testing.T) {
	file := playbackTempFile(t)
	input := &PlaybackInput{File: file}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if !fileClosed(t, file) {
		t.Fatal("a File-only input did not close its descriptor")
	}
	if err := input.Close(); err != nil {
		t.Fatalf("repeated close returned %v", err)
	}
}
