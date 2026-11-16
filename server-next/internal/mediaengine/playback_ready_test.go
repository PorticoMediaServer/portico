package mediaengine_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Start a play as a client does: 202 keeps the same session and names when to
// ask again. Only that contract's preparing answer is retried; errors fail.
func (h *house) startPlayback(body api.PlaybackStart) api.PlaybackSession {
	h.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	key, id := db.NewID("start"), ""
	for {
		if ctx.Err() != nil {
			h.Fatal("playback did not become ready")
		}
		response := h.do("POST", "/v1/playback/sessions", body, "Idempotency-Key", key)
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			h.Fatal(err)
		}
		switch response.StatusCode {
		case http.StatusCreated, http.StatusOK, http.StatusAccepted:
		default:
			h.Fatalf("starting playback answered %d: %s", response.StatusCode, raw)
		}
		var next api.PlaybackSession
		if err := json.Unmarshal(raw, &next); err != nil {
			h.Fatal(err)
		}
		if next.ID == "" || id != "" && next.ID != id {
			h.Fatal("preparing start lost or replaced its session")
		}
		id = next.ID
		if response.StatusCode != http.StatusAccepted {
			return next
		}
		if next.RetryAfterMs <= 0 {
			h.Fatal("preparing start did not name a retry delay")
		}
		select {
		case <-time.After(time.Duration(next.RetryAfterMs) * time.Millisecond):
		case <-ctx.Done():
			h.Fatal("playback did not become ready")
		}
	}
}
