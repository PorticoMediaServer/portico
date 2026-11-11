package api

import (
	"crypto/tls"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// reach sends a request as if from addr ("127.0.0.1" is home by default, "203.0.113.9" is
// away), over HTTPS when secure, to host.
func reach(s *Server, method, path, token, addr, host string, secure bool, body any) *httptest.ResponseRecorder {
	r := newRequest(method, path, token, body)
	r.RemoteAddr = addr + ":50000"
	r.Host = host
	if secure {
		r.TLS = &tls.ConnectionState{}
	}
	return serve(s, r)
}

func saveRemote(t *testing.T, s *Server, token string, change func(*RemoteAccessSettings)) {
	t.Helper()
	var v RemoteAccessSettings
	w := reach(s, "GET", "/v1/admin/settings/remote-access", token, "127.0.0.1", "localhost", false, nil)
	if err := jsonInto(w, &v); err != nil || w.Code != 200 {
		t.Fatalf("reading remote access answered %d: %s", w.Code, w.Body)
	}
	change(&v)
	r := newRequest("PUT", "/v1/admin/settings/remote-access", token, v)
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("If-Match", w.Header().Get("ETag"))
	if w := serve(s, r); w.Code != 200 {
		t.Fatalf("saving remote access answered %d: %s", w.Code, w.Body)
	}
}

// Each remote access switch refuses what it should from outside the home networks, and
// nothing at home: Off refuses everything from outside; Secure connections Required refuses
// plain HTTP from outside, a trusted reverse proxy's HTTPS counting as secure; Enhanced also
// answers only at approved addresses and shows the owner what it refused.
func TestRemoteAccessFromOutside(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	const home, away, proxy = "127.0.0.1", "203.0.113.9", "10.9.9.9"
	expect := func(what string, w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("%s answered %d, want %d: %s", what, w.Code, status, w.Body)
		}
	}
	expect("plain HTTP from outside by default", reach(s, "GET", "/v1/system", "", away, "203.0.113.1:32500", false, nil), 200)

	saveRemote(t, s, owner, func(v *RemoteAccessSettings) { v.RemoteAccess = "off" })
	expect("outside with remote access off", reach(s, "GET", "/v1/system", "", away, "203.0.113.1:32500", true, nil), 403)
	expect("home with remote access off", reach(s, "GET", "/v1/system", "", home, "localhost:32500", false, nil), 200)

	saveRemote(t, s, owner, func(v *RemoteAccessSettings) { v.RemoteAccess, v.SecureConnections = "on", "required" })
	expect("plain HTTP from outside under Required", reach(s, "GET", "/v1/system", "", away, "203.0.113.1:32500", false, nil), 403)
	expect("HTTPS from outside under Required", reach(s, "GET", "/v1/system", "", away, "203.0.113.1:32500", true, nil), 200)
	expect("plain HTTP at home under Required", reach(s, "GET", "/v1/system", "", home, "localhost:32500", false, nil), 200)
	// A trusted reverse proxy that ended HTTPS for someone outside.
	var network NetworkSettings
	call(t, s, "GET", "/v1/admin/settings/network", owner, nil, &network)
	edit := NetworkSettingsEdit{HomeNetworks: &[]string{"127.0.0.0/8"}, TrustedProxies: []string{proxy}}
	if w := reach(s, "PUT", "/v1/admin/settings/network", owner, home, "localhost", false, edit); w.Code != 200 {
		t.Fatalf("saving networks answered %d: %s", w.Code, w.Body)
	}
	r := newRequest("GET", "/v1/system", "", nil)
	r.RemoteAddr, r.Host = proxy+":50000", "media.example.com"
	r.Header.Set("X-Forwarded-For", away)
	r.Header.Set("X-Forwarded-Proto", "https")
	expect("HTTPS through a trusted proxy under Required", serve(s, r), 200)
	r.Header.Set("X-Forwarded-Proto", "http")
	expect("plain HTTP through a trusted proxy under Required", serve(s, r), 403)

	saveRemote(t, s, owner, func(v *RemoteAccessSettings) {
		v.SecureConnections, v.RemoteSecurity = "preferred", "enhanced"
		v.OwnAddress = OwnAddress{Enabled: true, Domain: "Media.Example.com"}
		v.AllowedAddresses = []string{"203.0.113.1:8443"}
	})
	expect("the own domain under Enhanced", reach(s, "GET", "/v1/system", "", away, "media.example.com", true, nil), 200)
	expect("an added address under Enhanced", reach(s, "GET", "/v1/system", "", away, "203.0.113.1:8443", true, nil), 200)
	expect("an added host at another port under Enhanced", reach(s, "GET", "/v1/system", "", away, "203.0.113.1", true, nil), 403)
	expect("plain HTTP under Enhanced", reach(s, "GET", "/v1/system", "", away, "media.example.com", false, nil), 403)
	expect("any address at home under Enhanced", reach(s, "GET", "/v1/system", "", home, "whatever.local:32500", false, nil), 200)
	var status RemoteAccessStatus
	if err := jsonInto(reach(s, "GET", "/v1/admin/remote-access", owner, home, "localhost", false, nil), &status); err != nil {
		t.Fatal(err)
	}
	if !status.ThisDevice.AtHome || len(status.Refused) != 2 {
		t.Fatalf("the owner's status says %+v", status)
	}
	for _, refused := range status.Refused {
		if refused.Address == "203.0.113.1:443" && (refused.Reason != "address" || refused.Fixes[0] != "allow") ||
			refused.Address == "media.example.com:80" && refused.Reason != "http" {
			t.Errorf("refused %+v", refused)
		}
	}
}

// With "Only the owner", another account cannot sign in from outside, and a sign-in it made
// at home stops working away from home; the owner and everyone at home are unaffected.
func TestOnlyTheOwnerFromOutside(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	if w := call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: "sam", Password: testPassword}, nil); w.Code != 201 && w.Code != 200 {
		t.Fatalf("adding a member answered %d: %s", w.Code, w.Body)
	}
	signInFrom := func(username, addr string, n int) *httptest.ResponseRecorder {
		return reach(s, "POST", "/v1/auth/sign-in", "", addr, "localhost", true, SignInRequest{Username: username, Password: testPassword, Device: device(n)})
	}
	var sam SignInResponse
	if err := jsonInto(signInFrom("sam", "127.0.0.1", 1), &sam); err != nil || sam.Session.AccessToken == "" {
		t.Fatal("the member could not sign in at home")
	}
	saveRemote(t, s, owner, func(v *RemoteAccessSettings) { v.SignInFromOutside = "owner" })
	for i, c := range []struct {
		username, addr string
		status         int
	}{{"sam", "203.0.113.9", 403}, {"justin", "203.0.113.9", 200}, {"sam", "127.0.0.1", 200}} {
		if w := signInFrom(c.username, c.addr, 2+i); w.Code != c.status {
			t.Errorf("%s signing in from %s answered %d, want %d", c.username, c.addr, w.Code, c.status)
		}
	}
	for addr, want := range map[string]int{"203.0.113.9": 403, "127.0.0.1": 200} {
		if w := reach(s, "GET", "/v1/me", sam.Session.AccessToken, addr, "localhost", true, nil); w.Code != want {
			t.Errorf("the member's sign-in used from %s answered %d, want %d", addr, w.Code, want)
		}
	}
	if w := reach(s, "GET", "/v1/me", owner, "203.0.113.9", "localhost", true, nil); w.Code != 200 {
		t.Errorf("the owner away from home answered %d", w.Code)
	}
}

// A save with an old ETag is refused, and nonsense is refused naming the field.
func TestRemoteAccessSettingsChecks(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	saveRemote(t, s, owner, func(v *RemoteAccessSettings) { v.PublicPort = 40000 })
	v := defaultRemoteAccess()
	put := func(etag string, v RemoteAccessSettings) int {
		r := newRequest("PUT", "/v1/admin/settings/remote-access", owner, v)
		r.Header.Set("If-Match", etag)
		return serve(s, r).Code
	}
	if code := put(`"0"`, v); code != 412 {
		t.Errorf("a stale save answered %d", code)
	}
	for _, bad := range []func(*RemoteAccessSettings){
		func(v *RemoteAccessSettings) { v.RemoteAccess = "private" },
		func(v *RemoteAccessSettings) { v.PublicPort = 70000 },
		func(v *RemoteAccessSettings) { v.OwnAddress = OwnAddress{Enabled: true, Domain: "https://x.example/"} },
		func(v *RemoteAccessSettings) {
			v.OwnAddress = OwnAddress{Enabled: true, Domain: "x.example", CertificateFile: "../../etc/key.pem", KeyFile: "k.pem"}
		},
	} {
		changed := v
		bad(&changed)
		if code := put("", changed); code != 400 {
			t.Errorf("%+v answered %d, want 400", changed, code)
		}
	}
	var got RemoteAccessSettings
	call(t, s, "GET", "/v1/admin/settings/remote-access", owner, nil, &got)
	if got.PublicPort != 40000 {
		t.Fatalf("the saved document reads %+v", got)
	}
}

// An owner who shut themselves out creates the reset file and restarts: remote access and the
// home networks are back to safe defaults, Standard included, and the file is gone.
func TestResetRemoteAccessFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	owner := onMain(t, s, setup(t, s)).AccessToken
	saveRemote(t, s, owner, func(v *RemoteAccessSettings) {
		v.RemoteAccess, v.RemoteSecurity, v.SecureConnections = "off", "enhanced", "required"
	})
	edit := NetworkSettingsEdit{HomeNetworks: &[]string{}, TrustedProxies: []string{}}
	if w := reach(s, "PUT", "/v1/admin/settings/network", owner, "127.0.0.1", "localhost", false, edit); w.Code != 200 {
		t.Fatalf("emptying the home networks answered %d: %s", w.Code, w.Body)
	}
	if code := reach(s, "GET", "/v1/system", "", "127.0.0.1", "localhost", false, nil).Code; code != 403 {
		t.Fatalf("with no home networks and remote access off, this machine answered %d, want 403", code)
	}
	closeDB(s.db)
	reset := filepath.Join(filepath.Dir(path), resetRemoteFile)
	os.WriteFile(reset, nil, 0o600)
	s = openTestServer(t, path)
	if _, err := os.Stat(reset); err == nil {
		t.Fatal("the reset file is still there")
	}
	if v, _ := s.remote.current(); v.RemoteAccess != "on" || v.RemoteSecurity != "standard" || v.SecureConnections != "preferred" {
		t.Fatalf("after the reset: %+v", v)
	}
	for _, addr := range []string{"127.0.0.1", "203.0.113.9"} {
		if code := reach(s, "GET", "/v1/system", "", addr, "localhost", false, nil).Code; code != 200 {
			t.Fatalf("after the reset %s answered %d", addr, code)
		}
	}
}

// Enhanced keeps the newest refused addresses, and a made-up Host header is never kept.
func TestRefusedListKeepsTheNewest(t *testing.T) {
	s := newTestServer(t)
	for i := range maxRefused + 5 {
		s.remote.noteRefused("host"+strconv.Itoa(i)+".example:443", "address", []string{"allow"}, time.Now().Add(time.Duration(i)*time.Second))
	}
	list := s.remote.refusedList()
	if len(list) != maxRefused || list[0].Address != "host"+strconv.Itoa(maxRefused+4)+".example:443" {
		t.Fatalf("the list holds %d, newest %+v", len(list), list[0])
	}
}
