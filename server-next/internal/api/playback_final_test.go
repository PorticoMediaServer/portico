package api

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A final position remains a save of that play after its lease, retained record or process
// has gone. Replaying it cannot spend a second play or lose the position silently.
func TestFinalPositionSurvivesTheSession(t *testing.T) {
	for _, stage := range []string{"expired", "forgotten", "restarted"} {
		t.Run(stage, func(t *testing.T) {
			th := newTheatre(t)
			th.clock.Add(-time.Hour) // observations stay in the past of the real database clock
			session := th.play(th.owner.AccessToken, th.film)
			if session.ProgressToken == "" {
				t.Fatal("the play has no final-position token")
			}
			th.report(th.owner.AccessToken, session.ID, "paused", 600_000)
			th.clock.Add(30 * time.Second)
			observed := th.clock.Now()
			th.clock.Add(121 * time.Second)
			th.s.playback.Sweep(context.Background())
			if stage != "expired" {
				th.clock.Add(3 * time.Minute)
				th.s.playback.Sweep(context.Background())
			}
			if stage == "restarted" {
				stopServer(th.s)
				closeDB(th.s.db)
				th.s = openTestServer(t, th.path)
				th.s.setClock(th.clock.Now)
				th.owner = onMain(t, th.s, signIn(t, th.s, 0))
			}
			if got := th.state(th.film).PositionMS; got != 600_000 {
				t.Fatalf("checkpoint before replay: %d", got)
			}
			body := PlaybackStop{ProgressToken: session.ProgressToken, PositionMs: ptr(630_000),
				MadeAgoMs: th.clock.Now().Sub(observed).Milliseconds(), PlayClockMs: 30_000}
			for range 2 {
				if w := call(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, th.owner.AccessToken, body, nil); w.Code != 204 {
					t.Fatalf("final observation: %d %s", w.Code, w.Body)
				}
			}
			if got := th.state(th.film).PositionMS; got != 630_000 {
				t.Fatalf("final position was lost after %s: %d", stage, got)
			}
		})
	}
}

func TestDelayedFinalPositionCannotOverwriteANewerDevice(t *testing.T) {
	th := newTheatre(t)
	th.clock.Add(-time.Hour)
	a := th.play(th.owner.AccessToken, th.film)
	th.clock.Add(30 * time.Second)
	observed := th.clock.Now()
	th.clock.Add(10 * time.Second)
	other := onMain(t, th.s, signIn(t, th.s, 4))
	b := th.play(other.AccessToken, th.film)
	th.clock.Add(10 * time.Second)
	if w := call(t, th.s, "DELETE", "/v1/playback/sessions/"+b.ID, other.AccessToken,
		PlaybackStop{ProgressToken: b.ProgressToken, PositionMs: ptr(1_200_000), PlayClockMs: 10_000}, nil); w.Code != 204 {
		t.Fatal(w.Body)
	}
	th.clock.Add(10 * time.Second)
	body := PlaybackStop{ProgressToken: a.ProgressToken, PositionMs: ptr(600_000),
		MadeAgoMs: th.clock.Now().Sub(observed).Milliseconds(), PlayClockMs: 30_000}
	if w := call(t, th.s, "DELETE", "/v1/playback/sessions/"+a.ID, th.owner.AccessToken, body, nil); w.Code != 204 {
		t.Fatal(w.Body)
	}
	if got := th.state(th.film).PositionMS; got != 1_200_000 {
		t.Fatalf("the older device replaced the newer observation: %d", got)
	}
	for _, tc := range []struct{ name, token, id, claim string }{
		{"other device", other.AccessToken, a.ID, a.ProgressToken},
		{"other play", th.owner.AccessToken, b.ID, a.ProgressToken},
		{"tampered token", th.owner.AccessToken, a.ID, a.ProgressToken + "x"},
	} {
		body.ProgressToken = tc.claim
		if w := call(t, th.s, "DELETE", "/v1/playback/sessions/"+tc.id, tc.token, body, nil); w.Code != 404 {
			t.Fatalf("%s accepted: %d %s", tc.name, w.Code, w.Body)
		}
	}
}

func TestFinalPositionKeepsItsOriginalProfile(t *testing.T) {
	th := newTheatre(t)
	th.clock.Add(-time.Hour)
	session := th.play(th.owner.AccessToken, th.film)
	th.clock.Add(time.Minute)
	var profile Profile
	if w := call(t, th.s, "POST", "/v1/profiles", th.owner.AccessToken, ProfileCreate{Name: "Other"}, &profile); w.Code != 200 {
		t.Fatal(w.Body)
	}
	var selected Session
	if w := call(t, th.s, "POST", "/v1/profiles/"+profile.ID+"/select", th.owner.AccessToken, ProfileSelect{}, &selected); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := call(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, selected.AccessToken,
		PlaybackStop{ProgressToken: session.ProgressToken, PositionMs: ptr(630_000), PlayClockMs: 60_000}, nil); w.Code != 204 {
		t.Fatal(w.Body)
	}
	if got := th.state(th.film).PositionMS; got != 630_000 {
		t.Fatalf("the original profile lost its position: %d", got)
	}
	if got := get[Item](t, th.s, selected.AccessToken, "/v1/items/"+th.film).State.PositionMs; got != 0 {
		t.Fatalf("the new profile received another profile's position: %d", got)
	}
}

// Deleting and re-creating a profile may reuse SQLite's internal id. A token keeps the
// public identity too, and checks it in the same write as the observation.
func TestFinalPositionDoesNotFollowADeletedProfile(t *testing.T) {
	th := newTheatre(t)
	th.clock.Add(-time.Hour)
	var old Profile
	call(t, th.s, "POST", "/v1/profiles", th.owner.AccessToken, ProfileCreate{Name: "Old"}, &old)
	var selected Session
	call(t, th.s, "POST", "/v1/profiles/"+old.ID+"/select", th.owner.AccessToken, ProfileSelect{}, &selected)
	playing := th.play(selected.AccessToken, th.film)
	th.clock.Add(time.Minute)
	th.owner = onMain(t, th.s, signIn(t, th.s, 0))
	if w := call(t, th.s, "DELETE", "/v1/profiles/"+old.ID, th.owner.AccessToken, nil, nil); w.Code != 204 {
		t.Fatal(w.Body)
	}
	var replacement Profile
	call(t, th.s, "POST", "/v1/profiles", th.owner.AccessToken, ProfileCreate{Name: "Replacement"}, &replacement)
	if w := call(t, th.s, "DELETE", "/v1/playback/sessions/"+playing.ID, th.owner.AccessToken,
		PlaybackStop{ProgressToken: playing.ProgressToken, PositionMs: ptr(630_000), PlayClockMs: 60_000}, nil); w.Code != 204 {
		t.Fatal(w.Body)
	}
	call(t, th.s, "POST", "/v1/profiles/"+replacement.ID+"/select", th.owner.AccessToken, ProfileSelect{}, &selected)
	if got := get[Item](t, th.s, selected.AccessToken, "/v1/items/"+th.film).State.PositionMs; got != 0 {
		t.Fatalf("a replacement profile inherited an old play: %d", got)
	}
}

// A fresh sign-in and an age of zero cannot revive an old play's progress token.
func TestFinalPositionTokenExpires(t *testing.T) {
	th := newTheatre(t)
	th.clock.Add(-time.Hour)
	playing := th.play(th.owner.AccessToken, th.film)
	th.report(th.owner.AccessToken, playing.ID, "paused", 600_000)
	th.clock.Add(db.MaxEditAge + time.Millisecond)
	th.owner = onMain(t, th.s, signIn(t, th.s, 0))
	if w := call(t, th.s, "DELETE", "/v1/playback/sessions/"+playing.ID, th.owner.AccessToken,
		PlaybackStop{ProgressToken: playing.ProgressToken, PositionMs: ptr(630_000)}, nil); w.Code != 404 {
		t.Fatalf("an expired play token was accepted: %d %s", w.Code, w.Body)
	}
	if got := th.state(th.film).PositionMS; got != 600_000 {
		t.Fatalf("the expired play changed progress: %d", got)
	}
}
