package api

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func (th *theatre) room(method, path, token string, body any) (int, Room) {
	th.Helper()
	r := newRequest(method, path, token, body)
	r.Header.Set("Idempotency-Key", "group-"+path)
	w := serve(th.s, r)
	var out Room
	if w.Code < 300 {
		if err := jsonInto(w, &out); err != nil {
			th.Fatal(err)
		}
	}
	return w.Code, out
}

func (th *theatre) here(token, group, state string, positionMs int64, entry string) Room {
	th.Helper()
	code, out := th.room("PUT", "/v1/groups/"+group+"/me", token, GroupMe{State: state, PositionMs: positionMs, EntryID: entry})
	if code != 200 {
		th.Fatalf("reporting %s answered %d", state, code)
	}
	return out
}

// Three people on three devices: the owner hosts, Ada joins from her own account, and a
// restricted profile with Privacy hiding it joins too. The group plays only through each
// person's own access: a title the restricted profile may not see has no id or name in its
// room, waits for whoever controls to go ahead, and does not play for it. Then the host's
// connection drops: playback pauses at two minutes and Ada, in step, becomes host.
func TestWatchTogether(t *testing.T) {
	th := newTheatre(t)
	host := th.owner.AccessToken
	addMember(t, th.s, host, "ada", "member")
	ada := signInAs(t, th.s, "ada", testPassword, 2).AccessToken
	var kid Profile
	call(t, th.s, "POST", "/v1/profiles", host, ProfileCreate{Name: "Kid"}, &kid)
	age := 6
	if code := status(t, th.s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", host, Restrictions{MaxRatingAge: &age, AllLibraries: true}); code != 200 {
		t.Fatalf("restricting the kid answered %d", code)
	}
	var onKid Session
	call(t, th.s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, th.s, 3).Session.AccessToken, ProfileSelect{}, &onKid)
	kidToken := onKid.AccessToken
	if code, _ := patchPreferences(t, th.s, kidToken, "profile-server", map[string]any{"privacy.includeInWatchTogether": false}); code != 200 {
		t.Fatalf("hiding the kid from Watch Together answered %d", code)
	}

	code, made := th.room("POST", "/v1/groups", host, GroupCreate{Name: "Movie night", Control: "host"})
	if code != 201 || made.Invitation == nil || len(made.Invitation.Code) != 8 || !made.You.Host {
		t.Fatalf("starting a group answered %d %+v", code, made)
	}
	if code, again := th.room("POST", "/v1/groups", host, GroupCreate{Name: "Movie night", Control: "host"}); code != 200 || again.ID != made.ID {
		t.Fatalf("the same start sent again answered %d %s", code, again.ID)
	}
	if code, _ := th.room("POST", "/v1/groups/join", ada, GroupJoin{Code: "AAAA-AAAA"}); code != 404 {
		t.Fatalf("a wrong code answered %d", code)
	}
	hear := listen(t, th.s, ada)
	typed := strings.ToLower(made.Invitation.Code[:4] + "-" + made.Invitation.Code[4:])
	if code, joined := th.room("POST", "/v1/groups/join", ada, GroupJoin{Code: typed, DisplayName: "Ada L"}); code != 200 || joined.Invitation != nil {
		t.Fatalf("Ada joining answered %d %+v", code, joined)
	}
	th.room("POST", "/v1/groups/join", kidToken, GroupJoin{Code: made.Invitation.Code})

	code, queued := th.room("PUT", "/v1/groups/"+made.ID+"/queue", host, GroupQueue{Entries: []GroupQueueEntry{{ItemID: th.film}, {ItemID: th.song}}})
	if code != 200 || len(queued.Queue) != 2 {
		t.Fatalf("setting the queue answered %d %+v", code, queued.Queue)
	}
	hear(`"topic":"group","op":"updated","id":"` + made.ID)
	_, asKid := th.room("GET", "/v1/groups/"+made.ID, kidToken, nil)
	if film := asKid.Queue[0]; film.ItemID != "" || film.Title != "" || film.Available {
		t.Fatalf("the kid's room names a title it may not see: %+v", film)
	}
	_, asAda := th.room("GET", "/v1/groups/"+made.ID, ada, nil)
	if asAda.Queue[0].Title != "Heat" || len(asAda.Members) != 3 || asAda.Members[1].Name != "Ada L" {
		t.Fatalf("Ada's room: %+v", asAda)
	}
	for _, m := range asAda.Members {
		if m.Username == "" && !m.Private || m.Private && (m.Username != "" || m.Name != "") {
			t.Fatalf("a member is shown as %+v", m)
		}
	}
	if !asAda.Members[2].Private {
		t.Fatalf("the kid who hides from Watch Together is shown as %+v", asAda.Members[2])
	}

	// Only the host controls; a title someone may not see waits for the host to go ahead.
	if code, _ := th.room("POST", "/v1/groups/"+made.ID+"/playback", ada, GroupAction{Action: "pause"}); code != 403 {
		t.Fatalf("Ada pausing a host-controlled group answered %d", code)
	}
	_, watching := th.room("POST", "/v1/groups/"+made.ID+"/playback", host, GroupAction{Action: "watch", EntryID: queued.Queue[0].EntryID})
	if watching.CantSee != 1 || watching.Clock.WaitUntil == nil {
		t.Fatalf("watching a title the kid may not see: %+v", watching.Clock)
	}
	th.clock = th.clock.Add(6 * time.Second)
	th.s.groups.Sweep()
	if _, still := th.room("GET", "/v1/groups/"+made.ID, host, nil); still.Clock.WaitUntil == nil {
		t.Fatal("the group started a title someone may not see without the host going ahead")
	}
	if _, started := th.room("POST", "/v1/groups/"+made.ID+"/playback", host, GroupAction{Action: "goAhead"}); started.Clock.WaitUntil != nil ||
		!started.Clock.Playing {
		t.Fatalf("going ahead: %+v", started.Clock)
	}
	if code, _ := th.start(kidToken, "", PlaybackStart{ItemID: th.film}); code != 404 {
		t.Fatalf("the group let the kid play a title it may not see: %d", code)
	}

	// A seek waits for everyone up to five seconds, and no longer once everyone is ready.
	entry := queued.Queue[0].EntryID
	_, seeking := th.room("POST", "/v1/groups/"+made.ID+"/playback", host, GroupAction{Action: "seek", PositionMs: 600_000})
	if seeking.Clock.WaitUntil == nil || seeking.Clock.WaitingFor != 2 {
		t.Fatalf("a seek did not wait for the two who can play: %+v", seeking.Clock)
	}
	th.here(host, made.ID, "ready", 600_000, entry)
	if started := th.here(ada, made.ID, "ready", 600_000, entry); started.Clock.WaitUntil != nil {
		t.Fatalf("everyone was ready and the group still waits: %+v", started.Clock)
	}

	// The host's device goes quiet. At two minutes playback pauses and Ada, in step, may become host.
	for range 4 {
		th.clock = th.clock.Add(30 * time.Second)
		_, now := th.room("GET", "/v1/groups/"+made.ID, ada, nil)
		state := "playing"
		if !now.Clock.Playing {
			state = "paused"
		}
		th.here(ada, made.ID, state, now.Clock.PositionMs, entry)
		th.here(kidToken, made.ID, "paused", 0, "")
		th.s.groups.Sweep()
	}
	th.clock = th.clock.Add(time.Second)
	th.s.groups.Sweep()
	_, paused := th.room("GET", "/v1/groups/"+made.ID, ada, nil)
	if paused.Clock.Playing || paused.Host.Present {
		t.Fatalf("two minutes without the host: %+v %+v", paused.Clock, paused.Host)
	}
	th.here(ada, made.ID, "paused", paused.Clock.PositionMs, entry)
	if code, _ := th.room("PATCH", "/v1/groups/"+made.ID, kidToken, GroupEdit{BecomeHost: true}); code != 409 {
		t.Fatalf("a member out of step becoming host answered %d", code)
	}
	if code, hosted := th.room("PATCH", "/v1/groups/"+made.ID, ada, GroupEdit{BecomeHost: true}); code != 200 || !hosted.You.Host {
		t.Fatalf("Ada becoming host answered %d %+v", code, hosted.You)
	}

	// The new host ends it for everyone; the room says so.
	if code := status(t, th.s, http.MethodDelete, "/v1/groups/"+made.ID, ada, nil); code != 204 {
		t.Fatalf("ending answered %d", code)
	}
	if _, ended := th.room("GET", "/v1/groups/"+made.ID, host, nil); ended.Ended == nil || ended.Ended.Reason != "ended_by_host" {
		t.Fatalf("the ended room: %+v", ended.Ended)
	}
}
