package api

import "testing"

// "Rewind on resume" starts a film that much before where it stopped, in what playback offers
// and in the stream it starts; a film never started still starts at the beginning.
func TestRewindOnResume(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	stop := int64(30 * 60_000)
	session := th.play(token, th.film)
	status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: &stop})

	body := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{"playback.resumeRewindMs": 10000}}
	if w := call(t, th.s, "PATCH", "/v1/preferences", token, body, nil); w.Code != 200 {
		t.Fatalf("saving Rewind on resume answered %d: %s", w.Code, w.Body)
	}
	if resume := th.options(token, th.film, "").Resume; resume == nil || resume.PositionMs != stop-10000 {
		t.Fatalf("playback offers to resume at %+v", resume)
	}
	if started := th.play(token, th.film); started.Presentation.StartPositionMs != stop-10000 {
		t.Fatalf("the stream starts at %d", started.Presentation.StartPositionMs)
	}
	if resume := th.options(token, th.eps[0], "").Resume; resume != nil {
		t.Fatalf("an episode never started offers to resume at %d", resume.PositionMs)
	}
}
