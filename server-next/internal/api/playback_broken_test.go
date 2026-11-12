package api

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// A stream that fails because the server could not start converting at all ends the session
// with that said to the viewer. No other way of playing is tried, and nothing is remembered
// against this way of playing on the device: it was not the device's doing.
func TestAConversionTheServerCannotStartEndsTheSessionSayingSo(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	session := th.play(token, th.film)
	running := th.s.playback.All()[0]
	th.engine.mu.Lock()
	failed := th.engine.running[session.ID].Failed
	th.engine.mu.Unlock()
	var now PlaybackSession
	call(t, th.s, "GET", "/v1/playback/sessions/"+session.ID, token, nil, &now)
	failed(playback.Stream{Grant: strings.Split(now.Presentation.URL, "/")[3], Problem: playback.ConversionBroken})
	call(t, th.s, "GET", "/v1/playback/sessions/"+session.ID, token, nil, &now)
	if now.State != "ended" || now.End == nil || now.End.Reason != "failed" || now.End.Message != playback.ConversionBroken || th.engine.streams() != 0 {
		t.Fatalf("after the server could not start converting: %+v, %d streams", now, th.engine.streams())
	}
	item, err := th.s.db.PlaybackItem(context.Background(), running.Viewer(), th.film)
	if err != nil {
		t.Fatal(err)
	}
	failures, err := th.s.db.RouteFailures(context.Background(), running.Device.ID, item.Versions[0].FileID, th.clock.Now())
	if err != nil || len(failures) != 0 {
		t.Fatalf("a failure of the server's own was remembered against the device: %+v (%v)", failures, err)
	}
}
