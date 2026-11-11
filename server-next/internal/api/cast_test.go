package api

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
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

// pairCast signs a Cast receiver in for the owner's main profile on a Portico Accounts server.
func pairCast(t *testing.T, th *theatre) SignInResponse {
	t.Helper()
	ctx := context.Background()
	th.s.db.SaveSetting(ctx, "sign_in_kind", "portico_account", th.clock)
	th.s.db.SaveSetting(ctx, castApplicationSetting, "ABCD1234", th.clock)
	var code CastCode
	call(t, th.s, "POST", "/v1/cast/codes", th.owner.AccessToken, nil, &code)
	var paired SignInResponse
	if w := call(t, th.s, "POST", "/v1/cast/redeem", "", CastRedeem{Code: code.Code, Device: device(7)}, &paired); w.Code != 200 {
		t.Fatalf("pairing answered %d %s", w.Code, w.Body)
	}
	return paired
}

// A Cast receiver's sign-in reaches every route the server has, and is refused on all of them
// but the short list a receiver needs to play what it was sent. On that list it still reaches
// only its own session, its own queue and its own capabilities.
func TestCastReachesOnlyItsOwnPlayback(t *testing.T) {
	th := newTheatre(t)
	phone := th.owner.AccessToken
	tv := pairCast(t, th).Session.AccessToken
	fill := strings.NewReplacer("{id}", "x1", "{entryId}", "x2", "{requestId}", "x3", "{imageId}", "x4", "{grant}", "x5")
	for _, route := range th.s.Routes() {
		if route.Access == Public || castRoutes[route.Pattern] {
			continue
		}
		method, path, _ := strings.Cut(route.Pattern, " ")
		path = fill.Replace(path)
		if i := strings.Index(path, "{"); i >= 0 {
			path = path[:i] + "x"
		}
		var refused errorBody
		if w := call(t, th.s, method, path, tv, nil, &refused); w.Code != 403 || refused.Error.Code != "playback_only" {
			t.Errorf("a Cast receiver's %s answered %d %s, want 403 playback_only", route.Pattern, w.Code, refused.Error.Code)
		}
	}

	// On the list: its own things only.
	playing := th.play(phone, th.film)
	var phoneQueue QueueCreated
	call(t, th.s, "POST", "/v1/queues", phone, QueueCreate{Source: playqueue.QueueSource{Items: th.eps[:]}}, &phoneQueue)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/v1/devices/" + th.owner.DeviceID + "/capabilities", json.RawMessage(`{"players":[]}`)},
		{"GET", "/v1/devices/" + th.owner.DeviceID + "/capabilities", nil},
		{"DELETE", "/v1/playback/sessions/" + playing.ID, nil},
		{"GET", "/v1/playback/sessions/" + playing.ID, nil},
		{"GET", "/v1/queues/" + phoneQueue.Queue.ID, nil},
		{"POST", "/v1/queues/" + phoneQueue.Queue.ID + "/advance", nil},
		{"POST", "/v1/playback/sessions", PlaybackStart{Queue: &playback.QueueLink{QueueID: phoneQueue.Queue.ID, EntryID: phoneQueue.Queue.Current.EntryID}}},
	} {
		if code := status(t, th.s, c.method, c.path, tv, c.body); code != 403 {
			t.Errorf("a Cast receiver's %s %s on another device answered %d, want 403", c.method, c.path, code)
		}
	}
	if th.report(phone, playing.ID, "playing", 1000) != 200 {
		t.Fatal("the receiver stopped the phone's stream")
	}
	if code := status(t, th.s, "PUT", "/v1/devices/current/capabilities", tv, json.RawMessage(`{"players":[]}`)); code != 204 {
		t.Errorf("the receiver publishing its own capabilities answered %d", code)
	}
	var own QueueCreated
	if w := call(t, th.s, "POST", "/v1/queues", tv, QueueCreate{Source: playqueue.QueueSource{Items: th.eps[:]}}, &own); w.Code != 201 {
		t.Fatalf("the receiver making its queue answered %d", w.Code)
	}
	code, session := th.start(tv, "", PlaybackStart{Queue: &playback.QueueLink{QueueID: own.Queue.ID, EntryID: own.Queue.Current.EntryID}})
	if code != 201 || th.report(tv, session.ID, "playing", 1000) != 200 {
		t.Fatalf("the receiver playing its own queue answered %d", code)
	}
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, tv, nil); code != 204 {
		t.Errorf("the receiver stopping its own session answered %d", code)
	}
	if code := status(t, th.s, "DELETE", "/v1/auth/session", tv, nil); code != 204 {
		t.Errorf("the receiver signing itself out answered %d", code)
	}
}
