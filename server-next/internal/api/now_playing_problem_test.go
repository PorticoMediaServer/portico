package api

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// A player that says it is waiting for its stream shows as Buffering in Now playing; after
// ten seconds of waiting it is a problem, listed before every other stream and counted on the
// Dashboard; the next time it says it is playing, it is neither.
func TestAStreamWaitingForItsStreamIsAProblemAfterTenSeconds(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	tv := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken
	_, waiting := th.start(tv, "", PlaybackStart{ItemID: th.film})
	th.clock.Add(time.Second)
	_, newer := th.start(owner, "", PlaybackStart{ItemID: th.eps[0]})
	if code := th.report(tv, waiting.ID, playback.StateBuffering, 60_000); code != 200 {
		t.Fatalf("reporting buffering answered %d", code)
	}
	read := func() []NowPlaying {
		t.Helper()
		return get[Page[NowPlaying]](t, th.s, owner, "/v1/admin/sessions").Items
	}
	items := read()
	if len(items) != 2 || items[0].ID != newer.ID || items[1].State != playback.StateBuffering || items[1].BufferingSince == nil || items[1].Problem {
		t.Fatalf("just after the player said it was buffering, Now playing reads %+v", items)
	}

	th.clock.Add(ProblemAfter)
	items = read()
	if items[0].ID != waiting.ID || !items[0].Problem || items[1].Problem {
		t.Fatalf("after ten seconds of buffering, Now playing reads %+v", items)
	}
	if got := get[Dashboard](t, th.s, owner, "/v1/admin/dashboard"); got.NowPlaying.Problems != 1 || got.NowPlaying.Streams != 2 {
		t.Fatalf("the Dashboard counts %+v", got.NowPlaying)
	}
	if got := get[ActivitySummary](t, th.s, owner, "/v1/admin/activity"); len(got.Streams.Items) == 0 || got.Streams.Items[0].ID != waiting.ID {
		t.Fatalf("the activity popover does not list the problem first: %+v", got.Streams)
	}

	if code := th.report(tv, waiting.ID, playback.StatePlaying, 60_000); code != 200 {
		t.Fatalf("reporting playing answered %d", code)
	}
	items = read()
	if items[0].ID != newer.ID || items[1].State != playback.StatePlaying || items[1].BufferingSince != nil || items[1].Problem {
		t.Fatalf("once playing again, Now playing reads %+v", items)
	}
	if got := get[Dashboard](t, th.s, owner, "/v1/admin/dashboard"); got.NowPlaying.Problems != 0 {
		t.Fatalf("once playing again the Dashboard counts %d problems", got.NowPlaying.Problems)
	}
}
