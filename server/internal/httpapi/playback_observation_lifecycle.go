package httpapi

import (
	"context"
	"net/http"
	"time"
)

// FlushPlaybackObservations saves one bounded pass over the router's pending
// renderer samples before an online backup. New ticks may continue afterwards.
func FlushPlaybackObservations(ctx context.Context, handler http.Handler) error {
	if h, ok := handler.(*credentialUsageHandler); ok && h.playback != nil {
		return h.playback.FlushObservations(ctx)
	}
	return nil
}

// ShutdownPlayback cancels and joins the router's background database work and
// flushes pending observations after HTTP drain, before the database closes.
func ShutdownPlayback(ctx context.Context, handler http.Handler) error {
	if h, ok := handler.(*credentialUsageHandler); ok && h.playback != nil {
		return h.playback.Shutdown(ctx)
	}
	return nil
}

// ShutdownPlaybackAfterHTTPDrain uses the same ordering for signal shutdown and
// an unexpected Serve failure. Waiting for HTTP does not consume playback's budget.
func ShutdownPlaybackAfterHTTPDrain(ctx context.Context, handler http.Handler, drained <-chan struct{}, budget time.Duration) error {
	if h, ok := handler.(*credentialUsageHandler); ok && h.playback != nil {
		return h.playback.ShutdownAfterHTTPDrain(ctx, drained, budget)
	}
	return nil
}
