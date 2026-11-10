package api

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func patchPreferences(t *testing.T, s *Server, token, scope string, values map[string]any) (int, Preferences) {
	t.Helper()
	var out Preferences
	w := call(t, s, "PATCH", "/v1/preferences", token, PreferencesEdit{Scope: scope, DeviceClass: "web", Values: values}, nil)
	if w.Code == 200 {
		if err := jsonInto(w, &out); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, out
}

// An app starts by reading every setting with its default. What a profile then saves holds
// on the devices it was saved for and nowhere else, the narrowest saved value wins, a null
// goes back to the wider value, and a value the setting does not take is refused.
func TestPreferences(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	token := owner.AccessToken
	first := get[Preferences](t, s, token, "/v1/preferences?deviceClass=web")
	if len(first.Registry.Fields) < 50 || first.Registry.Revision == "" || len(first.Documents) != 3 || !first.DeviceBound {
		t.Fatalf("the first answer has %d settings, revision %q, %d documents", len(first.Registry.Fields), first.Registry.Revision, len(first.Documents))
	}
	for _, f := range first.Registry.Fields {
		if _, ok := first.Effective[f.Key]; !ok || first.EffectiveSource[f.Key] != "default" {
			t.Fatalf("%s has no value in force, or one that is not the default", f.Key)
		}
	}
	if first.Effective["notifications.badges"] != true || first.Effective["region.locale"] != "auto" {
		t.Fatalf("defaults: %v %v", first.Effective["notifications.badges"], first.Effective["region.locale"])
	}

	if code, _ := patchPreferences(t, s, token, "profile-server", map[string]any{"playback.autoplayNext": false, "region.locale": "en-gb"}); code != 200 {
		t.Fatalf("saving for the profile answered %d", code)
	}
	if code, _ := patchPreferences(t, s, token, "profile-device-class", map[string]any{"appearance.cardSizePercent": 120}); code != 200 {
		t.Fatalf("saving for this kind of device answered %d", code)
	}
	code, saved := patchPreferences(t, s, token, "profile-device", map[string]any{"appearance.textSize": "large"})
	if code != 200 || saved.Effective["playback.autoplayNext"] != false || saved.Effective["region.locale"] != "en-GB" ||
		saved.Effective["appearance.cardSizePercent"] != 120.0 || saved.Effective["appearance.textSize"] != "large" ||
		saved.EffectiveSource["appearance.textSize"] != "profile-device" || saved.EffectiveSource["appearance.cardSizePercent"] != "profile-device-class" {
		t.Fatalf("after three saves: %d %v %v", code, saved.Effective["appearance.cardSizePercent"], saved.EffectiveSource)
	}
	// Another device of the same profile has the profile's settings, not this device's, and
	// a television does not have what was saved for the web.
	other := onMain(t, s, signIn(t, s, 5))
	tv := get[Preferences](t, s, other.AccessToken, "/v1/preferences?deviceClass=television")
	if tv.Effective["playback.autoplayNext"] != false || tv.Effective["appearance.textSize"] != "standard" || tv.Effective["appearance.cardSizePercent"] != 100.0 {
		t.Fatalf("another device: %v %v %v", tv.Effective["playback.autoplayNext"], tv.Effective["appearance.textSize"], tv.Effective["appearance.cardSizePercent"])
	}
	// The profile's other open apps are told to read their settings again.
	claims, _ := s.tokens.CheckAccess(token, time.Now())
	events, _, _, err := s.db.Events(context.Background(), db.Viewer{UserID: claims.UserID, ProfileID: claims.ProfileID}, 0, 100)
	if err != nil || len(events) == 0 || events[len(events)-1].Topic != db.TopicPreferences {
		t.Fatalf("the feed after a save: %+v, %v", events, err)
	}

	if code, cleared := patchPreferences(t, s, token, "profile-server", map[string]any{"playback.autoplayNext": nil}); code != 200 ||
		cleared.Effective["playback.autoplayNext"] != true || cleared.EffectiveSource["playback.autoplayNext"] != "default" {
		t.Fatalf("clearing a setting: %d %v", code, cleared.Effective["playback.autoplayNext"])
	}
	for name, c := range map[string]struct {
		scope  string
		values map[string]any
	}{
		"a setting this server does not have": {"profile-server", map[string]any{"playback.madeUp": true}},
		"a setting saved at the wrong scope":  {"profile-server", map[string]any{"appearance.textSize": "large"}},
		"a value outside the list":            {"profile-server", map[string]any{"playback.subtitleMode": "sometimes"}},
		"a number outside the range":          {"profile-device-class", map[string]any{"appearance.cardSizePercent": 500}},
		"a scope that does not exist":         {"everyone", map[string]any{"playback.autoplayNext": false}},
	} {
		if code, _ := patchPreferences(t, s, token, c.scope, c.values); code != 400 {
			t.Errorf("%s answered %d, want 400", name, code)
		}
	}
	if w := call(t, s, "GET", "/v1/preferences", token, nil, nil); w.Code != 400 {
		t.Errorf("reading without saying the kind of device answered %d", w.Code)
	}
}

// When the owner changes what a member may see, or the member's role, every profile of that
// member gets a new access marker and its open apps are told on the feed, so they throw
// their cache away without asking again and again.
func TestAccessChangesMoveTheMarkerAndReachTheFeed(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	max := addMember(t, s, owner.AccessToken, "max", "member")
	member := signInAs(t, s, "max", testPassword, 3)
	marker := func() string {
		return get[Me](t, s, signInAs(t, s, "max", testPassword, 3).AccessToken, "/v1/me").Profile.AccessMarker
	}
	before := marker()
	claims, _ := s.tokens.CheckAccess(member.AccessToken, time.Now())
	viewer := db.Viewer{UserID: claims.UserID, ProfileID: claims.ProfileID}
	head, _ := s.db.ChangesHead(context.Background())

	none, role := false, "admin"
	for n, edit := range []MemberEdit{{AllLibraries: &none, Libraries: &[]string{}}, {Role: &role}} {
		if w := call(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner.AccessToken, edit, nil); w.Code != 200 {
			t.Fatalf("edit %d answered %d: %s", n, w.Code, w.Body)
		}
		events, next, _, err := s.db.Events(context.Background(), viewer, head, 100)
		if err != nil || len(events) != 1 || events[0].Topic != db.TopicAccess {
			t.Fatalf("edit %d: the member's feed said %+v, %v", n, events, err)
		}
		head = next
		after := marker()
		if after == "" || after == before {
			t.Fatalf("edit %d left the access marker at %q", n, after)
		}
		before = after
	}
	// A change that is not about access leaves the marker alone, and nobody else's moves.
	notes := "likes westerns"
	call(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner.AccessToken, MemberEdit{Notes: &notes}, nil)
	if after := marker(); after != before {
		t.Errorf("a note moved the access marker from %q to %q", before, after)
	}
	ownerClaims, _ := s.tokens.CheckAccess(owner.AccessToken, time.Now())
	if events, _, _, _ := s.db.Events(context.Background(), db.Viewer{UserID: ownerClaims.UserID, ProfileID: ownerClaims.ProfileID}, 0, 100); len(events) != 0 {
		t.Errorf("the owner's feed heard of the member's access: %+v", events)
	}
}

// A token that is no longer good is answered 401 at an address this server does not have,
// so an app learns to renew; a stranger and a good token are both told 404.
func TestABadTokenIsToldSoAtAnUnknownAddress(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	old := owner.AccessToken
	s.now = func() time.Time { return time.Now().Add(16 * time.Minute) }
	for token, want := range map[string]int{"": 404, "made-up": 401, old: 401} {
		for _, method := range []string{"GET", "POST"} {
			if got := status(t, s, method, "/v1/nothing/here", token, nil); got != want {
				t.Errorf("%s with the token %.8q answered %d, want %d", method, token, got, want)
			}
		}
	}
	s.now = time.Now
	if got := status(t, s, "GET", "/v1/nothing/here", old, nil); got != 404 {
		t.Errorf("a good token at an unknown address answered %d, want 404", got)
	}
	if got := status(t, s, "GET", "/elsewhere", "made-up", nil); got != 404 {
		t.Errorf("an address outside the API answered %d, want 404", got)
	}
}

// Whether two-step sign-in is on is part of how the account is secured: only the main
// profile is told, like the rest of managing the account.
func TestTwoStepStateNeedsTheMainProfile(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	var kid Profile
	if w := call(t, s, "POST", "/v1/profiles", owner.AccessToken, ProfileCreate{Name: "Kid"}, &kid); w.Code != 200 {
		t.Fatalf("adding a profile answered %d", w.Code)
	}
	in := signIn(t, s, 4)
	if got := status(t, s, "GET", "/v1/me/two-factor", in.Session.AccessToken, nil); got != 403 {
		t.Errorf("a sign-in still at the profile chooser was answered %d, want 403", got)
	}
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", in.Session.AccessToken, ProfileSelect{}, &onKid)
	if got := status(t, s, "GET", "/v1/me/two-factor", onKid.AccessToken, nil); got != 403 {
		t.Errorf("another profile was answered %d, want 403", got)
	}
	if got := status(t, s, "GET", "/v1/me/two-factor", owner.AccessToken, nil); got != 200 {
		t.Errorf("the main profile was answered %d, want 200", got)
	}
}
