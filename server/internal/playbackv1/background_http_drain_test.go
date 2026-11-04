package playbackv1

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestPlaybackShutdownPreservesHTTPDrainOnSignalAndServeFailure(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal-signal", true: "early-serve-error"}[early], func(t *testing.T) {
			f := newObservationFixture(t)
			epoch := f.claim(f.session.ID, "")
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			observed := make(chan error, 1)
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				_, err := f.tick(f.session.ID, epoch, 1, "playing")
				observed <- err
				if err != nil {
					w.WriteHeader(503)
				}
			})}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			served := make(chan error, 1)
			go func() { served <- server.Serve(listener) }()
			client := make(chan error, 1)
			go func() {
				reply, e := http.Get("http://" + listener.Addr().String())
				if e == nil {
					reply.Body.Close()
				}
				client <- e
			}()
			<-entered
			if early {
				listener.Close()
				if err = <-served; errors.Is(err, http.ErrServerClosed) {
					t.Fatal("expected listener failure")
				}
			}
			drained := make(chan struct{})
			drainStarted := make(chan struct{})
			go func() {
				defer close(drained)
				close(drainStarted)
				ctx, end := context.WithTimeout(context.Background(), time.Second)
				defer end()
				server.Shutdown(ctx)
			}()
			<-drainStarted
			if !early {
				if err = <-served; !errors.Is(err, http.ErrServerClosed) {
					t.Fatal(err)
				}
			}
			// A shutdown request before the drain completes must time out waiting; it
			// must not cancel playback while an admitted handler still owns it.
			wait, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			if err = f.service.ShutdownAfterHTTPDrain(wait, drained, time.Second); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("shutdown bypassed HTTP drain: %v", err)
			}
			select {
			case <-drained:
				t.Error("HTTP drained while handler blocked")
			default:
			}
			unblock()
			if err = <-client; err != nil {
				t.Fatal(err)
			}
			if err = <-observed; err != nil {
				t.Errorf("admitted request cut short: %v", err)
			}
			<-drained
			join, end := context.WithTimeout(context.Background(), time.Second)
			defer end()
			if err = f.service.ShutdownAfterHTTPDrain(join, drained, time.Second); err != nil {
				t.Fatal(err)
			}
		})
	}
}
