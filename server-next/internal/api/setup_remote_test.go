package api

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const away = "203.0.113.9"

// setupFrom sets a new server up from addr with the remote access step's choice and returns
// the owner's sign-in on the main profile, chosen from home; nil when setup was refused.
func setupFrom(t *testing.T, s *Server, addr string, step SetupRemote) *Session {
	t.Helper()
	body := SetupOwnerRequest{SetupCode: s.setupCode, SetupRemote: step, Username: "Justin", Password: testPassword, Device: device(0)}
	w := reach(s, "POST", "/v1/setup/owner", "", addr, "203.0.113.1:32500", true, body)
	if w.Code != 200 {
		return nil
	}
	var in SignInResponse
	jsonInto(w, &in)
	var out Session
	if w := reach(s, "POST", "/v1/profiles/"+in.Profiles[0].ID+"/select", in.Session.AccessToken, "127.0.0.1", "localhost", false, ProfileSelect{}); w.Code != 200 {
		t.Fatalf("choosing the main profile answered %d: %s", w.Code, w.Body)
	} else {
		jsonInto(w, &out)
	}
	return &out
}

// Setup's remote access step: On unless the person turns it off, and turning it off from
// outside the home networks, which would shut out the connection doing setup, must be confirmed.
func TestSetupRemoteAccessStep(t *testing.T) {
	s := newTestServer(t)
	if setupFrom(t, s, away, SetupRemote{RemoteAccess: "off"}) != nil {
		t.Fatal("Remote access Off from outside was accepted without confirming")
	}
	owner := setupFrom(t, s, away, SetupRemote{RemoteAccess: "off", ConfirmRefusal: true})
	if owner == nil {
		t.Fatal("a confirmed Remote access Off was refused")
	}
	if v, _ := s.access.current(); v.RemoteAccess != "off" || v.RemoteSecurity != "enhanced" {
		t.Fatalf("setup left %+v", v)
	}
	if w := reach(s, "GET", "/v1/system", "", away, "203.0.113.1:32500", true, nil); w.Code != 403 {
		t.Fatalf("outside after setup with Remote access Off answered %d", w.Code)
	}

	on := newTestServer(t)
	if setupFrom(t, on, away, SetupRemote{}) == nil {
		t.Fatal("setup with the default remote access was refused")
	}
	if v, _ := on.access.current(); v.RemoteAccess != "on" {
		t.Fatalf("the default remote access is %q", v.RemoteAccess)
	}
}

// Enhanced is chosen from the start but takes effect only once setup has finished, and then
// only once there is an address it can allow; with a Portico address it waits for the
// certificate, saying so in the owner's words, and turns on by itself when it arrives.
func TestEnhancedWaitsForSetupAndCertificate(t *testing.T) {
	s := newTestServer(t)
	owner := setupFrom(t, s, "127.0.0.1", SetupRemote{}).AccessToken
	s.SetPorticoAddress("cabin.portico.example", true, "")
	stranger := func() int { return reach(s, "GET", "/v1/system", "", away, "203.0.113.1:32500", true, nil).Code }
	security := func() RemoteSecurity {
		var st RemoteAccessStatus
		jsonInto(reach(s, "GET", "/v1/admin/remote-access", owner, "127.0.0.1", "localhost", false, nil), &st)
		return st.RemoteSecurity
	}
	if got := security(); got.Chosen != "enhanced" || got.Active != "standard" || got.Waiting != "setup_not_finished" || stranger() != 200 {
		t.Fatalf("during setup: %+v", got)
	}
	if w := reach(s, "POST", "/v1/setup/finish", owner, "127.0.0.1", "localhost", false, SetupFinish{}); w.Code != 200 {
		t.Fatalf("finishing setup answered %d: %s", w.Code, w.Body)
	}
	if got := security(); got.Active != "enhanced" || stranger() != 403 {
		t.Fatalf("after setup: %+v", got)
	}
	if code := reach(s, "GET", "/v1/system", "", away, "cabin.portico.example", true, nil).Code; code != 200 {
		t.Fatalf("the Portico address under Enhanced answered %d", code)
	}

	s.SetPorticoAddress("cabin.portico.example", false, "")
	got := security()
	if got.Active != "standard" || got.Waiting != "certificate_not_ready" || stranger() != 200 ||
		got.Message != "Enhanced Security requires an SSL certificate, which may take a few minutes to be issued to your server. "+
			"This feature will be enabled automatically once that certificate is available." {
		t.Fatalf("waiting for the certificate: %+v", got)
	}
	s.SetPorticoAddress("cabin.portico.example", true, "")
	if got := security(); got.Active != "enhanced" || stranger() != 403 {
		t.Fatalf("once the certificate arrived: %+v", got)
	}

	// With no address to allow at all, Enhanced never refuses everything: it waits, and says why.
	bare := newTestServer(t)
	token := setupFrom(t, bare, "127.0.0.1", SetupRemote{}).AccessToken
	reach(bare, "POST", "/v1/setup/finish", token, "127.0.0.1", "localhost", false, SetupFinish{})
	var st RemoteAccessStatus
	call(t, bare, "GET", "/v1/admin/remote-access", token, nil, &st)
	if st.RemoteSecurity.Waiting != "no_approved_address" || reach(bare, "GET", "/v1/system", "", away, "x.example", true, nil).Code != 200 {
		t.Fatalf("with no approved address: %+v", st.RemoteSecurity)
	}
}

// The dry run says whether the connection asking would be refused under proposed settings, and
// changes nothing; finishing setup from a connection Enhanced would refuse asks first, and the
// owner can approve this address in the same step, or keep Standard.
func TestEnhancedCheckAndConfirmation(t *testing.T) {
	s := newTestServer(t)
	s.SetPorticoAddress("cabin.portico.example", true, "")
	owner := setupFrom(t, s, away, SetupRemote{}).AccessToken
	current, _ := s.access.current()
	check := func(host string) RouteCheck {
		var out RouteCheck
		w := reach(s, "POST", "/v1/admin/remote-access/route-check", owner, away, host, true, current)
		if err := jsonInto(w, &out); err != nil || w.Code != 200 {
			t.Fatalf("the check answered %d: %s", w.Code, w.Body)
		}
		return out
	}
	if c := check("media.example.com"); !c.Refused || c.Reason != "address" || c.Address != "media.example.com:443" || c.Fixes[0] != "allow" {
		t.Fatalf("a route Enhanced would refuse: %+v", c)
	}
	if c := check("cabin.portico.example"); c.Refused {
		t.Fatalf("the Portico address was said to be refused: %+v", c)
	}
	if got, _ := s.access.current(); got.RemoteSecurity != current.RemoteSecurity || s.setupFinished() {
		t.Fatal("the check changed something")
	}

	finish := func(req SetupFinish) (int, string) {
		w := reach(s, "POST", "/v1/setup/finish", owner, away, "media.example.com", true, req)
		return w.Code, w.Body.String()
	}
	if code, body := finish(SetupFinish{}); code != 409 || !strings.Contains(body, "would_refuse_this_route") {
		t.Fatalf("finishing from a route Enhanced would refuse answered %d: %s", code, body)
	}
	if code, body := finish(SetupFinish{AllowThisAddress: true}); code != 200 {
		t.Fatalf("finishing with Allow this address answered %d: %s", code, body)
	}
	if v, _ := s.access.current(); len(v.AllowedAddresses) != 1 || v.AllowedAddresses[0] != "media.example.com:443" {
		t.Fatalf("the approved addresses are %q", v.AllowedAddresses)
	}
	if code := reach(s, "GET", "/v1/me", owner, away, "media.example.com", true, nil).Code; code != 200 {
		t.Fatalf("the owner's route after approving it answered %d", code)
	}

	// Declining: keep Standard, and the route goes on working.
	other := newTestServer(t)
	other.SetPorticoAddress("den.portico.example", true, "")
	token := setupFrom(t, other, away, SetupRemote{}).AccessToken
	if w := reach(other, "POST", "/v1/setup/finish", token, away, "media.example.com", true, SetupFinish{RemoteSecurity: "standard"}); w.Code != 200 {
		t.Fatalf("keeping Standard answered %d: %s", w.Code, w.Body)
	}
	if code := reach(other, "GET", "/v1/me", token, away, "media.example.com", true, nil).Code; code != 200 {
		t.Fatalf("the owner's route with Standard answered %d", code)
	}
}

// Enhanced waits on the owner's own certificate as it does on the Portico one: with certificate
// files named but not usable it works as Standard and says why; once they are, it takes effect.
func TestEnhancedWaitsForTheOwnCertificate(t *testing.T) {
	s := newTestServer(t)
	owner := setupFrom(t, s, "127.0.0.1", SetupRemote{}).AccessToken
	usable := false
	s.access.ownCertificateUsable = func() bool { return usable }
	var v RemoteAccessSettings
	jsonInto(reach(s, "GET", "/v1/admin/settings/remote-access", owner, "127.0.0.1", "localhost", false, nil), &v)
	v.OwnAddress = OwnAddress{Enabled: true, Domain: "media.example.com", CertificateFile: "cert.pem", KeyFile: "key.pem"}
	if w := reach(s, "PUT", "/v1/admin/settings/remote-access", owner, "127.0.0.1", "localhost", false, RemoteAccessSave{RemoteAccessSettings: v}); w.Code != 200 {
		t.Fatalf("saving the own address answered %d: %s", w.Code, w.Body)
	}
	stranger := func() int { return reach(s, "GET", "/v1/system", "", away, "203.0.113.1:32500", true, nil).Code }
	security := func() RemoteSecurity {
		var st RemoteAccessStatus
		jsonInto(reach(s, "GET", "/v1/admin/remote-access", owner, "127.0.0.1", "localhost", false, nil), &st)
		return st.RemoteSecurity
	}
	if got := security(); got.Waiting != "certificate_not_ready" || stranger() != 200 {
		t.Fatalf("with the certificate not usable: %+v", got)
	}
	usable = true
	if got := security(); got.Active != "enhanced" || stranger() != 403 {
		t.Fatalf("with the certificate usable: %+v", got)
	}
}

// Port mapping waits for an owner, and starts as setup finishes, with no restart. The router
// is never asked here: the mapper is not running, and only what it is told to want is read.
func TestPortMappingStartsWhenSetupFinishes(t *testing.T) {
	s := newTestServer(t)
	s.access.mu.Lock()
	s.access.listenPort = 32400 // as ServeNetwork would set it for a server the network can reach
	s.access.mu.Unlock()
	v, _ := s.access.current()
	s.applyCertificate(v)
	if s.ports.Wanted() {
		t.Fatal("a server with no owner wants its port open on the router")
	}
	if setupFrom(t, s, "127.0.0.1", SetupRemote{}) == nil {
		t.Fatal("setup was refused")
	}
	if !s.ports.Wanted() {
		t.Fatal("port mapping did not start when setup finished")
	}
}

// A server that knows no home networks counts everyone as outside, the household included, so
// Enhanced does not turn on by itself there: it waits and says why. The owner can still turn it
// on, and can let only the owner sign in from outside, each after confirming.
func TestEnhancedWaitsForHomeNetworksUnlessTheOwnerTurnsItOn(t *testing.T) {
	s := newTestServer(t)
	const local, host = "127.0.0.1", "localhost"
	owner := setupFrom(t, s, local, SetupRemote{}).AccessToken
	if w := reach(s, "POST", "/v1/setup/finish", owner, local, host, false, SetupFinish{}); w.Code != 200 {
		t.Fatalf("finishing setup answered %d: %s", w.Code, w.Body)
	}
	none := []string{}
	if w := reach(s, "PUT", "/v1/admin/settings/network", owner, local, host, false, NetworkSettingsEdit{HomeNetworks: &none, TrustedProxies: []string{}, ConfirmRefusal: true}); w.Code != 200 {
		t.Fatalf("emptying the home networks answered %d: %s", w.Code, w.Body)
	}
	s.SetPorticoAddress("den.portico.example", true, "")
	household := func() int {
		return reach(s, "GET", "/v1/system", "", "192.168.1.20", "192.168.1.10:32500", false, nil).Code
	}
	status := func() RemoteAccessStatus {
		var st RemoteAccessStatus
		jsonInto(reach(s, "GET", "/v1/admin/remote-access", owner, local, host, false, nil), &st)
		return st
	}
	if got := status().RemoteSecurity; got.Active != "standard" || got.Waiting != "no_home_networks" || got.Message == "" || household() != 200 {
		t.Fatalf("with a certificate and no home networks: %+v, the household answered %d", got, household())
	}

	var v RemoteAccessSettings
	jsonInto(reach(s, "GET", "/v1/admin/settings/remote-access", owner, local, host, false, nil), &v)
	save := func(v RemoteAccessSettings, confirm bool) *httptest.ResponseRecorder {
		return reach(s, "PUT", "/v1/admin/settings/remote-access", owner, local, host, false, RemoteAccessSave{RemoteAccessSettings: v, ConfirmRefusal: confirm})
	}
	v.SignInFromOutside = "owner"
	if w := save(v, false); w.Code != 409 || !strings.Contains(w.Body.String(), "would_refuse_sign_in") {
		t.Fatalf("owner only with no home networks, unconfirmed, answered %d: %s", w.Code, w.Body)
	}
	if w := save(v, true); w.Code != 200 {
		t.Fatalf("owner only, confirmed, answered %d: %s", w.Code, w.Body)
	}
	if st := status(); !slices.ContainsFunc(st.Warnings, func(w Warning) bool { return w.Code == "owner_only_no_home" }) {
		t.Fatalf("owner only with no home networks is not warned about: %+v", st.Warnings)
	}

	v.EnhancedWithoutHome = true
	if w := save(v, false); w.Code != 409 || !strings.Contains(w.Body.String(), "would_refuse_this_route") {
		t.Fatalf("turning Enhanced on with no home networks, unconfirmed, answered %d: %s", w.Code, w.Body)
	}
	if w := save(v, true); w.Code != 200 {
		t.Fatalf("turning Enhanced on, confirmed, answered %d: %s", w.Code, w.Body)
	}
	if got := s.access.security(v, []string{"den.portico.example"}, true, false); got.Active != "enhanced" || household() != 403 {
		t.Fatalf("after the owner turned Enhanced on: %+v, the household answered %d", got, household())
	}
}
