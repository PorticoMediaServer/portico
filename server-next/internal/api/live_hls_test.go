package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// standInHLS stands in for the media engine's live HLS: it answers what it is told to, and
// records what it was asked.
type standInHLS struct {
	mu      sync.Mutex
	play    *live.Play
	err     error
	started []live.Conversion
	stopped []string
	failed  bool
}

func (f *standInHLS) StartLive(_ context.Context, r live.Conversion) (*live.Play, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, r)
	return f.play, f.err
}

func (f *standInHLS) ServeLive(w http.ResponseWriter, _ *http.Request, sessionID, file string) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Write([]byte("#EXTM3U\n# " + sessionID + " " + file + "\n"))
}

func (f *standInHLS) StopLive(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, sessionID)
}

func (f *standInHLS) LiveFailed(string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failed
}

// A channel the device cannot play as the source sends it is played as HLS made for the
// device: the session names its playlist beneath the stream's address, with the route and
// container. However the session ends, the HLS stops with it. A conversion the server is too
// busy for is refused with the plain "busy" answer, and the tuner is given back at once, so
// another channel on a one-tuner source plays.
func TestAChannelTheDeviceCannotPlayIsServedAsHLS(t *testing.T) {
	s, owner, members := seededWith(t, 50, 1)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close)
	hls := &standInHLS{play: &live.Play{Route: "converted", Engine: "native", Container: "mpegts_hls"}}
	s.LiveHLSWith(hls)
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(1), Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/tuned.m3u"}})
	channels := get[Page[LiveChannel]](t, s, members[0], "/v1/live/channels").Items
	if len(channels) != 2 {
		t.Fatalf("channels: %+v", channels)
	}

	var session PlaybackSession
	if w := call(t, s, "POST", "/v1/playback/sessions", members[0], PlaybackStart{Channel: channels[0].ID, Network: "cellular"}, &session); w.Code != 201 {
		t.Fatalf("watching answered %d: %s", w.Code, w.Body)
	}
	p := session.Presentation
	if p.Mode != "live" || !strings.HasSuffix(p.URL, "/index.m3u8") || p.Route != "converted" || p.Container != "mpegts_hls" || p.Engine != "native" {
		t.Fatalf("the presentation reads %+v", p)
	}
	hls.mu.Lock()
	asked := append([]live.Conversion{}, hls.started...)
	hls.mu.Unlock()
	if len(asked) != 1 || asked[0].SessionID != session.ID || asked[0].Network != playback.NetworkCellular || asked[0].Input == nil {
		t.Fatalf("the engine was asked %+v", asked)
	}
	w := serve(s, newRequest("GET", p.URL, "", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), session.ID+" index.m3u8") {
		t.Fatalf("the playlist answered %d: %s", w.Code, w.Body)
	}
	// The session reads the same, and says when the conversion failed.
	hls.mu.Lock()
	hls.failed = true
	hls.mu.Unlock()
	var read PlaybackSession
	call(t, s, "GET", "/v1/playback/sessions/"+session.ID, members[0], nil, &read)
	if read.State != playback.StateEnded || read.End == nil || read.End.Reason != "failed" {
		t.Fatalf("a failed conversion's session reads %s %+v", read.State, read.End)
	}
	hls.mu.Lock()
	hls.failed = false
	hls.mu.Unlock()
	status(t, s, "DELETE", "/v1/playback/sessions/"+session.ID, members[0], nil)
	hls.mu.Lock()
	stopped := append([]string{}, hls.stopped...)
	hls.mu.Unlock()
	if len(stopped) != 1 || stopped[0] != session.ID {
		t.Fatalf("stopping the session stopped %v", stopped)
	}
	if code := status(t, s, "GET", p.URL, "", nil); code != 404 {
		t.Fatalf("a stopped session's playlist answered %d", code)
	}

	// Busy: refused plainly, and the one tuner is free again for the other channel.
	hls.mu.Lock()
	hls.err = playback.ErrBusy
	hls.mu.Unlock()
	var refused struct {
		Error Error `json:"error"`
	}
	if w := call(t, s, "POST", "/v1/playback/sessions", members[0], PlaybackStart{Channel: channels[0].ID}, &refused); w.Code != http.StatusTooManyRequests ||
		refused.Error.Code != "conversion_capacity" {
		t.Fatalf("a conversion the server is too busy for answered %d %+v", w.Code, refused.Error)
	}
	hls.mu.Lock()
	hls.err, hls.play = nil, nil // this device plays the source's stream as it is
	hls.mu.Unlock()
	if w := call(t, s, "POST", "/v1/playback/sessions", owner, PlaybackStart{Channel: channels[1].ID}, &session); w.Code != 201 {
		t.Fatalf("after a refused conversion the tuner is still held: %d %s", w.Code, w.Body)
	}
	if session.Presentation.Route != "original" || strings.HasSuffix(session.Presentation.URL, ".m3u8") {
		t.Fatalf("a device that plays the source's stream reads %+v", session.Presentation)
	}
}
