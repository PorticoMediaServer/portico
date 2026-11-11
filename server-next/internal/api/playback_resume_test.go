package api

import "testing"

// A film resumes where the viewer stopped, less the profile's "Rewind when resuming", which
// is nothing until they choose one.
func TestAFilmResumesALittleEarlierWhenThePersonAsksForThat(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	session := th.play(token, th.film)
	th.report(token, session.ID, "playing", 600_000)
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, nil); code != 204 {
		t.Fatalf("stop answered %d", code)
	}
	if resume := th.options(token, th.film, "").Resume; resume == nil || resume.PositionMs != 600_000 {
		t.Fatalf("with no rewind chosen the film resumes at %+v", resume)
	}
	edit := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{"playback.resumeRewindMs": 15000}}
	if code := status(t, th.s, "PATCH", "/v1/preferences", token, edit); code != 200 {
		t.Fatalf("saving the rewind answered %d", code)
	}
	if resume := th.options(token, th.film, "").Resume; resume == nil || resume.PositionMs != 585_000 {
		t.Fatalf("with 15 seconds chosen the film resumes at %+v", resume)
	}
}
