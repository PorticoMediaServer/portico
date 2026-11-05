package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// Capture the actual owner, even when a test later replaces its router/service.
func cleanupPlaybackService(t testing.TB, service *playbackv1.Service) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("playback fixture shutdown: %v", err)
		}
	})
}

func cleanupRouterPlayback(t testing.TB, handler http.Handler) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := ShutdownPlayback(ctx, handler); err != nil {
			t.Errorf("playback fixture shutdown: %v", err)
		}
	})
}

// The first explicit fixture cleanup closes the DB. Intercept that boundary
// while leaving testing's own TempDir cleanup and LIFO execution intact.
type fixtureCloseWitness struct {
	*testing.T
	registered  bool
	beforeClose func()
}

func (w *fixtureCloseWitness) Cleanup(cleanup func()) {
	if !w.registered {
		w.registered = true
		w.T.Cleanup(func() {
			if w.beforeClose != nil {
				w.beforeClose()
			}
			cleanup()
		})
		return
	}
	w.T.Cleanup(cleanup)
}

func TestTL6FixtureShutsPlaybackDownBeforeDatabaseClose(t *testing.T) {
	w := &fixtureCloseWitness{T: t}
	f := newTL6V1Fixture(w, 1)
	var queue playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "fixture-cleanup-00001"}, map[string]any{
		"selector": map[string]any{"items": map[string]any{"ids": f.items}},
	}, 201, &queue) // Queue creation admits asynchronous resume/sweep work.
	w.beforeClose = func() {
		if err := f.db.Ping(); err != nil {
			t.Errorf("database closed before playback cleanup witness: %v", err)
		}
		// Closed admission is observable without starting another DB operation.
		// Shutdown's join contract is separately exercised by playbackv1 tests.
		_, err := f.v1.Observe(context.Background(), playbackv1.Caller{}, "closed", playbackv1.Observation{
			Epoch: strings.Repeat("a", 22), Seq: 1, State: "playing",
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("playback owner still live at database close: %v", err)
		}
	}
}
