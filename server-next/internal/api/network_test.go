package api

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// from sends a request from one connection address, with a forwarded header if given.
func from(t *testing.T, s *Server, peer, forwarded, method, path, token string, body, out any) int {
	t.Helper()
	r := newRequest(method, path, token, body)
	r.RemoteAddr = peer + ":4000"
	if forwarded != "" {
		r.Header.Set("X-Forwarded-For", forwarded)
	}
	w := serve(s, r)
	if out != nil && w.Code == 200 {
		if err := jsonInto(w, out); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code
}

// Behind a reverse proxy every connection comes from the proxy. Once the owner lists the
// proxy, each person is counted by their own address: one person's wrong guesses block
// that person's network and nobody else's, and a forwarded address from anywhere else is
// ignored, so nobody can claim to be at home.
func TestTrustedProxiesAndHomeNetworks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	// Setup works from any network: this one is not home by anyone's definition.
	owner := onMainProfile(t, s)
	const proxy, guesser, friend = "10.9.9.9", "198.51.100.7", "198.51.100.8"

	var settings NetworkSettings
	if w := call(t, s, "GET", "/v1/admin/settings/network", owner.AccessToken, nil, &settings); w.Code != 200 || !settings.HomeNetworksDefault ||
		len(settings.TrustedProxies) != 0 || w.Header().Get("ETag") != `"0"` {
		t.Fatalf("the first settings: %d %+v %q", w.Code, settings, w.Header().Get("ETag"))
	}
	for name, edit := range map[string]NetworkSettingsEdit{
		"a proxy that is not an address": {TrustedProxies: []string{"my-proxy"}},
		"the whole internet as a proxy":  {TrustedProxies: []string{"0.0.0.0/0"}},
		"a home network that is not one": {HomeNetworks: &[]string{"192.168.1.0/40"}, TrustedProxies: []string{}},
	} {
		if got := status(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, edit); got != 400 {
			t.Errorf("%s answered %d, want 400", name, got)
		}
	}
	save := func(etag string, edit NetworkSettingsEdit) int {
		r := newRequest("PUT", "/v1/admin/settings/network", owner.AccessToken, edit)
		if etag != "" {
			r.Header.Set("If-Match", etag)
		}
		return serve(s, r).Code
	}
	edit := NetworkSettingsEdit{HomeNetworks: &[]string{"192.168.1.0/24"}, TrustedProxies: []string{proxy}}
	if got := save(`"0"`, edit); got != 200 {
		t.Fatalf("saving the settings answered %d", got)
	}
	if got := save(`"0"`, edit); got != http.StatusPreconditionFailed {
		t.Errorf("a save from a stale copy answered %d, want 412", got)
	}

	wrong := SignInRequest{Username: "justin", Password: "not the password"}
	for n := range 30 {
		wrong.Device = device(100 + n)
		// The wait for wrong passwords is not what is being tested: step past it.
		clock := time.Now().Add(time.Duration(n) * 2 * time.Minute)
		s.now = func() time.Time { return clock }
		if got := from(t, s, proxy, guesser, "POST", "/v1/auth/sign-in", "", wrong, nil); got != 401 {
			t.Fatalf("wrong password %d through the proxy answered %d", n, got)
		}
	}
	// Past the wait for wrong passwords, which is a minute at most; the block lasts fifteen.
	later := time.Now().Add(62 * time.Minute)
	s.now = func() time.Time { return later }
	right := SignInRequest{Username: "justin", Password: testPassword, Device: device(200)}
	if got := from(t, s, proxy, guesser, "POST", "/v1/auth/sign-in", "", right, nil); got != 429 {
		t.Errorf("the network that guessed 30 times was answered %d, want 429", got)
	}
	// It cannot get out of the block by naming another address in front of the proxy's entry.
	if got := from(t, s, proxy, "192.168.1.5, "+guesser, "POST", "/v1/auth/sign-in", "", right, nil); got != 429 {
		t.Errorf("the blocked network claiming a home address was answered %d, want 429", got)
	}
	var in SignInResponse
	if got := from(t, s, proxy, friend, "POST", "/v1/auth/sign-in", "", right, &in); got != 200 {
		t.Fatalf("someone else behind the same proxy was answered %d, want 200", got)
	}
	// The owner sees which network is blocked and lets it back in.
	owner = onMain(t, s, in)
	blocked := get[Page[BlockedNetwork]](t, s, owner.AccessToken, "/v1/admin/blocked-networks")
	if len(blocked.Items) != 1 || blocked.Items[0].Network != guesser || !blocked.Items[0].BlockedUntil.After(later) {
		t.Fatalf("the blocked networks are listed as %+v", blocked.Items)
	}
	if got := status(t, s, "DELETE", "/v1/admin/blocked-networks/"+blocked.Items[0].ID, owner.AccessToken, nil); got != 200 && got != 204 {
		t.Fatalf("unblocking answered %d", got)
	}
	unblocked := SignInRequest{Username: "justin", Password: testPassword, Device: device(203)}
	if got := from(t, s, proxy, guesser, "POST", "/v1/auth/sign-in", "", unblocked, nil); got != 200 {
		t.Errorf("the unblocked network was answered %d, want 200", got)
	}
	if left := get[Page[BlockedNetwork]](t, s, owner.AccessToken, "/v1/admin/blocked-networks"); len(left.Items) != 0 {
		t.Errorf("after unblocking the list still holds %+v", left.Items)
	}
	if got := status(t, s, "DELETE", "/v1/admin/blocked-networks/"+blocked.Items[0].ID, owner.AccessToken, nil); got != 404 {
		t.Errorf("unblocking a network that is not blocked answered %d, want 404", got)
	}
	// Home is what the owner listed, as the proxy reports it, and never what a stranger claims.
	atHome := SignInRequest{Username: "justin", Password: testPassword, Device: device(201)}
	if got := from(t, s, proxy, "192.168.1.5", "POST", "/v1/auth/sign-in", "", atHome, nil); got != 200 {
		t.Fatalf("a sign-in from home answered %d", got)
	}
	claimed := SignInRequest{Username: "justin", Password: testPassword, Device: device(202)}
	if got := from(t, s, "203.0.113.50", "192.168.1.5", "POST", "/v1/auth/sign-in", "", claimed, nil); got != 200 {
		t.Fatalf("a sign-in from elsewhere answered %d", got)
	}
	// A proxy on this machine that the owner has not listed: whoever comes through it is
	// not at home, whatever address the connection has.
	unlisted := SignInRequest{Username: "justin", Password: testPassword, Device: device(204)}
	if got := from(t, s, "127.0.0.1", "203.0.113.60", "POST", "/v1/auth/sign-in", "", unlisted, nil); got != 200 {
		t.Fatalf("a sign-in through an unlisted proxy answered %d", got)
	}
	s.now = time.Now
	owner = onMain(t, s, signIn(t, s, 0))
	networks := map[string]string{}
	for _, d := range get[Page[Device]](t, s, owner.AccessToken, "/v1/devices").Items {
		networks[d.Name] = d.Network
	}
	if networks["Device 200"] != "away" || networks["Device 201"] != "home" || networks["Device 202"] != "away" || networks["Device 204"] != "away" {
		t.Errorf("home and away in the device list: %v", networks)
	}

	// The settings are still there after a restart.
	closeDB(s.db)
	if got := openTestServer(t, path).networkSettings(0); len(got.TrustedProxies) != 1 || got.HomeNetworksDefault {
		t.Errorf("after a restart the settings are %+v", got)
	}
}

// A profile's PIN cannot be guessed faster by making new devices. Each device has its own
// count, and the profile has one across all of them: after ten wrong PINs, wherever they
// were typed, every device waits a minute, the right PIN included. The wait is short, it
// is still there after a restart, and Forgot PIN ends it at once.
func TestPINGuessesAcrossDevicesShareOneCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	owner := onMainProfile(t, s)
	var kid Profile
	if w := call(t, s, "POST", "/v1/profiles", owner.AccessToken, ProfileCreate{Name: "Kid", PIN: "4821"}, &kid); w.Code != 200 {
		t.Fatalf("adding a profile answered %d", w.Code)
	}
	try := func(n int, pin string) (int, int64) {
		t.Helper()
		in := signIn(t, s, n)
		var refused errorBody
		w := call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", in.Session.AccessToken, ProfileSelect{PIN: pin}, nil)
		if w.Code != 200 {
			jsonInto(w, &refused)
		}
		return w.Code, refused.Error.RetryAfterMs
	}
	// One wrong PIN on each of ten new devices: no device has reached a wait of its own
	// that matters, but the profile has.
	for n := range 10 {
		if code, _ := try(300+n, "0000"); code != 403 {
			t.Fatalf("wrong PIN on new device %d answered %d, want 403", n, code)
		}
	}
	code, wait := try(320, "4821")
	if code != 429 || wait < 50_000 || wait > 60_000 {
		t.Fatalf("the right PIN on an eleventh device answered %d with a wait of %d ms, want 429 and about a minute", code, wait)
	}
	closeDB(s.db)
	s = openTestServer(t, path)
	if code, _ := try(321, "4821"); code != 429 {
		t.Fatalf("after a restart the right PIN answered %d, want 429", code)
	}
	// Forgot PIN: the account's password sets a new PIN from any profile, and the wait is over.
	in := signIn(t, s, 322)
	if w := call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/pin", in.Session.AccessToken, ProfilePIN{PIN: "7777", CurrentPassword: testPassword}, nil); w.Code != 200 {
		t.Fatalf("Forgot PIN answered %d: %s", w.Code, w.Body)
	}
	if code, _ := try(323, "7777"); code != 200 {
		t.Fatalf("the new PIN straight after Forgot PIN answered %d, want 200", code)
	}
	// The right PIN clears the count, and the wait passes by itself.
	for n := range 10 {
		try(340+n, "0000")
	}
	clock := time.Now().Add(61 * time.Second)
	s.now = func() time.Time { return clock }
	if code, _ := try(360, "7777"); code != 200 {
		t.Fatalf("the right PIN after the wait answered %d, want 200", code)
	}
	for n := range 9 {
		try(370+n, "0000")
	}
	if code, _ := try(390, "7777"); code != 200 {
		t.Fatalf("after a right PIN, nine wrong ones elsewhere made a new device wait (%d)", code)
	}
}
