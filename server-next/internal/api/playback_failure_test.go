package api

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// A stream that stops working by itself in the middle of playing (its conversion failed) is
// moved to the next way of playing by the server, where the viewer was, without waiting for
// the player to find the dead address. When no way is left the session ends as failed.
func TestAStreamThatFailsMidPlayMovesToTheNextWay(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	session := th.play(token, th.film)
	path := "/v1/playback/sessions/" + session.ID
	call(t, th.s, "PUT", path+"/progress", token, PlaybackProgress{State: "playing", PositionMs: 30_000}, nil)
	read := func() PlaybackSession {
		var now PlaybackSession
		call(t, th.s, "GET", path, token, nil, &now)
		return now
	}
	// fail reports that the stream at an address failed, as the engine does.
	fail := func(url string) PlaybackSession {
		th.engine.mu.Lock()
		failed := th.engine.running[session.ID].Failed
		th.engine.mu.Unlock()
		failed(playback.Stream{Grant: strings.Split(url, "/")[3], URL: url})
		return read()
	}
	first := read().Presentation.URL
	second := fail(first)
	if p := second.Presentation; p.Route != "repackaged" || p.Generation != 2 || p.StartPositionMs != 30_000 {
		t.Fatalf("after the stream failed: %+v", p)
	}
	// A failure of a stream the session no longer plays changes nothing.
	if now := fail(first); now.Presentation.URL != second.Presentation.URL || now.Presentation.Generation != 2 || now.State == "ended" {
		t.Fatalf("after a failure of the stream it had before: %+v", now)
	}
	third := fail(second.Presentation.URL)
	if p := third.Presentation; p.Route != "converted" || p.Generation != 3 {
		t.Fatalf("after the second way failed: %+v", p)
	}
	if now := fail(third.Presentation.URL); now.State != "ended" || now.End == nil || now.End.Reason != "failed" || th.engine.streams() != 0 {
		t.Fatalf("after the last way failed: %+v, %d streams", now, th.engine.streams())
	}
}
