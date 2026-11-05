package playbackv1

import (
	"context"
	"testing"
	"time"
)

func cleanupPlaybackService(t testing.TB, service *Service) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("playback fixture shutdown: %v", err)
		}
	})
}
