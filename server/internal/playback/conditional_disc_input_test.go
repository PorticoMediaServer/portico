package playback

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

// These regressions pin the conditional disc pipe's completion contract with
// fakes only: no helper process, source object or real disc bytes.

type conditionalTestReader struct {
	mu     sync.Mutex
	chunks [][]byte
	index  int
	check  func() error
	checks int
	closed int
}

func newConditionalTestReader(chunks ...string) *conditionalTestReader {
	reader := &conditionalTestReader{}
	for _, chunk := range chunks {
		reader.chunks = append(reader.chunks, []byte(chunk))
	}
	return reader
}

func (r *conditionalTestReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.index])
	r.chunks[r.index] = r.chunks[r.index][n:]
	if len(r.chunks[r.index]) == 0 {
		r.index++
	}
	return n, nil
}

func (r *conditionalTestReader) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks++
	if r.check != nil {
		return r.check()
	}
	return nil
}

func (r *conditionalTestReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed++
	return nil
}

func (r *conditionalTestReader) setCheck(check func() error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.check = check
}

func (r *conditionalTestReader) checkCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checks
}

// blockedWriter reports when the copy reaches Write, then holds it until the
// test releases it with an exact write result.
type blockedWriter struct {
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
	releaseOnce sync.Once
	written     int
	err         error
}

// unblock releases a held Write exactly once, so cleanup and the direct success
// path can both call it.
func (w *blockedWriter) unblock() {
	w.releaseOnce.Do(func() { close(w.release) })
}

func newBlockedWriter(written int, err error) *blockedWriter {
	return &blockedWriter{entered: make(chan struct{}), release: make(chan struct{}), written: written, err: err}
}

func (w *blockedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	n := w.written
	if n < 0 || n > len(p) {
		n = 0
	}
	return n, w.err
}

type conditionalCopyResult struct {
	complete bool
	err      error
}

func startBlockedCopy(t *testing.T, reader *conditionalTestReader, writer *blockedWriter) chan conditionalCopyResult {
	t.Helper()
	result := make(chan conditionalCopyResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		complete, err := copyConditionalDisc(context.Background(), writer, reader)
		result <- conditionalCopyResult{complete: complete, err: err}
	}()
	// A failed assertion before the normal release must still unblock and join
	// the copy goroutine.
	t.Cleanup(func() {
		writer.unblock()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("copy goroutine did not retire during cleanup")
		}
	})
	select {
	case <-writer.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("copy never reached the blocked writer")
	}
	return result
}

func waitCopyResult(t *testing.T, result chan conditionalCopyResult) conditionalCopyResult {
	t.Helper()
	timeout := time.After(3 * time.Second)
	select {
	case got := <-result:
		return got
	case <-timeout:
		t.Fatal("copy did not finish after the writer was released")
		return conditionalCopyResult{}
	}
}

// A write that fails or is short is not checked EOF; the terminal failure known
// while the write was blocked must be returned, not the write error.
func TestCopyConditionalDiscPreservesTerminalErrorDuringBlockedWrite(t *testing.T) {
	terminal := errors.New("helper failed while the write was blocked")
	writeErr := errors.New("the converter refused the stream")
	for _, c := range []struct {
		name    string
		written int
		err     error
	}{
		{"write error", 0, writeErr},
		{"short write", 2, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			reader := newConditionalTestReader("payload")
			defer reader.Close()
			writer := newBlockedWriter(c.written, c.err)
			result := startBlockedCopy(t, reader, writer)
			reader.setCheck(func() error { return terminal })
			writer.unblock()
			got := waitCopyResult(t, result)
			if got.complete {
				t.Fatal("an abandoned write was reported as checked EOF")
			}
			if !errors.Is(got.err, terminal) {
				t.Fatalf("terminal failure was replaced: %v", got.err)
			}
			if reader.checkCount() < 2 {
				t.Fatal("the source was not checked before and after the write")
			}
			// The held-write result itself must refuse later, with no refresh.
			refreshes := 0
			if err := checkConditionalDiscAttempt(context.Background(), conditionalAttempt(reader, got.complete, got.err, true), countingRefresh(&refreshes, nil)); !errors.Is(err, terminal) {
				t.Fatalf("the held-write result was not preserved: %v", err)
			}
			if refreshes != 0 {
				t.Fatal("a refresh was consulted for the held-write result")
			}
		})
	}
}

// A benign writer stop is genuinely exercised through copyConditionalDisc: the
// source never fails, the result is incomplete + nil, and only a passing refresh
// lets the same original retry.
func TestCopyConditionalDiscBenignWriterStopNeedsRefresh(t *testing.T) {
	refusal := errors.New("source changed on refresh")
	for _, c := range []struct {
		name    string
		written int
		err     error
	}{
		{"benign write error", 0, errors.New("the converter refused the stream")},
		{"benign short write", 2, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			reader := newConditionalTestReader("payload")
			defer reader.Close()
			writer := newBlockedWriter(c.written, c.err)
			result := startBlockedCopy(t, reader, writer)
			writer.unblock()
			got := waitCopyResult(t, result)
			if got.complete || got.err != nil {
				t.Fatalf("benign stop reported complete=%v err=%v", got.complete, got.err)
			}
			attempt := conditionalAttempt(reader, got.complete, got.err, true)
			refreshes := 0
			if err := checkConditionalDiscAttempt(context.Background(), attempt, countingRefresh(&refreshes, refusal)); !errors.Is(err, refusal) {
				t.Fatalf("refusing refresh did not retain its refusal: %v", err)
			}
			if refreshes != 1 {
				t.Fatalf("refresh called %d times, want 1", refreshes)
			}
			refreshes = 0
			if err := checkConditionalDiscAttempt(context.Background(), attempt, countingRefresh(&refreshes, nil)); err != nil {
				t.Fatalf("passing refresh refused the same original: %v", err)
			}
			if refreshes != 1 {
				t.Fatalf("refresh called %d times, want 1", refreshes)
			}
		})
	}
}

// A checked EOF is the only complete result.
func TestCopyConditionalDiscCheckedEOFIsComplete(t *testing.T) {
	reader := newConditionalTestReader("payload")
	defer reader.Close()
	complete, err := copyConditionalDisc(context.Background(), io.Discard, reader)
	if err != nil || !complete {
		t.Fatalf("checked EOF reported complete=%v err=%v", complete, err)
	}
}

func conditionalAttempt(reader *conditionalTestReader, complete bool, err error, closed bool) *conditionalDiscAttempt {
	attempt := &conditionalDiscAttempt{source: reader, readDone: make(chan struct{}), complete: complete, err: err}
	if closed {
		close(attempt.readDone)
	}
	return attempt
}

func countingRefresh(calls *int, err error) func(context.Context) error {
	return func(context.Context) error {
		*calls++
		return err
	}
}

// A completed attempt that ended in a known error refuses on its own result; the
// refresh is only for a benign early stop.
func TestCheckConditionalDiscAttemptRefusesCompletedErrorWithoutRefresh(t *testing.T) {
	terminal := errors.New("known helper failure")
	reader := newConditionalTestReader()
	defer reader.Close()
	attempt := conditionalAttempt(reader, false, terminal, true)
	refreshes := 0
	if err := checkConditionalDiscAttempt(context.Background(), attempt, countingRefresh(&refreshes, nil)); !errors.Is(err, terminal) {
		t.Fatalf("completed error was not preserved: %v", err)
	}
	if refreshes != 0 {
		t.Fatal("a refresh was consulted for an attempt that already failed")
	}
}

// An early benign consumer stop is incomplete: it needs a real refresh, and a
// nil refresh is refused rather than promoted.
func TestCheckConditionalDiscAttemptBenignStopNeedsRefresh(t *testing.T) {
	refusal := errors.New("source changed on refresh")
	for _, c := range []struct {
		name       string
		nilRefresh bool
		refreshErr error
		want       error
	}{
		{name: "nil refresh refuses", nilRefresh: true, want: disc.ErrRemoteUnqualified},
		{name: "refresh refusal retained", refreshErr: refusal, want: refusal},
		{name: "refresh pass allows the same original"},
	} {
		t.Run(c.name, func(t *testing.T) {
			reader := newConditionalTestReader()
			defer reader.Close()
			attempt := conditionalAttempt(reader, false, nil, true)
			refreshes := 0
			var refresh func(context.Context) error
			if !c.nilRefresh {
				refresh = countingRefresh(&refreshes, c.refreshErr)
			}
			err := checkConditionalDiscAttempt(context.Background(), attempt, refresh)
			if c.want != nil {
				if !errors.Is(err, c.want) {
					t.Fatalf("got %v want %v", err, c.want)
				}
			} else if err != nil {
				t.Fatalf("passing refresh still refused: %v", err)
			}
			want := 0
			if !c.nilRefresh {
				want = 1
			}
			if refreshes != want {
				t.Fatalf("refresh called %d times, want %d", refreshes, want)
			}
		})
	}
}

// Checked EOF completes without any refresh.
func TestCheckConditionalDiscAttemptCheckedEOFNeedsNoRefresh(t *testing.T) {
	reader := newConditionalTestReader()
	defer reader.Close()
	refreshes := 0
	if err := checkConditionalDiscAttempt(context.Background(), conditionalAttempt(reader, true, nil, true), countingRefresh(&refreshes, errors.New("must not run"))); err != nil {
		t.Fatalf("checked EOF refused: %v", err)
	}
	if refreshes != 0 {
		t.Fatal("checked EOF consulted a refresh")
	}
}

// A known non-cancel helper failure is preserved even when the attempt finishes
// benignly at the same moment.
func TestCheckConditionalDiscAttemptPreservesKnownErrorDuringBenignCompletion(t *testing.T) {
	terminal := errors.New("final observation failed")
	reader := newConditionalTestReader()
	defer reader.Close()
	attempt := conditionalAttempt(reader, false, nil, false)
	reader.setCheck(func() error {
		// The pipe finishes benignly while Check is running.
		attempt.complete, attempt.err = false, nil
		close(attempt.readDone)
		return terminal
	})
	refreshes := 0
	if err := checkConditionalDiscAttempt(context.Background(), attempt, countingRefresh(&refreshes, nil)); !errors.Is(err, terminal) {
		t.Fatalf("known helper failure was replaced by benign completion: %v", err)
	}
	if refreshes != 0 {
		t.Fatal("a refresh was consulted despite the known failure")
	}
	reader.setCheck(nil)
}

// A cancellation from Check is not itself source failure: the actual attempt
// result decides, and a benign stop goes through the refresh.
func TestCheckConditionalDiscAttemptCanceledCheckConsultsBenignCompletion(t *testing.T) {
	reader := newConditionalTestReader()
	defer reader.Close()
	attempt := conditionalAttempt(reader, false, nil, false)
	reader.setCheck(func() error {
		attempt.complete, attempt.err = false, nil
		close(attempt.readDone)
		return context.Canceled
	})
	refreshes := 0
	if err := checkConditionalDiscAttempt(context.Background(), attempt, countingRefresh(&refreshes, nil)); err != nil {
		t.Fatalf("benign completion after a canceled check refused: %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("refresh called %d times, want 1", refreshes)
	}
	reader.setCheck(nil)
}

// While the attempt is still running and Check only reports cancellation, the
// caller sees that cancellation and no refresh runs.
func TestCheckConditionalDiscAttemptRunningCancellationIsReturned(t *testing.T) {
	reader := newConditionalTestReader()
	defer reader.Close()
	reader.setCheck(func() error { return context.Canceled })
	refreshes := 0
	err := checkConditionalDiscAttempt(context.Background(), conditionalAttempt(reader, false, nil, false), countingRefresh(&refreshes, nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("running cancellation was not returned: %v", err)
	}
	if refreshes != 0 {
		t.Fatal("refresh ran while the attempt was still running")
	}
}
