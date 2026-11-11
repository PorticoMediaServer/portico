package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
	"github.com/PorticoMediaServer/portico/server-next/internal/receivers"
)

// listen opens the change feed as an app does and returns a function that waits until the
// feed has said something containing want.
func listen(t *testing.T, s *Server, token string) func(want string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stream := httptest.NewServer(s)
	t.Cleanup(func() { cancel(); stream.Close() })
	request, _ := http.NewRequestWithContext(ctx, "GET", stream.URL+"/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("opening the feed: %v, %v", response, err)
	}
	t.Cleanup(func() { response.Body.Close() })
	lines := make(chan string, 100)
	go func() {
		for scanner := bufio.NewScanner(response.Body); scanner.Scan(); {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	return func(want string) {
		t.Helper()
		for {
			select {
			case line, open := <-lines:
				if !open {
					t.Fatalf("the feed closed before it said %q", want)
				}
				if strings.Contains(line, want) {
					return
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("the feed did not say %q", want)
			}
		}
	}
}

func (th *theatre) ask(token, receiver, key string, body ReceiverAsk) (int, ReceiverRequest) {
	th.Helper()
	r := newRequest("POST", "/v1/receivers/"+receiver+"/requests", token, body)
	r.Header.Set("Idempotency-Key", key)
	w := serve(th.s, r)
	var out ReceiverRequest
	if w.Code < 300 {
		jsonInto(w, &out)
	}
	return w.Code, out
}

func (th *theatre) answer(token, request string, body ReceiverAnswer) ReceiverRequest {
	th.Helper()
	var out ReceiverRequest
	if w := call(th.T, th.s, "PATCH", "/v1/receivers/current/requests/"+request, token, body, &out); w.Code != 200 {
		th.Fatalf("answering %s answered %d: %s", body.Answer, w.Code, w.Body)
	}
	return out
}

// A phone moves an episode, its place, its tracks and its queue to the TV. The phone keeps
// playing until the TV plays, the two count as one stream, and the phone then stops. The
// phone can then pause the TV, which hears of it on its feed.
func TestMovingPlaybackToAnotherDevice(t *testing.T) {
	th := newTheatre(t)
	phone := th.owner.AccessToken
	tv := onMain(t, th.s, signIn(t, th.s, 1)).AccessToken
	if code := status(t, th.s, "PUT", "/v1/admin/playback/stream-limits", phone, StreamLimits{PerPerson: 1}); code != 200 {
		t.Fatalf("setting a stream limit answered %d", code)
	}
	var opened ReceiverOpen
	if w := call(t, th.s, "PUT", "/v1/receivers/current", tv, nil, &opened); w.Code != 200 || opened.RenewEveryMs == 0 {
		t.Fatalf("opening the TV answered %d %s", w.Code, w.Body)
	}
	var list Page[ReceiverItem]
	if call(t, th.s, "GET", "/v1/receivers", phone, nil, &list); len(list.Items) != 1 || list.Items[0].ID != opened.ID || list.Items[0].Allowed {
		t.Fatalf("Play on listed %+v", list.Items)
	}

	// The phone plays the show's first episode from a queue of the show, with English audio.
	show := get[ItemPage](t, th.s, phone, "/v1/items?library="+th.libs[db.LibraryShows].PublicID+"&kind=show").Items[0]
	var made QueueCreated
	if w := call(t, th.s, "POST", "/v1/queues", phone, QueueCreate{Source: playqueue.QueueSource{Container: show.ID}}, &made); w.Code != 201 {
		t.Fatalf("a queue of the show answered %d: %s", w.Code, w.Body)
	}
	english := "eng"
	for _, track := range th.options(phone, th.eps[0], "").Versions[0].Audio {
		if track.Language == "eng" {
			english = track.ID
		}
	}
	code, playing := th.start(phone, "", PlaybackStart{Queue: &playback.QueueLink{QueueID: made.Queue.ID, EntryID: made.Queue.Current.EntryID},
		StartPositionMs: ptr(60_000), Audio: &AudioPick{TrackID: english}})
	if code != 201 {
		t.Fatalf("the phone's start answered %d", code)
	}

	hear := listen(t, th.s, tv)
	ask := ReceiverAsk{Kind: "play", SessionID: playing.ID,
		Play: &receivers.Play{ItemID: th.eps[0], PositionMs: 61_000, AudioID: &english, QueueID: made.Queue.ID}}
	code, asked := th.ask(phone, opened.ID, "move-1", ask)
	if code != 201 || asked.State != "asking" || asked.From.SignInID == "" {
		t.Fatalf("asking the TV answered %d %+v", code, asked)
	}
	if code, again := th.ask(phone, opened.ID, "move-1", ask); code != 200 || again.ID != asked.ID {
		t.Fatalf("the same request sent again answered %d %s", code, again.ID)
	}
	hear(`"topic":"receiver"`)
	var pending Page[ReceiverRequest]
	if call(t, th.s, "GET", "/v1/receivers/current/requests", tv, nil, &pending); len(pending.Items) != 1 || pending.Items[0].Play.QueueID != made.Queue.ID {
		t.Fatalf("the TV was asked %+v", pending.Items)
	}
	th.answer(tv, asked.ID, ReceiverAnswer{Answer: "allowed"})

	// The TV takes the queue on at the same entry and starts there, within the limit of one.
	var copied QueueCreated
	if w := call(t, th.s, "POST", "/v1/queues", tv, QueueCreate{Source: playqueue.QueueSource{Queue: made.Queue.ID}}, &copied); w.Code != 201 ||
		copied.Queue.Total != made.Queue.Total || copied.Queue.Current.Position != made.Queue.Current.Position {
		t.Fatalf("the TV's copy of the queue answered %d %+v", w.Code, copied.Queue)
	}
	code, onTV := th.start(tv, "", PlaybackStart{Queue: &playback.QueueLink{QueueID: copied.Queue.ID, EntryID: copied.Queue.Current.EntryID},
		StartPositionMs: ptr(ask.Play.PositionMs), Audio: &AudioPick{TrackID: english}, RequestID: asked.ID})
	if code != 201 || onTV.Presentation.AudioTrackID != english {
		t.Fatalf("the TV's start answered %d with audio %q", code, onTV.Presentation.AudioTrackID)
	}
	var stillPlaying PlaybackSession
	if call(t, th.s, "GET", "/v1/playback/sessions/"+playing.ID, phone, nil, &stillPlaying); stillPlaying.State != "playing" {
		t.Fatalf("the phone stopped before the TV played: %+v", stillPlaying)
	}
	if done := th.answer(tv, asked.ID, ReceiverAnswer{Answer: "playing", SessionID: onTV.ID}); done.State != "playing" {
		t.Fatalf("the request after playing: %+v", done)
	}
	var stopped PlaybackSession
	if call(t, th.s, "GET", "/v1/playback/sessions/"+playing.ID, phone, nil, &stopped); stopped.State != "ended" {
		t.Fatalf("the phone still plays after the TV took over: %+v", stopped)
	}

	// Allowed once, the phone controls the TV: the TV is told and does it itself.
	var paused PlaybackSession
	if w := call(t, th.s, "PATCH", "/v1/playback/sessions/"+onTV.ID, phone, PlaybackChange{State: "paused"}, &paused); w.Code != 200 ||
		paused.Command == nil || paused.Command.State != "paused" || paused.Presentation.URL != "" {
		t.Fatalf("pausing the TV from the phone answered %d %s", w.Code, w.Body)
	}
	hear(`"topic":"playback","op":"updated","id":"` + onTV.ID)
	var own PlaybackSession
	if call(t, th.s, "GET", "/v1/playback/sessions/"+onTV.ID, tv, nil, &own); own.Command == nil || own.Command.ID != paused.Command.ID {
		t.Fatalf("the TV does not see the command: %+v", own.Command)
	}
}

// A device that is not asked in time, that declines, or that turned Play from other devices
// off leaves the asking device as it was, and nobody controls a device it did not allow.
func TestPlayOnRefusals(t *testing.T) {
	th := newTheatre(t)
	phone := th.owner.AccessToken
	tvIn := onMain(t, th.s, signIn(t, th.s, 1))
	tv := tvIn.AccessToken
	var opened ReceiverOpen
	call(t, th.s, "PUT", "/v1/receivers/current", tv, nil, &opened)
	playing := th.play(phone, th.film)
	onTV := th.play(tv, th.song)

	if w := call(t, th.s, "PATCH", "/v1/playback/sessions/"+onTV.ID, phone, PlaybackChange{State: "paused"}, nil); w.Code != 403 {
		t.Fatalf("controlling a device that did not allow it answered %d", w.Code)
	}
	_, declined := th.ask(phone, opened.ID, "control-1", ReceiverAsk{Kind: "control"})
	if got := th.answer(tv, declined.ID, ReceiverAnswer{Answer: "declined"}); got.State != "declined" {
		t.Fatalf("declining: %+v", got)
	}
	if w := call(t, th.s, "PATCH", "/v1/playback/sessions/"+onTV.ID, phone, PlaybackChange{State: "paused"}, nil); w.Code != 403 {
		t.Fatalf("controlling after a decline answered %d", w.Code)
	}

	// Not started in two minutes: the move failed, and the phone plays on.
	_, late := th.ask(phone, opened.ID, "move-1", ReceiverAsk{Kind: "play", SessionID: playing.ID, Play: &receivers.Play{ItemID: th.film}})
	for range 2 {
		// The TV says it is still open every minute, as it is told to.
		th.clock = th.clock.Add(time.Minute + time.Second)
		call(t, th.s, "PUT", "/v1/receivers/current", tv, nil, nil)
		th.report(phone, playing.ID, "playing", 120_000)
	}
	th.s.receivers.Sweep()
	var failed ReceiverRequest
	if call(t, th.s, "GET", "/v1/receivers/"+opened.ID+"/requests/"+late.ID, phone, nil, &failed); failed.State != "failed" || failed.Failed != "timed_out" {
		t.Fatalf("a move not started in time: %+v", failed)
	}
	if th.report(phone, playing.ID, "playing", 130_000) != 200 {
		t.Fatal("the phone's playback ended when the move failed")
	}

	// Play from other devices off: the TV is no longer listed, asked or controllable.
	_, allowed := th.ask(phone, opened.ID, "control-2", ReceiverAsk{Kind: "control"})
	th.answer(tv, allowed.ID, ReceiverAnswer{Answer: "allowed"})
	if code, _ := patchPreferences(t, th.s, tv, "profile-device", map[string]any{"devices.playFromOtherDevices": false}); code != 200 {
		t.Fatalf("turning Play from other devices off answered %d", code)
	}
	var list Page[ReceiverItem]
	call(t, th.s, "GET", "/v1/receivers", phone, nil, &list)
	code, _ := th.ask(phone, opened.ID, "move-2", ReceiverAsk{Kind: "play", Play: &receivers.Play{ItemID: th.film}})
	if len(list.Items) != 0 || code != 404 {
		t.Fatalf("with Play from other devices off the TV is listed %d times and a request answered %d", len(list.Items), code)
	}
	if w := call(t, th.s, "PATCH", "/v1/playback/sessions/"+onTV.ID, phone, PlaybackChange{State: "paused"}, nil); w.Code != 403 {
		t.Fatalf("controlling a device that turned it off answered %d", w.Code)
	}
	if w := call(t, th.s, "PUT", "/v1/receivers/current", tv, nil, nil); w.Code != 409 {
		t.Fatalf("opening a device with the setting off answered %d", w.Code)
	}
}

// live counts the account's streams that are still playing.
func (th *theatre) live(token string) int {
	th.Helper()
	var account AccountPlayback
	call(th.T, th.s, "GET", "/v1/playback/sessions", token, nil, &account)
	return len(account.Items)
}

// A move whose second device started and then failed, was cancelled or ran out of time leaves
// the account exactly as it was: the first device plays on, the second's start is undone, and
// a stream limit of one is never passed. Nobody but the two devices reads the request, and
// nobody else copies the queue it names.
func TestAFailedMoveLeavesTheAccountAsItWas(t *testing.T) {
	th := newTheatre(t)
	phone := th.owner.AccessToken
	tv := onMain(t, th.s, signIn(t, th.s, 1)).AccessToken
	if code := status(t, th.s, "PUT", "/v1/admin/playback/stream-limits", phone, StreamLimits{PerPerson: 1}); code != 200 {
		t.Fatalf("setting a stream limit answered %d", code)
	}
	var opened ReceiverOpen
	call(t, th.s, "PUT", "/v1/receivers/current", tv, nil, &opened)
	playing := th.play(phone, th.film)
	th.report(phone, playing.ID, "playing", 300_000)

	for n, ending := range []string{"failed", "cancelled", "timed_out"} {
		_, asked := th.ask(phone, opened.ID, "move-"+ending, ReceiverAsk{Kind: "play", SessionID: playing.ID,
			Play: &receivers.Play{ItemID: th.film, PositionMs: 300_000}})
		th.answer(tv, asked.ID, ReceiverAnswer{Answer: "allowed"})
		code, onTV := th.start(tv, "", PlaybackStart{ItemID: th.film, RequestID: asked.ID})
		if code != 201 {
			t.Fatalf("%s: the TV's start under the move answered %d", ending, code)
		}
		// "Allowed" sent again, as an app that retries does, keeps the move open and the TV playing.
		th.answer(tv, asked.ID, ReceiverAnswer{Answer: "allowed"})
		if code := th.report(tv, onTV.ID, "playing", 300_000); code != 200 {
			t.Fatalf("%s: the TV's report after allowing again answered %d", ending, code)
		}
		if n == 0 {
			// A third device is refused meanwhile: the two count as one stream, at the limit.
			if code, _ := th.start(onMain(t, th.s, signIn(t, th.s, 2)).AccessToken, "", PlaybackStart{ItemID: th.song}); code != 409 {
				t.Fatalf("a third device started during a move under a limit of one: %d", code)
			}
		}
		switch ending {
		case "failed":
			th.answer(tv, asked.ID, ReceiverAnswer{Answer: "failed"})
		case "cancelled":
			if code := status(t, th.s, "DELETE", "/v1/receivers/"+opened.ID+"/requests/"+asked.ID, phone, nil); code != 200 {
				t.Fatalf("cancelling answered %d", code)
			}
		case "timed_out":
			for range 2 {
				th.clock = th.clock.Add(time.Minute + time.Second)
				call(t, th.s, "PUT", "/v1/receivers/current", tv, nil, nil)
				th.report(phone, playing.ID, "playing", 300_000)
				th.report(tv, onTV.ID, "playing", 300_000)
			}
			th.s.receivers.Sweep()
		}
		if live, tvGone := th.live(phone), th.report(tv, onTV.ID, "playing", 310_000); live != 1 || tvGone != 410 ||
			th.report(phone, playing.ID, "playing", 300_000) != 200 {
			t.Fatalf("after a move that %s: %d streams play, the TV's report answered %d", ending, live, tvGone)
		}
	}

	// Only the two devices read a request; another profile of the account cannot read it or
	// copy the queue it names.
	var kid Profile
	call(t, th.s, "POST", "/v1/profiles", phone, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	call(t, th.s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, th.s, 3).Session.AccessToken, ProfileSelect{}, &onKid)
	var made QueueCreated
	call(t, th.s, "POST", "/v1/queues", phone, QueueCreate{Source: playqueue.QueueSource{Items: th.eps[:]}}, &made)
	_, asked := th.ask(phone, opened.ID, "move-queue", ReceiverAsk{Kind: "play", Play: &receivers.Play{ItemID: th.eps[0], QueueID: made.Queue.ID}})
	if code := status(t, th.s, "GET", "/v1/receivers/"+opened.ID+"/requests/"+asked.ID, onKid.AccessToken, nil); code != 404 {
		t.Errorf("another profile read a request: %d", code)
	}
	if code := status(t, th.s, "POST", "/v1/queues", onKid.AccessToken, QueueCreate{Source: playqueue.QueueSource{Queue: made.Queue.ID}}); code != 404 {
		t.Errorf("another profile copied a queue it was not offered: %d", code)
	}
	if code := status(t, th.s, "POST", "/v1/queues", tv, QueueCreate{Source: playqueue.QueueSource{Queue: made.Queue.ID}}); code != 201 {
		t.Errorf("the asked TV could not copy the queue it was offered: %d", code)
	}
}

// Another of the account's devices may stop a stream, but where it says it is does not become
// that stream's resume point.
func TestStoppingAnotherDeviceKeepsItsPlace(t *testing.T) {
	th := newTheatre(t)
	phone := th.owner.AccessToken
	tablet := onMain(t, th.s, signIn(t, th.s, 1)).AccessToken
	playing := th.play(phone, th.film)
	th.report(phone, playing.ID, "paused", 600_000)
	end := int64(hour - 60_000)
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+playing.ID, tablet, PlaybackStop{PositionMs: &end}); code != 204 {
		t.Fatalf("stopping from the tablet answered %d", code)
	}
	if state := th.state(th.film); state.PositionMS != 600_000 || state.Watched {
		t.Fatalf("the phone's place became the tablet's: %+v", state)
	}
	var stopped PlaybackSession
	if call(t, th.s, "GET", "/v1/playback/sessions/"+playing.ID, phone, nil, &stopped); stopped.End == nil || stopped.End.Reason != "stopped_elsewhere" {
		t.Fatalf("the phone is told %+v", stopped.End)
	}
}
