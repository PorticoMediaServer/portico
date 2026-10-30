package remotesources

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSequentialSessionFencesExtentsWithoutSeekingOrMaterialization(t *testing.T) {
	for _, change := range []string{"", "metadata", "actor", "short"} {
		t.Run(change, func(t *testing.T) {
			ready := make(chan struct{})
			var changed, revoked atomic.Bool
			var gets, notices atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gets.Add(1)
				if r.Header.Get("Range") != "" || r.Header.Get("If-Match") != `"served"` {
					t.Error("sequential request became a range")
				}
				w.Header().Set("Content-Length", "8")
				w.Header().Set("ETag", `"served"`)
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				select {
				case <-ready:
				case <-r.Context().Done():
					return
				}
				if change == "metadata" {
					changed.Store(true)
				}
				if change == "actor" {
					revoked.Store(true)
				}
				if change == "short" {
					w.Write([]byte("bad"))
					return
				}
				w.Write([]byte("bookfile"))
			}))
			defer server.Close()
			endpoint, err := remotemedia.NewServeEndpoint(server.Listener.Addr().(*net.TCPAddr).Port, "private", "secret", remotemedia.NewOperationBudget(context.Background(), 1))
			if err != nil {
				t.Fatal(err)
			}
			defer endpoint.Close()
			facts := remotemedia.FileObservation{SourceID: "remote", Incarnation: "1", Object: "book.pdf", Size: 8, ServeETag: `"served"`}
			admission := SequentialAdmission{Scan: facts, Check: func(ctx context.Context) error {
				if revoked.Load() {
					return storage.ErrRemoteOffline
				}
				return ctx.Err()
			}, Observe: func(context.Context) (remotemedia.FileObservation, error) {
				next := facts
				if changed.Load() {
					next.Size++
				}
				return next, nil
			}, OnChanged: func() { notices.Add(1) }}
			session, err := OpenSequentialSession(context.Background(), endpoint, admission)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			close(ready)
			buf := []byte("--------")
			n, err := session.Read(context.Background(), buf)
			if change != "" {
				if n != 0 || err == nil || string(buf) != "--------" {
					t.Fatal("unfenced extent exposed", n, string(buf), err)
				}
				if n, err = session.Read(context.Background(), buf); n != 0 || err == nil || string(buf) != "--------" {
					t.Fatal("failed stream reopened", n, err)
				}
				if change == "metadata" && notices.Load() != 1 {
					t.Fatal("change notification", notices.Load())
				}
			} else {
				if n != 8 || err != nil || string(buf) != "bookfile" {
					t.Fatal(n, string(buf), err)
				}
				if n, err = session.Read(context.Background(), buf); n != 0 || err != io.EOF {
					t.Fatal(n, err)
				}
			}
			if gets.Load() != 1 {
				t.Fatal("stream replayed", gets.Load())
			}
		})
	}
}

func TestSequentialCloseCancelsBlockedBodyAndNeverReopens(t *testing.T) {
	bodyClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "8")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(bodyClosed)
	}))
	defer server.Close()
	endpoint, _ := remotemedia.NewServeEndpoint(server.Listener.Addr().(*net.TCPAddr).Port, "private", "secret", remotemedia.NewOperationBudget(context.Background(), 1))
	defer endpoint.Close()
	facts := remotemedia.FileObservation{SourceID: "remote", Incarnation: "1", Object: "book.pdf", Size: 8}
	session, err := OpenSequentialSession(context.Background(), endpoint, SequentialAdmission{Scan: facts, Check: func(ctx context.Context) error { return ctx.Err() }, Observe: func(context.Context) (remotemedia.FileObservation, error) { return facts, nil }, OnChanged: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		buf := []byte("----")
		n, err := session.Read(context.Background(), buf)
		if n != 0 || string(buf) != "----" {
			done <- errors.New("blocked extent exposed")
		} else {
			done <- err
		}
	}()
	session.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed stream read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not cancel read")
	}
	select {
	case <-bodyClosed:
	case <-time.After(time.Second):
		t.Fatal("HTTP body survived close")
	}
}
