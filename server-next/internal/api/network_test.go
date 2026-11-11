package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
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
	// Trusting a forwarded address from most of the internet is the owner's choice to make: it
	// is saved, warned about, and the warning goes once it no longer holds.
	var risky NetworkSettings
	if w := call(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, NetworkSettingsEdit{TrustedProxies: []string{"0.0.0.0/0"}}, &risky); w.Code != 200 ||
		len(risky.Warnings) != 1 || risky.Warnings[0].Code != "proxy_everywhere" {
		t.Fatalf("the whole internet as a proxy answered %d %+v", w.Code, risky)
	}
	call(t, s, "GET", "/v1/admin/settings/network", owner.AccessToken, nil, &risky)
	if len(risky.Warnings) != 1 {
		t.Fatalf("the warning is not shown while it holds: %+v", risky)
	}
	settings = NetworkSettings{}
	if w := call(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, NetworkSettingsEdit{TrustedProxies: []string{}}, &settings); w.Code != 200 ||
		len(settings.Warnings) != 0 {
		t.Fatalf("taking it back answered %d %+v", w.Code, settings)
	}
	if got := save("", edit); got != 200 {
		t.Fatalf("saving the settings again answered %d", got)
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

// A listed proxy that passes on no visitor's address must not make its visitors count as at
// home, although the proxy itself is. They are away, the owner's network settings say what
// is wrong, and once the proxy sends the address, a visitor at home is home again.
func TestListedProxyThatSendsNoAddress(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	const proxy = "192.168.1.2"
	edit := NetworkSettingsEdit{HomeNetworks: &[]string{"192.168.1.0/24"}, TrustedProxies: []string{proxy}}
	var saved NetworkSettings
	if w := call(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, edit, &saved); w.Code != 200 || len(saved.Warnings) != 0 {
		t.Fatalf("saving the settings answered %d with warnings %v", w.Code, saved.Warnings)
	}
	for n, forwarded := range []string{"", "unknown", "203.0.113.9", "192.168.1.20"} {
		body := SignInRequest{Username: "justin", Password: testPassword, Device: device(300 + n)}
		if got := from(t, s, proxy, forwarded, "POST", "/v1/auth/sign-in", "", body, nil); got != 200 {
			t.Fatalf("signing in through the proxy with %q forwarded answered %d", forwarded, got)
		}
	}
	networks := map[string]string{}
	for _, d := range get[Page[Device]](t, s, owner.AccessToken, "/v1/devices").Items {
		networks[d.Name] = d.Network
	}
	if networks["Device 300"] != "away" || networks["Device 301"] != "away" || networks["Device 302"] != "away" || networks["Device 303"] != "home" {
		t.Errorf("no header, junk, an outside address and a home address are listed as %v, want away, away, away, home", networks)
	}
	warnings := get[NetworkSettings](t, s, owner.AccessToken, "/v1/admin/settings/network").Warnings
	if len(warnings) != 1 || warnings[0].Code != "proxy_without_header" || !strings.Contains(warnings[0].Message, proxy) ||
		!strings.Contains(warnings[0].Message, "X-Forwarded-For") {
		t.Errorf("the owner's network settings say %+v", warnings)
	}
	// Wrong guesses through the silent proxy are counted against the proxy's address, like
	// any network away from home, and are not let off as the household's.
	wrong := SignInRequest{Username: "justin", Password: "not the password"}
	for n := range 30 {
		wrong.Device = device(400 + n)
		clock := time.Now().Add(time.Duration(n) * 2 * time.Minute)
		s.now = func() time.Time { return clock }
		from(t, s, proxy, "", "POST", "/v1/auth/sign-in", "", wrong, nil)
	}
	later := time.Now().Add(62 * time.Minute)
	s.now = func() time.Time { return later }
	right := SignInRequest{Username: "justin", Password: testPassword, Device: device(500)}
	if got := from(t, s, proxy, "", "POST", "/v1/auth/sign-in", "", right, nil); got != 429 {
		t.Errorf("after 30 wrong guesses through the silent proxy the next sign-in answered %d, want 429", got)
	}
}

// The log says once that an address in the trusted list sent no visitor's address, and
// once sixteen are noted it says nothing about any more: a wide trusted range must not let
// anyone add a line to the owner's log with every request.
func TestSilentProxiesAreLoggedOnceAndOnlyWhileNoted(t *testing.T) {
	s := newTestServer(t)
	var log bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&log, nil))
	for range 2 {
		for n := range maxSilentProxies + 4 {
			s.noteSilentProxy(fmt.Sprintf("172.18.0.%d", n+1))
		}
	}
	if lines := strings.Count(log.String(), "with no X-Forwarded-For"); lines != maxSilentProxies {
		t.Errorf("%d addresses noted twice wrote %d warnings, want one for each of the first %d", maxSilentProxies+4, lines, maxSilentProxies)
	}
	if warnings := s.networkWarnings(s.networks()); len(warnings) != maxSilentProxies {
		t.Errorf("the owner is shown %d warnings, want %d", len(warnings), maxSilentProxies)
	}
}

// Behind a reverse proxy on the server's own machine that the owner has not listed, every
// visitor is one address, so a stranger's wrong passwords block sign-in for everyone. The
// block says who to ask; the owner, already signed in, can still enter their profile, is
// told what is wrong and how to fix it, and fixes it; and a PIN is still not guessable.
func TestUnlistedProxyOnThisMachine(t *testing.T) {
	s := newTestServer(t)
	const proxy, ownerAt, stranger = "127.0.0.1", "198.51.100.20", "203.0.113.66"
	in := setup(t, s)
	owner := onMain(t, s, in)
	main := "/v1/profiles/" + in.Profiles[0].ID
	if w := call(t, s, "PUT", main+"/pin", owner.AccessToken, ProfilePIN{PIN: "2468"}, nil); w.Code != 200 {
		t.Fatalf("setting a PIN answered %d", w.Code)
	}
	through := func(forwarded, method, path, token string, body, out any) (int, Error) {
		t.Helper()
		r := newRequest(method, path, token, body)
		r.RemoteAddr = proxy + ":4000"
		r.Header.Set("X-Forwarded-For", forwarded)
		w := serve(s, r)
		var refused errorBody
		if w.Code >= 400 {
			jsonInto(w, &refused)
		} else if out != nil {
			if err := jsonInto(w, out); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, refused.Error
	}

	wrong := SignInRequest{Username: "justin", Password: "not the password"}
	for n := range 30 {
		wrong.Device = device(600 + n)
		clock := time.Now().Add(time.Duration(n) * 2 * time.Minute)
		s.now = func() time.Time { return clock }
		through(stranger, "POST", "/v1/auth/sign-in", "", wrong, nil)
	}
	later := time.Now().Add(62 * time.Minute)
	s.now = func() time.Time { return later }

	// Everyone through the proxy is blocked from signing in, and told who to ask.
	fresh := SignInRequest{Username: "justin", Password: testPassword, Device: device(700)}
	if code, refused := through(ownerAt, "POST", "/v1/auth/sign-in", "", fresh, nil); code != 429 || refused.Message != blockedNetworkMessage {
		t.Fatalf("a sign-in through the blocked proxy answered %d %q", code, refused.Message)
	}

	// The owner's existing sign-in renews, enters the main profile with its PIN, reads the
	// warning and lists the proxy.
	var renewed, entered Session
	renewal := RefreshRequest{RefreshToken: owner.RefreshToken, InstallationID: device(0).InstallationID, RequestID: "after-the-block"}
	if code, _ := through(ownerAt, "POST", "/v1/auth/refresh", "", renewal, &renewed); code != 200 {
		t.Fatalf("the owner's renewal through the blocked proxy answered %d", code)
	}
	if code, refused := through(ownerAt, "POST", main+"/select", renewed.AccessToken, ProfileSelect{PIN: "2468"}, &entered); code != 200 {
		t.Fatalf("the signed-in owner entering their profile through the blocked proxy answered %d %q", code, refused.Message)
	}
	var settings NetworkSettings
	if code, _ := through(ownerAt, "GET", "/v1/admin/settings/network", entered.AccessToken, nil, &settings); code != 200 {
		t.Fatalf("the owner reading the network settings answered %d", code)
	}
	var warned bool
	for _, w := range settings.Warnings {
		warned = warned || (w.Code == "proxy_not_listed" && strings.Contains(w.Message, proxy) && strings.Contains(w.Message, "Trusted proxies") &&
			strings.Contains(w.Message, "If you run no proxy, do not add it."))
	}
	if !warned {
		t.Errorf("the owner's network settings do not say the proxy is not listed: %+v", settings.Warnings)
	}

	// The PIN is no way in for a guesser: a wrong one still makes this device wait, with the
	// PIN's own message, and the header is still not believed.
	if code, refused := through(ownerAt, "POST", main+"/select", entered.AccessToken, ProfileSelect{PIN: "0000"}, nil); code != 403 || refused.Code != "profile_pin_invalid" {
		t.Errorf("a wrong PIN through the proxy answered %d %s", code, refused.Code)
	}
	code, refused := through(ownerAt, "POST", main+"/select", entered.AccessToken, ProfileSelect{PIN: "2468"}, nil)
	if code != 429 || refused.Code != "profile_pin_locked" || refused.Message == blockedNetworkMessage {
		t.Errorf("the right PIN straight after a wrong one answered %d %s %q, want the PIN's own wait", code, refused.Code, refused.Message)
	}

	edit := NetworkSettingsEdit{TrustedProxies: []string{proxy}}
	if code, refused := through(ownerAt, "PUT", "/v1/admin/settings/network", entered.AccessToken, edit, &settings); code != 200 {
		t.Fatalf("the owner listing the proxy answered %d %q", code, refused.Message)
	}
	for _, w := range settings.Warnings {
		if w.Code == "proxy_not_listed" {
			t.Errorf("the warning is still there after the proxy was listed: %q", w.Message)
		}
	}
	// With the proxy listed each visitor is their own address again: the owner signs in, and
	// the stranger who guessed starts from their own count.
	if code, refused := through(ownerAt, "POST", "/v1/auth/sign-in", "", fresh, nil); code != 200 {
		t.Errorf("a sign-in once the proxy is listed answered %d %q", code, refused.Message)
	}
}

// A proxy or CDN of the owner's at a public address that is not listed gets one warning,
// however many addresses it uses, saying what to do if it is theirs and to ignore it if not.
// A stranger sending the header cannot fill the settings or the log with it.
func TestUnlistedProxyAtAPublicAddress(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	var log bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&log, nil))
	wrong := SignInRequest{Username: "justin", Password: "not the password"}
	for range 2 {
		for n := range maxSilentProxies + 4 {
			wrong.Device = device(800 + n)
			from(t, s, fmt.Sprintf("203.0.113.%d", n+1), "192.168.1.20", "POST", "/v1/auth/sign-in", "", wrong, nil)
		}
	}
	var public []Warning
	for _, w := range get[NetworkSettings](t, s, owner.AccessToken, "/v1/admin/settings/network").Warnings {
		if w.Code == "proxy_not_listed_public" {
			public = append(public, w)
		}
		if w.Code == "proxy_not_listed" {
			t.Errorf("a public address is named as a proxy on the owner's own network: %q", w.Message)
		}
	}
	if len(public) != 1 || !strings.Contains(public[0].Message, "203.0.113.") || !strings.Contains(public[0].Message, "15 others") ||
		!strings.Contains(public[0].Message, "Trusted proxies") || !strings.Contains(public[0].Message, "ignore this") {
		t.Errorf("the warning for public addresses is %+v", public)
	}
	if lines := strings.Count(log.String(), "from public addresses"); lines != maxSilentProxies {
		t.Errorf("%d public addresses sending the header twice wrote %d log lines, want %d", maxSilentProxies+4, lines, maxSilentProxies)
	}
}

// In a container with a published port every visitor can arrive from the container network's
// gateway. While the server is guessing what is home, that address is away, setup says so
// to whoever is doing it and the owner's settings say so afterwards. If the owner lists a
// home network that holds the gateway, that is saved and holds, and the owner is warned.
func TestVisitorsFromTheGateway(t *testing.T) {
	const gateway, neighbour = "172.17.0.1", "172.17.0.9"
	old := defaultGateway
	defaultGateway = func() netip.Addr { return netip.MustParseAddr(gateway) }
	t.Cleanup(func() { defaultGateway = old })
	s := newTestServer(t)
	// What the server would guess inside a container: itself and the container's network.
	s.networks()
	s.network.mu.Lock()
	s.network.defaults = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("172.17.0.0/16")}
	s.network.mu.Unlock()
	codes := func(warnings []Warning) string {
		var out []string
		for _, w := range warnings {
			out = append(out, w.Code)
		}
		return strings.Join(out, ",")
	}

	var system System
	if from(t, s, gateway, "", "GET", "/v1/system", "", nil, &system); codes(system.Warnings) != "gateway_visitors" || !strings.Contains(system.Warnings[0].Message, gateway) {
		t.Fatalf("before setup, a request from the gateway is told %+v", system.Warnings)
	}
	system = System{}
	if from(t, s, neighbour, "", "GET", "/v1/system", "", nil, &system); len(system.Warnings) != 0 {
		t.Errorf("a request from another address is warned: %+v", system.Warnings)
	}
	// Setup works from the gateway all the same.
	var in SignInResponse
	body := SetupOwnerRequest{SetupCode: s.setupCode, Username: "Justin", Password: testPassword, Device: device(0)}
	if got := from(t, s, gateway, "", "POST", "/v1/setup/owner", "", body, &in); got != 200 {
		t.Fatalf("setup from the gateway answered %d", got)
	}
	owner := onMain(t, s, in)
	system = System{}
	if from(t, s, gateway, "", "GET", "/v1/system", "", nil, &system); len(system.Warnings) != 0 {
		t.Errorf("after setup a stranger is still told about the gateway: %+v", system.Warnings)
	}
	signInFrom := func(peer, forwarded string, n int) {
		t.Helper()
		body := SignInRequest{Username: "justin", Password: testPassword, Device: device(n)}
		if got := from(t, s, peer, forwarded, "POST", "/v1/auth/sign-in", "", body, nil); got != 200 {
			t.Fatalf("signing in from %s answered %d", peer, got)
		}
	}
	labels := func() map[string]string {
		out := map[string]string{}
		for _, d := range get[Page[Device]](t, s, owner.AccessToken, "/v1/devices").Items {
			out[d.Name] = d.Network
		}
		return out
	}
	signInFrom(gateway, "", 901)
	signInFrom(neighbour, "", 902)
	if got := labels(); got["Device 901"] != "away" || got["Device 902"] != "home" {
		t.Errorf("while guessing, the gateway and its neighbour are listed as %q and %q, want away and home", got["Device 901"], got["Device 902"])
	}
	settings := get[NetworkSettings](t, s, owner.AccessToken, "/v1/admin/settings/network")
	if codes(settings.Warnings) != "gateway_visitors" || slices.Contains(settings.HomeNetworks, gateway+"/32") {
		t.Errorf("the owner's settings warn %q with home networks %v", codes(settings.Warnings), settings.HomeNetworks)
	}

	// The owner says the container's network is home. It is their server: saved, and said.
	edit := NetworkSettingsEdit{HomeNetworks: &[]string{"172.17.0.0/16"}, TrustedProxies: []string{}}
	if w := call(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, edit, &settings); w.Code != 200 || len(settings.Warnings) != 0 {
		t.Fatalf("listing the container's network as home answered %d with warnings %q", w.Code, codes(settings.Warnings))
	}
	// A proxy on the host that passes addresses on is the other warning's business, not this one's.
	signInFrom(gateway, "203.0.113.9", 903)
	if got := codes(get[NetworkSettings](t, s, owner.AccessToken, "/v1/admin/settings/network").Warnings); got != "proxy_not_listed" {
		t.Errorf("a request through a proxy at the gateway warns %q, want proxy_not_listed", got)
	}
	signInFrom(gateway, "", 904)
	if got := labels(); got["Device 904"] != "home" {
		t.Errorf("with the gateway in the owner's home networks it is listed as %q, want home", got["Device 904"])
	}
	settings = get[NetworkSettings](t, s, owner.AccessToken, "/v1/admin/settings/network")
	if got := codes(settings.Warnings); !strings.Contains(got, "gateway_at_home") || strings.Contains(got, "gateway_visitors") {
		t.Errorf("with the gateway in the owner's home networks the settings warn %q", got)
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

// The owner reads one list of warnings: the wide proxy list first, then each trusted proxy
// address seen sending no X-Forwarded-For, each with its own code; saving starts it afresh.
func TestNetworkWarningsAreOneList(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	if w := call(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, NetworkSettingsEdit{TrustedProxies: []string{"0.0.0.0/0"}}, nil); w.Code != 200 {
		t.Fatalf("saving a wide proxy list answered %d: %s", w.Code, w.Body)
	}
	s.noteSilentProxy("203.0.113.9")
	warnings := get[NetworkSettings](t, s, owner.AccessToken, "/v1/admin/settings/network").Warnings
	if len(warnings) != 2 || warnings[0].Code != "proxy_everywhere" || warnings[1].Code != "proxy_without_header" ||
		!strings.Contains(warnings[1].Message, "203.0.113.9") {
		t.Fatalf("the warnings are %+v", warnings)
	}
	var saved NetworkSettings
	if w := call(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, NetworkSettingsEdit{TrustedProxies: []string{}}, &saved); w.Code != 200 ||
		len(saved.Warnings) != 0 {
		t.Fatalf("after a narrower save the warnings are %+v", saved.Warnings)
	}
}
