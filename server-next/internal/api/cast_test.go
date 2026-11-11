package api

import (
	"context"
	"slices"
	"testing"
	"time"
)

// A local server has no Google Cast: nothing answers and the server does not say it has it.
// With Portico Accounts, a phone's pairing code signs a receiver in on the phone's profile,
// once; the receiver can play and nothing else, and its sign-in lasts a day unused.
func TestCastOnlyWithPorticoAccounts(t *testing.T) {
	th := newTheatre(t)
	phone := th.owner.AccessToken
	receiver := device(7)
	var system System
	call(t, th.s, "GET", "/v1/system", "", nil, &system)
	if slices.Contains(system.Capabilities, "cast") {
		t.Fatal("a local server says it has Google Cast")
	}
	for _, c := range []struct{ method, path, token string }{{"GET", "/v1/cast/config", phone}, {"POST", "/v1/cast/codes", phone},
		{"POST", "/v1/cast/redeem", ""}} {
		var body any
		if c.path == "/v1/cast/redeem" {
			body = CastRedeem{Code: "AAAA-AAAA", Device: receiver}
		}
		if code := status(t, th.s, c.method, c.path, c.token, body); code != 404 {
			t.Errorf("%s %s on a local server answered %d, want 404", c.method, c.path, code)
		}
	}

	ctx := context.Background()
	th.s.db.SaveSetting(ctx, "sign_in_kind", "portico_account", th.clock)
	th.s.db.SaveSetting(ctx, castApplicationSetting, "ABCD1234", th.clock)
	call(t, th.s, "GET", "/v1/system", "", nil, &system)
	var config CastConfig
	if call(t, th.s, "GET", "/v1/cast/config", phone, nil, &config); config.ApplicationID != "ABCD1234" || !slices.Contains(system.Capabilities, "cast") {
		t.Fatalf("a Portico Accounts server's Cast: %+v %v", config, system.Capabilities)
	}
	var code CastCode
	if w := call(t, th.s, "POST", "/v1/cast/codes", phone, nil, &code); w.Code != 200 || code.Code == "" {
		t.Fatalf("a pairing code answered %d %s", w.Code, w.Body)
	}
	var paired SignInResponse
	if w := call(t, th.s, "POST", "/v1/cast/redeem", "", CastRedeem{Code: code.Code, Device: receiver}, &paired); w.Code != 200 ||
		paired.Session.Viewer.Role != "cast" || paired.Account.Username != "" || len(paired.Profiles) != 0 {
		t.Fatalf("redeeming answered %d %s", w.Code, w.Body)
	}
	if again := status(t, th.s, "POST", "/v1/cast/redeem", "", CastRedeem{Code: code.Code, Device: device(8)}); again != 404 {
		t.Fatalf("a used code answered %d", again)
	}
	tv := paired.Session.AccessToken
	if code, _ := th.start(tv, "", PlaybackStart{ItemID: th.film}); code != 201 {
		t.Fatalf("the receiver could not play: %d", code)
	}
	for _, c := range []struct{ method, path string }{{"GET", "/v1/libraries"}, {"GET", "/v1/preferences?deviceClass=television"},
		{"POST", "/v1/cast/codes"}, {"GET", "/v1/profiles"}} {
		if got := status(t, th.s, c.method, c.path, tv, nil); got != 403 {
			t.Errorf("the receiver's %s %s answered %d, want 403", c.method, c.path, got)
		}
	}
	th.clock = th.clock.Add(23 * time.Hour)
	_, renewed := renew(t, th.s, paired.Session.RefreshToken, 7, "cast-renew")
	if want := th.clock.Add(24 * time.Hour); !renewed.SessionExpiresAt.Equal(want.UTC().Truncate(time.Millisecond)) {
		t.Fatalf("a renewed receiver's sign-in lasts until %s, want a day from now", renewed.SessionExpiresAt)
	}
}
