package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

const (
	atHome = "127.0.0.1"
	away   = "203.0.113.9"
)

// noLimits is an account held to nothing, as every account starts.
func noLimits() MemberLimits {
	return MemberLimits{AllowWatchTogether: true, AllowTokens: true}
}

// sent sends one request from an address.
func sent(t *testing.T, s *Server, peer, method, path, token string, body, out any) *httptest.ResponseRecorder {
	t.Helper()
	r := newRequest(method, path, token, body)
	r.RemoteAddr = peer + ":4000"
	w := serve(s, r)
	if out != nil && w.Code == 200 {
		if err := jsonInto(w, out); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// hold sets everything a member's account is held to, as the owner.
func hold(t *testing.T, s *Server, owner, member string, limits MemberLimits) Member {
	t.Helper()
	var out Member
	if w := call(t, s, "PATCH", "/v1/admin/members/"+member, owner, map[string]any{"limits": limits}, &out); w.Code != 200 {
		t.Fatalf("setting a member's limits answered %d: %s", w.Code, w.Body)
	}
	return out
}

// twoMembers is a server with its owner on the main profile and two members, max and eve.
// Tests hold max to a limit and leave eve alone.
func twoMembers(t *testing.T) (s *Server, owner string, max, eve Member) {
	t.Helper()
	s = newTestServer(t)
	owner = onMainProfile(t, s).AccessToken
	return s, owner, addMember(t, s, owner, "max", "member"), addMember(t, s, owner, "eve", "member")
}

// An account held to the home network cannot sign in, renew, choose a profile or use a
// token away from home, and does all four at home; its sign-in is kept for when it is back.
// The same holds outside its viewing hours. An account held to nothing does all of it from
// anywhere, and nobody lifts their own limits.
func TestAnAccountHeldToHomeAndToViewingHours(t *testing.T) {
	s, owner, max, _ := twoMembers(t)
	signInFrom := func(peer, username string, n int) *httptest.ResponseRecorder {
		return sent(t, s, peer, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: username, Password: testPassword, Device: device(n)}, nil)
	}
	renewFrom := func(peer, refresh, requestID string) (*httptest.ResponseRecorder, Session) {
		var out Session
		w := sent(t, s, peer, "POST", "/v1/auth/refresh", "", RefreshRequest{RefreshToken: refresh, InstallationID: device(2).InstallationID, RequestID: requestID}, &out)
		return w, out
	}
	held := signInAs(t, s, "max", testPassword, 2)
	var token KeyCreated
	if w := call(t, s, "POST", "/v1/me/tokens", held.AccessToken, KeyCreate{Name: "Script", Scopes: []string{"read"}}, &token); w.Code != 200 {
		t.Fatalf("making a token answered %d: %s", w.Code, w.Body)
	}
	var profiles Page[Profile]
	call(t, s, "GET", "/v1/profiles", held.AccessToken, nil, &profiles)
	choose := "/v1/profiles/" + profiles.Items[0].ID + "/select"

	limits := noLimits()
	limits.HomeOnly = true
	if got := hold(t, s, owner, max.ID, limits); !got.Limits.HomeOnly {
		t.Fatalf("the member reads back as %+v", got.Limits)
	}
	if w := signInFrom(away, "max", 3); w.Code != 403 || !strings.Contains(w.Body.String(), "away_from_home") {
		t.Errorf("a home-only account signing in away answered %d %s", w.Code, w.Body)
	}
	if w := signInFrom(atHome, "max", 3); w.Code != 200 {
		t.Errorf("a home-only account signing in at home answered %d %s", w.Code, w.Body)
	}
	if w := signInFrom(away, "eve", 4); w.Code != 200 {
		t.Errorf("an account with no limits signing in away answered %d %s", w.Code, w.Body)
	}
	// The change of limits made max's device ask for a new token. Away it gets none, and the
	// very same renewal works once it is home.
	if w, _ := renewFrom(away, held.RefreshToken, "r1"); w.Code != 403 || !strings.Contains(w.Body.String(), "away_from_home") {
		t.Fatalf("a home-only account renewing away answered %d %s", w.Code, w.Body)
	}
	w, renewed := renewFrom(atHome, held.RefreshToken, "r1")
	if w.Code != 200 {
		t.Fatalf("the same renewal at home answered %d %s", w.Code, w.Body)
	}
	if code := status(t, s, "PATCH", "/v1/admin/members/"+max.ID, renewed.AccessToken, map[string]any{"limits": noLimits()}); code != 403 {
		t.Errorf("a member lifting their own limits answered %d, want 403", code)
	}
	if w := sent(t, s, away, "POST", choose, renewed.AccessToken, ProfileSelect{}, nil); w.Code != 403 {
		t.Errorf("a home-only account choosing a profile away answered %d", w.Code)
	}
	if w := sent(t, s, atHome, "POST", choose, renewed.AccessToken, ProfileSelect{}, nil); w.Code != 200 {
		t.Errorf("a home-only account choosing a profile at home answered %d %s", w.Code, w.Body)
	}
	if w := sent(t, s, away, "GET", "/v1/me", token.Secret, nil, nil); w.Code != 403 || !strings.Contains(w.Body.String(), "away_from_home") {
		t.Errorf("a home-only account's token away answered %d %s", w.Code, w.Body)
	}
	if w := sent(t, s, atHome, "GET", "/v1/me", token.Secret, nil, nil); w.Code != 200 {
		t.Errorf("a home-only account's token at home answered %d %s", w.Code, w.Body)
	}

	for name, bad := range map[string]string{
		"hours in an unknown timezone": `{"viewingHours":{"timezone":"Mars/Olympus","windows":[{"days":[1],"startMinute":0,"endMinute":60}]}}`,
		"hours with no timezone":       `{"viewingHours":{"windows":[{"days":[1],"startMinute":0,"endMinute":60}]}}`,
		"a rating of 40":               `{"maxRatingAge":40}`,
		"no profiles at all":           `{"maxProfiles":0}`,
		"a cap of nothing":             `{"remoteMaxBitrateKbps":0}`,
		"a label nobody has":           `{"blockedLabels":["tag_nothing"]}`,
	} {
		if code := status(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner, json.RawMessage(`{"limits":`+bad+`}`)); code != 400 {
			t.Errorf("%s answered %d, want 400", name, code)
		}
	}

	// Viewing hours: tomorrow only, all day. Today everything is refused; tomorrow it works.
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	limits = noLimits()
	limits.ViewingHours = &auth.ViewingHours{Timezone: "UTC", Windows: []auth.ViewingWindow{{Days: []int{int(now.Weekday()+1) % 7}, StartMinute: 0, EndMinute: 1440}}}
	if got := hold(t, s, owner, max.ID, limits); got.Limits.ViewingHours == nil || got.Limits.HomeOnly {
		t.Fatalf("the member reads back as %+v", got.Limits)
	}
	if w := signInFrom(away, "max", 5); w.Code != 403 || !strings.Contains(w.Body.String(), "outside_viewing_hours") {
		t.Errorf("signing in outside viewing hours answered %d %s", w.Code, w.Body)
	}
	if w, _ := renewFrom(away, renewed.RefreshToken, "r2"); w.Code != 403 || !strings.Contains(w.Body.String(), "outside_viewing_hours") {
		t.Errorf("renewing outside viewing hours answered %d %s", w.Code, w.Body)
	}
	if w := sent(t, s, away, "GET", "/v1/me", token.Secret, nil, nil); w.Code != 403 {
		t.Errorf("a token outside viewing hours answered %d", w.Code)
	}
	if w := signInFrom(away, "eve", 4); w.Code != 200 {
		t.Errorf("an account with no hours signing in answered %d", w.Code)
	}
	now = now.Add(24 * time.Hour)
	if w, _ := renewFrom(away, renewed.RefreshToken, "r2"); w.Code != 200 {
		t.Errorf("the same renewal inside viewing hours answered %d %s", w.Code, w.Body)
	}
	if w := signInFrom(away, "max", 5); w.Code != 200 {
		t.Errorf("signing in inside viewing hours answered %d %s", w.Code, w.Body)
	}
}

// The owner holds a member's whole account to a rating and keeps a label from it. The
// member is shown less at once, cannot lift it from their own main profile, and sees where
// the account is the stricter. Another member is shown everything throughout.
func TestAnAccountHeldToARatingAndBlockedLabels(t *testing.T) {
	s, owner, max, _ := twoMembers(t)
	ctx := context.Background()
	if _, err := seed.Import(ctx, s.db, seed.Config{Seed: 3, Items: 600}, nil); err != nil {
		t.Fatal(err)
	}
	held, free := signInAs(t, s, "max", testPassword, 2), signInAs(t, s, "eve", testPassword, 3)
	films := "/v1/items?library=" + get[Libraries](t, s, owner, "/v1/libraries").Items[0].ID + "&kind=movie&total=true"
	// A change of limits makes the account's devices take a new token.
	renewals := 0
	shown := func(session *Session, n int) int {
		t.Helper()
		if status(t, s, "GET", films, session.AccessToken, nil) == 401 {
			renewals++
			code, next := renew(t, s, session.RefreshToken, n, "limits-"+string(rune('a'+renewals)))
			if code != 200 {
				t.Fatalf("renewing after a change of limits answered %d", code)
			}
			*session = next
		}
		return *get[ItemPage](t, s, session.AccessToken, films).Total
	}
	all := shown(&held, 2)

	limits := noLimits()
	limits.MaxRatingAge = new(13)
	hold(t, s, owner, max.ID, limits)
	rated := shown(&held, 2)
	if rated == 0 || rated >= all {
		t.Fatalf("held to 13 the member is shown %d films of %d", rated, all)
	}
	if got := shown(&free, 3); got != all {
		t.Errorf("a member held to nothing is shown %d films of %d", got, all)
	}
	// The member's own main profile says "no limit", and the account's still binds.
	var profiles Page[Profile]
	call(t, s, "GET", "/v1/profiles", held.AccessToken, nil, &profiles)
	var own Restrictions
	if w := call(t, s, "PUT", "/v1/profiles/"+profiles.Items[0].ID+"/restrictions", held.AccessToken, Restrictions{AllLibraries: true, AllowUnrated: true}, &own); w.Code != 200 ||
		own.MaxRatingAge != nil || own.AccountMaxRatingAge == nil || *own.AccountMaxRatingAge != 13 || own.AccountAllowUnrated {
		t.Fatalf("the member's own limits answered %d %+v", w.Code, own)
	}
	if got := shown(&held, 2); got != rated {
		t.Errorf("after lifting their own profile's limit the member is shown %d films, want the account's %d", got, rated)
	}
	limits.AllowUnrated = true
	hold(t, s, owner, max.ID, limits)
	if got := shown(&held, 2); got <= rated || got >= all {
		t.Errorf("with unrated titles allowed the member is shown %d films, want between %d and %d", got, rated, all)
	}

	labels, err := s.db.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) == 0 {
		t.Fatalf("the seeded library has no labels: %v", err)
	}
	limits = noLimits()
	limits.BlockedLabels = []string{labels[0].PublicID}
	if got := hold(t, s, owner, max.ID, limits); len(got.Limits.BlockedLabels) != 1 || got.Limits.MaxRatingAge != nil {
		t.Fatalf("the member reads back as %+v", got.Limits)
	}
	if got := shown(&held, 2); got >= all {
		t.Errorf("with a label kept from the account the member is shown %d films of %d", got, all)
	}
	hold(t, s, owner, max.ID, noLimits())
	if got := shown(&held, 2); got != all {
		t.Errorf("held to nothing again the member is shown %d films of %d", got, all)
	}
}

// An account held to two profiles gets no third; one with tokens or Watch Together
// turned off gets neither. Another member gets all of it.
func TestAnAccountHeldToFewerProfilesAndSwitchedOff(t *testing.T) {
	s, owner, max, _ := twoMembers(t)
	limits := MemberLimits{MaxProfiles: new(2)}
	hold(t, s, owner, max.ID, limits)
	held, free := signInAs(t, s, "max", testPassword, 2), signInAs(t, s, "eve", testPassword, 3)
	for name, c := range map[string]struct {
		session Session
		want    []int
	}{"held to two": {held, []int{200, 409}}, "held to nothing": {free, []int{200, 200}}} {
		for n, want := range c.want {
			if code := status(t, s, "POST", "/v1/profiles", c.session.AccessToken, ProfileCreate{Name: "Extra " + string(rune('A'+n))}); code != want {
				t.Errorf("%s, another profile (%d): answered %d, want %d", name, n+1, code, want)
			}
		}
	}
	// The owner adding one for the member is held to the same number.
	if code := status(t, s, "POST", "/v1/profiles?member="+max.ID, owner, ProfileCreate{Name: "Third"}); code != 409 {
		t.Errorf("the owner adding a third profile to an account held to two answered %d", code)
	}

	token := KeyCreate{Name: "Script", Scopes: []string{"read"}}
	if code := status(t, s, "POST", "/v1/me/tokens", held.AccessToken, token); code == 200 {
		t.Errorf("an account with tokens off made a token")
	}
	if code := status(t, s, "POST", "/v1/me/tokens", free.AccessToken, token); code != 200 {
		t.Errorf("an account with tokens on making a token answered %d", code)
	}
	for name, c := range map[string]struct {
		session Session
		want    bool
	}{"switched off": {held, false}, "held to nothing": {free, true}} {
		caller := CallerOf(t, s, c.session.AccessToken)
		if got, err := s.db.GroupMemberOf(context.Background(), caller.ProfileID); err != nil || got.Allowed != c.want {
			t.Errorf("%s: Watch Together allowed is %v (%v), want %v", name, got.Allowed, err, c.want)
		}
	}
}

// An edit of limits changes only the fields it names: holding an account to home leaves its
// Watch Together and tokens as they were, and null lifts one limit.
func TestALimitsEditChangesOnlyWhatItNames(t *testing.T) {
	s, owner, max, _ := twoMembers(t)
	edit := func(limits string) MemberLimits {
		t.Helper()
		var out Member
		if w := call(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner, json.RawMessage(`{"limits":`+limits+`}`), &out); w.Code != 200 {
			t.Fatalf("editing limits %s answered %d: %s", limits, w.Code, w.Body)
		}
		return out.Limits
	}
	got := edit(`{"homeOnly":true,"maxRatingAge":13,"maxProfiles":2}`)
	if !got.HomeOnly || !got.AllowWatchTogether || !got.AllowTokens || got.MaxRatingAge == nil {
		t.Fatalf("holding the account to home and to 13 left it as %+v", got)
	}
	got = edit(`{"maxRatingAge":null,"allowWatchTogether":false}`)
	if !got.HomeOnly || got.MaxRatingAge != nil || got.AllowWatchTogether || got.MaxProfiles == nil || *got.MaxProfiles != 2 || !got.AllowTokens {
		t.Fatalf("lifting the rating and turning Watch Together off left it as %+v", got)
	}
	hours := `{"viewingHours":{"timezone":"America/Halifax","windows":[{"days":[1,2],"startMinute":480,"endMinute":1200}]}}`
	if got = edit(hours); got.ViewingHours == nil || got.ViewingHours.Timezone != "America/Halifax" || !got.HomeOnly {
		t.Fatalf("setting hours left it as %+v", got)
	}
	if got = edit(`{"viewingHours":null}`); got.ViewingHours != nil || !got.HomeOnly {
		t.Fatalf("lifting the hours left it as %+v", got)
	}
}
