package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func TestRouterPlaybackShutdownControlsItsActualOwner(t *testing.T) {
	service := &playbackv1.Service{}
	handler := &credentialUsageHandler{playback: service}
	if err := FlushPlaybackObservations(t.Context(), handler); err != nil {
		t.Fatal(err)
	}
	if err := ShutdownPlayback(t.Context(), handler); err != nil {
		t.Fatal(err)
	}
	service.ResumeQueueBuilds() // Closed owner must refuse this without touching its nil DB.
	_, err := service.Observe(t.Context(), playbackv1.Caller{}, "closed", playbackv1.Observation{Epoch: strings.Repeat("a", 22), Seq: 1, State: "playing"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("router adapter closed a different observation owner: %v", err)
	}
	if err = ShutdownPlayback(t.Context(), handler); err != nil {
		t.Fatal(err)
	}
}
