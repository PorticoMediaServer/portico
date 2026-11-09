// Package progressio bounds inactivity while allowing a slow producer to finish.
package progressio

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

type Watch struct {
	mu           sync.Mutex
	timer        *time.Timer
	idle         time.Duration
	lastProgress time.Time
	closed       bool
	cancel       context.CancelFunc
	outer        context.CancelFunc
	expire       context.CancelFunc
}

// New keeps the parent's cancellation. overall is a safety bound for hostile
// trickle input; zero uses no extra wall-clock cap. Ping records actual bytes or
// completed work, never the passage of time.
func New(parent context.Context, idle, overall time.Duration) (context.Context, *Watch) {
	outer := func() {}
	if overall > 0 {
		parent, outer = context.WithTimeout(parent, overall)
	}
	ctx, cancel := context.WithCancelCause(parent)
	w := &Watch{idle: idle, cancel: func() { cancel(context.Canceled) }, expire: func() { cancel(context.DeadlineExceeded) }, outer: outer}
	w.Ping()
	return ctx, w
}

// NewDormant starts measuring only when Ping is called after resource admission.
// Waiting for a worker slot or an owner pause is not stalled producer input.
func NewDormant(parent context.Context, idle time.Duration) (context.Context, *Watch) {
	ctx, cancel := context.WithCancelCause(parent)
	return ctx, &Watch{idle: idle, cancel: func() { cancel(context.Canceled) }, expire: func() { cancel(context.DeadlineExceeded) }, outer: func() {}}
}
func (w *Watch) Ping() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.lastProgress = time.Now()
		if w.timer == nil {
			w.timer = time.AfterFunc(w.idle, w.expireIdle)
		} else {
			w.timer.Reset(w.idle)
		}
	}
}

// expireIdle is also called by an already dispatched timer after Ping resets it.
func (w *Watch) expireIdle() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		// Reset cannot stop a callback that has already started and
		// is waiting for this mutex. Recheck actual byte/work progress.
		if remaining := time.Until(w.lastProgress.Add(w.idle)); remaining > 0 {
			w.timer.Reset(remaining)
			return
		}
		w.closed = true
		w.expire()
	}
}

func (w *Watch) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.cancel()
	w.outer()
}
func (w *Watch) Reader(r io.Reader) io.Reader { return &reader{r, w} }

type reader struct {
	io.Reader
	watch *Watch
}

func (r *reader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.watch.Ping()
	}
	return n, err
}

// Transport wraps the caller's existing policy transport. Redirect, proxy, DNS,
// TLS and address authorization decisions remain with that transport/client.
type Transport struct {
	Base          http.RoundTripper
	Idle, Overall time.Duration
}

func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	idle := t.Idle
	if idle <= 0 {
		idle = 30 * time.Second
	}
	ctx, watch := New(req.Context(), idle, t.Overall)
	copy := req.Clone(ctx)
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(copy)
	if err != nil {
		watch.Close()
		return response, err
	}
	watch.Ping()
	response.Body = &body{ReadCloser: response.Body, watch: watch}
	return response, nil
}
func (t Transport) CloseIdleConnections() {
	if c, ok := t.Base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

type body struct {
	io.ReadCloser
	watch *Watch
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.watch.Ping()
	}
	if err == io.EOF {
		b.watch.Close()
	}
	return n, err
}
func (b *body) Close() error { b.watch.Close(); return b.ReadCloser.Close() }

func (w *Watch) Writer(out io.Writer) io.Writer { return &progressWriter{out, w} }

type progressWriter struct {
	io.Writer
	watch *Watch
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n > 0 {
		w.watch.Ping()
	}
	return n, err
}
