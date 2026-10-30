package supervise

import (
	"errors"
	"net/http"
	"runtime/debug"
	"strings"
)

// net/http already recovers a panic in a handler, so this middleware is not
// what keeps the process alive. What it adds is attribution and a number: the
// standard library logs an unattributed line to the server's ErrorLog and moves
// on, so a handler panicking on one route every few minutes is indistinguishable
// from silence. Here it is counted into the same figure the goroutine helpers
// feed, and it is logged with the method and route it came from.
//
// It also answers, which net/http's own recovery cannot: the connection is
// simply closed there, and a client sees a truncated response with no status. A
// panic before the first byte becomes an honest typed 500.

// HandlerMiddleware wraps h so a handler panic is counted, attributed and — when
// nothing has been written yet — answered.
func HandlerMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w}
		defer func() {
			value := recover()
			if value == nil {
				return
			}
			// ErrAbortHandler is not a failure: it is how a handler says "stop, and
			// say nothing more" — the producer bridge relies on it. net/http knows
			// what to do with it and it must reach net/http unchanged and uncounted.
			if err, ok := value.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(value)
			}
			note("http "+r.Method+" "+routeLabel(r.URL.Path), value, debug.Stack())
			if recorder.wrote {
				// The client already has a status and a partial body. There is no
				// honest answer left; closing the connection is the truthful one, and
				// that is what re-panicking into net/http does.
				panic(http.ErrAbortHandler)
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"The server could not complete the request.","retryable":true}}`))
		}()
		h.ServeHTTP(recorder, r)
	})
}

// statusRecorder remembers whether anything reached the client, which decides
// whether a 500 is still possible.
type statusRecorder struct {
	http.ResponseWriter
	wrote bool
}

func (s *statusRecorder) WriteHeader(code int) {
	s.wrote = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.NewResponseController reach the real writer, so the write
// deadlines the streaming routes set still work through this wrapper.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush keeps server-sent events working: a stream that cannot flush buffers
// until the response ends, which is never.
func (s *statusRecorder) Flush() {
	s.wrote = true
	if flusher, ok := s.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// routeLabel is enough of a path to find the handler and not enough to leak a
// capability. Media and stream URLs carry an unguessable grant in the third
// segment, and a server log is not a place to put one — nor are item, profile
// and session identifiers, which are not secrets but are somebody's.
func routeLabel(path string) string {
	segments := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(segments) > 2 {
		return "/" + segments[0] + "/" + segments[1] + "/…"
	}
	return "/" + strings.Join(segments, "/")
}
