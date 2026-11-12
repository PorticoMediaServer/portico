package api

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// A live channel watched is a stream like any title (area 11 rule 22): it is in the owner's
// Now playing and the Dashboard's count, it counts against the server's stream limit for a
// title and for another channel, the owner can stop it with a message the viewer is told,
// and a title started on the same device replaces it.
func TestALiveChannelWatchedIsAStream(t *testing.T) {
	th := newTheatre(t)
	fake := fakeProvider(t, th.s.now())
	t.Cleanup(th.s.tuners.Close) // before the fake source closes, which waits for its streams
	owner := th.owner.AccessToken
	addSource(t, th.s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(2), Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/tuned.m3u"}})
	channels := get[Page[LiveChannel]](t, th.s, owner, "/v1/live/channels").Items
	if len(channels) != 2 {
		t.Fatalf("channels: %+v", channels)
	}
	tv := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken
	var watching PlaybackSession
	if w := call(t, th.s, "POST", "/v1/playback/sessions", tv, PlaybackStart{Channel: channels[0].ID}, &watching); w.Code != 201 || watching.Kind != "live" {
		t.Fatalf("watching answered %d: %s", w.Code, w.Body)
	}

	playing := get[Page[NowPlaying]](t, th.s, owner, "/v1/admin/sessions").Items
	if len(playing) != 1 || playing[0].ID != watching.ID || playing[0].Kind != "live" || playing[0].Title != channels[0].Name || playing[0].DeviceName == "" {
		t.Fatalf("Now playing reads %+v", playing)
	}
	if got := get[Dashboard](t, th.s, owner, "/v1/admin/dashboard"); got.NowPlaying.Streams != 1 {
		t.Fatalf("the Dashboard counts %d streams", got.NowPlaying.Streams)
	}

	// At a server limit of one, the channel watched leaves no room for a title or another channel.
	if code := status(t, th.s, "PUT", "/v1/admin/playback/stream-limits", owner, StreamLimits{Total: 1}); code != 200 {
		t.Fatalf("setting the limit answered %d", code)
	}
	if code, _ := th.start(owner, "", PlaybackStart{ItemID: th.film}); code == 201 || code == 202 {
		t.Fatalf("a title past the server's limit started (%d)", code)
	}
	if code := status(t, th.s, "POST", "/v1/playback/sessions", owner, PlaybackStart{Channel: channels[1].ID}); code == 201 {
		t.Fatal("a second channel past the server's limit started")
	}
	if code := status(t, th.s, "PUT", "/v1/admin/playback/stream-limits", owner, StreamLimits{}); code != 200 {
		t.Fatalf("lifting the limit answered %d", code)
	}

	// Stopped by the owner, the viewer is told so with the message.
	if code := status(t, th.s, "DELETE", "/v1/admin/sessions/"+watching.ID, owner, StreamStop{Message: "Dinner is ready"}); code != 204 {
		t.Fatalf("stopping the stream answered %d", code)
	}
	if got := get[PlaybackSession](t, th.s, tv, "/v1/playback/sessions/"+watching.ID); got.State != playback.StateEnded || got.End == nil ||
		got.End.Reason != playback.EndTerminated || got.End.Message != "Dinner is ready" {
		t.Fatalf("the stopped viewer reads %+v", got)
	}

	// A title started on the device replaces what it watched live.
	call(t, th.s, "POST", "/v1/playback/sessions", tv, PlaybackStart{Channel: channels[0].ID}, &watching)
	if code, _ := th.start(tv, "", PlaybackStart{ItemID: th.film}); code != 201 && code != 202 {
		t.Fatalf("a title on the device answered %d", code)
	}
	if code := status(t, th.s, "GET", "/v1/playback/sessions/"+watching.ID, tv, nil); code != 404 {
		t.Fatalf("the channel replaced by a title answered %d", code)
	}
	if got := get[Dashboard](t, th.s, owner, "/v1/admin/dashboard"); got.NowPlaying.Streams != 1 {
		t.Fatalf("after the title replaced the channel the Dashboard counts %d streams", got.NowPlaying.Streams)
	}
}
