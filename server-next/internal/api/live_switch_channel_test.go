package api

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// A device watching the only tuner's channel can change to another channel of the same
// source: what it was watching gives its tuner up, and its own viewing is not counted
// against its account's limit.
func TestADeviceChangesChannelOnAOneTunerSource(t *testing.T) {
	s, owner, members := seededWith(t, 50, 1)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close)
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(1), Config: &live.SourceConfig{PlaylistURL: fake.URL + "/tuned.m3u"}})
	channels := get[Page[Channel]](t, s, members[0], "/v1/live/channels").Items
	var first, second PlaybackSession
	if w := call(t, s, "POST", "/v1/playback/sessions", members[0], PlaybackStart{Channel: channels[0].ID}, &first); w.Code != 201 {
		t.Fatalf("watching answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/v1/playback/sessions", members[0], PlaybackStart{Channel: channels[1].ID}, &second); w.Code != 201 {
		t.Fatalf("the same device changing channel answered %d: %s", w.Code, w.Body)
	}
}
