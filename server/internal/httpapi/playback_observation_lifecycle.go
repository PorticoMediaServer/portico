package httpapi

import (
	"context"
	"net/http"
)

// FlushPlaybackObservations saves one bounded pass over the router's pending
// renderer samples before an online backup. New ticks may continue afterwards.
func FlushPlaybackObservations(ctx context.Context, handler http.Handler) error {
	if h, ok := handler.(*credentialUsageHandler); ok && h.playback != nil {
		return h.playback.FlushObservations(ctx)
	}
	return nil
}

// ClosePlaybackObservations joins the router's observation worker and flushes
// its pending samples after HTTP drain, before the database closes.
func ClosePlaybackObservations(ctx context.Context, handler http.Handler) error {
	if h, ok := handler.(*credentialUsageHandler); ok && h.playback != nil {
		return h.playback.CloseObservations(ctx)
	}
	return nil
}
