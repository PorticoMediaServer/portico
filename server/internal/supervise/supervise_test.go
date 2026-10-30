package supervise

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func quietLog(t *testing.T) {
	t.Helper()
	previous := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previous) })
}

func TestGoContainsAPanicAndCountsIt(t *testing.T) {
	quietLog(t)
	before := Panics()
	done := make(chan struct{})
	Go("test.panicking", func() {
		defer close(done)
		panic("deliberate")
	})
	<-done
	// The deferred close runs during unwinding, before the recover, which is what
	// lets a caller waiting on a handoff still be released.
	deadline := time.Now().Add(2 * time.Second)
	for Panics() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if Panics() != before+1 {
		t.Fatalf("the panic counter went from %d to %d", before, Panics())
	}
}

func TestGoLeavesASuccessfulGoroutineAlone(t *testing.T) {
	before := Panics()
	var ran atomic.Bool
	done := make(chan struct{})
	Go("test.ordinary", func() { ran.Store(true); close(done) })
	<-done
	if !ran.Load() {
		t.Fatal("the goroutine did not run")
	}
	if Panics() != before {
		t.Fatal("an ordinary goroutine was counted as a panic")
	}
}

func TestSuperviseRestartsAStatelessLoopAndStopsWithItsContext(t *testing.T) {
	quietLog(t)
	previous := BackoffInitial
	BackoffInitial = time.Millisecond
	t.Cleanup(func() { BackoffInitial = previous })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var starts atomic.Int64
	settled := make(chan struct{})
	var once sync.Once
	Supervise(ctx, "test.loop", func(life context.Context) {
		if starts.Add(1) <= 3 {
			panic("still broken")
		}
		once.Do(func() { close(settled) })
		<-life.Done()
	})
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatalf("the loop was restarted %d times and never settled", starts.Load())
	}
	cancel()
	// A loop that returns because its context ended is not restarted: that would
	// fight the shutdown it is responding to.
	time.Sleep(50 * time.Millisecond)
	at := starts.Load()
	time.Sleep(50 * time.Millisecond)
	if starts.Load() != at {
		t.Fatal("a loop kept restarting after its context ended")
	}
}

func TestSuperviseDoesNotRestartACleanReturn(t *testing.T) {
	var starts atomic.Int64
	done := make(chan struct{})
	Go("test.clean", func() {
		Loop(context.Background(), "test.clean", func(context.Context) { starts.Add(1) })
		close(done)
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a loop that returned cleanly was restarted")
	}
	if starts.Load() != 1 {
		t.Fatalf("the loop ran %d times", starts.Load())
	}
}

func TestHandlerMiddlewareAnswersAPanicBeforeAnyBytes(t *testing.T) {
	quietLog(t)
	before := Panics()
	handler := HandlerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("handler exploded")
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/items/abc", nil))
	if w.Code != 500 {
		t.Fatalf("a panicking handler answered %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "internal_error") {
		t.Fatalf("the answer was not typed: %s", w.Body)
	}
	if Panics() != before+1 {
		t.Fatal("the handler panic was not counted")
	}
}

func TestHandlerMiddlewareLetsErrAbortHandlerThrough(t *testing.T) {
	before := Panics()
	handler := HandlerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		value := recover()
		if value != http.ErrAbortHandler {
			t.Fatalf("ErrAbortHandler did not reach net/http: %v", value)
		}
		if Panics() != before {
			t.Fatal("a deliberate abort was counted as a failure")
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/media/secret-grant", nil))
}

// A stream that cannot flush buffers until the response ends, which for an event
// stream is never.
func TestHandlerMiddlewareKeepsFlushAndTheResponseController(t *testing.T) {
	var flushed, deadline bool
	handler := HandlerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
			flushed = true
		}
		deadline = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Second)) == nil
	}))
	server := httptest.NewServer(handler)
	response, err := http.Get(server.URL + "/v1/events")
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	// The flush sends the headers, so the client can see the response before the
	// handler has returned. Close waits for every handler to finish (a fast
	// Linux runner read the flags too early).
	server.Close()
	if !flushed {
		t.Fatal("a handler could no longer flush through the wrapper")
	}
	if !deadline {
		t.Fatal("a handler could no longer set a write deadline through the wrapper")
	}
}

// The route label is what a panic is attributed to. Media and stream URLs carry
// an unguessable grant, and the log is not a place to put one.
func TestRouteLabelDoesNotLeakACapability(t *testing.T) {
	if label := routeLabel("/v1/media/a-secret-grant-token/segment-000001.ts"); strings.Contains(label, "a-secret-grant-token") {
		t.Fatalf("the label leaked the grant: %s", label)
	}
	if label := routeLabel("/v1/items/abc"); label != "/v1/items/…" {
		t.Fatalf("an ordinary route was mangled: %s", label)
	}
	if label := routeLabel("/v1/readiness"); label != "/v1/readiness" {
		t.Fatalf("a two-segment route was mangled: %s", label)
	}
}

func TestChaosInjectsByPrefixAndFraction(t *testing.T) {
	quietLog(t)
	restore := SetChaosForTest("test.chaos:1.0")
	defer restore()
	before := Panics()
	cleaned := make(chan struct{})
	Go("test.chaos.holder", func() {
		// The caller's own cleanup, which is exactly what an injection raised
		// before the body would have skipped.
		defer close(cleaned)
		Chaos("test.chaos.something")
		t.Error("the body continued past the injection")
	})
	select {
	case <-cleaned:
	case <-time.After(2 * time.Second):
		t.Fatal("the injected panic skipped the caller's own cleanup")
	}
	deadline := time.Now().Add(2 * time.Second)
	for Panics() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if Panics() != before+1 {
		t.Fatal("chaos injection did not panic the goroutine")
	}
	// A name outside the rule is untouched.
	ran := make(chan struct{})
	Go("other.goroutine", func() { Chaos("other.goroutine"); close(ran) })
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("chaos injection hit a name it does not match")
	}
}
