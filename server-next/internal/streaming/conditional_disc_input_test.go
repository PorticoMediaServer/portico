package streaming

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
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

func countingRefresh(calls *int, err error) func(context.Context) error {
	return func(context.Context) error {
		*calls++
		return err
	}
}
