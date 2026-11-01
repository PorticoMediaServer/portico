package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
)

type gateRequest struct {
	url        string
	remote     string
	serverName string
	headers    map[string]string
}

// serve runs one request through the real router and reports the status, or
// aborted=true when the gate dropped it (http.ErrAbortHandler) with nothing
// written.
func serveGated(t *testing.T, handler http.Handler, q gateRequest) (code int, aborted bool) {
	t.Helper()
	r := httptest.NewRequest("GET", q.url, nil)
	r.RemoteAddr = q.remote
	r.TLS = nil
	if q.serverName != "" {
		r.TLS = &tls.ConnectionState{ServerName: q.serverName}
	}
	for k, v := range q.headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			if p != http.ErrAbortHandler {
				panic(p)
			}
			if w.Body.Len() != 0 || len(w.Header()) != 0 || w.Code != 200 {
				t.Fatalf("a refused request wrote: %d %v %q", w.Code, w.Header(), w.Body)
			}
			aborted = true
		}
	}()
	handler.ServeHTTP(w, r)
	return w.Code, false
}

// home is f.call from the owner's own machine: the gate judges every request,
// and the fixture's default client address (192.0.2.1) is outside.
func home(f administrationFixture, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.RemoteAddr = "127.0.0.1:4242"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func patchConnectivity(t *testing.T, f administrationFixture, values map[string]any) {
	t.Helper()
	doc, err := f.deps.Console.Settings(context.Background(), operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values["expectedRevision"] = doc.Revision
	values["operationId"] = fmt.Sprintf("remote-gate-%d", doc.Revision)
	raw, _ := json.Marshal(values)
	if w := home(f, "PATCH", "/v1/admin/connectivity/policy", f.owner, string(raw)); w.Code != 200 {
		t.Fatalf("connectivity %d %s", w.Code, w.Body)
	}
}

func TestRemoteGateThroughTheRouter(t *testing.T) {
	gate := remotegate.New()
	f := administrationFixtureNewWith(t, func(d *Dependencies) { d.RemoteGate = gate })
	if p := gate.Policy(); p == nil || p.Security() != remotegate.SecurityEnhanced || p.Access() != remotegate.AccessOn {
		t.Fatalf("policy installed at startup: %+v", p)
	}
	const outside, proxy, homeProxy = "198.51.100.1:42", "203.0.113.9:42", "192.168.1.5:42"
	patchConnectivity(t, f, map[string]any{"accessUrls": []string{"https://media.example.com"}, "trustedProxies": []string{"203.0.113.9/32", "192.168.1.5/32"}, "lanNetworks": []string{"192.168.1.0/24"}})
	path := "/v1/networking/connection"
	for _, c := range []struct {
		name    string
		q       gateRequest
		aborted bool
	}{
		{"own TLS name", gateRequest{url: "https://own.ns.portico.direct" + path, remote: outside, serverName: "own.ns.portico.direct"}, false},
		{"Host differs from the TLS name", gateRequest{url: "https://evil.example" + path, remote: outside, serverName: "own.ns.portico.direct"}, true},
		{"a listed name over this server's TLS", gateRequest{url: "https://media.example.com" + path, remote: outside, serverName: "own.ns.portico.direct"}, false},
		{"plain HTTP from outside", gateRequest{url: "http://media.example.com" + path, remote: outside}, true},
		{"proxy, https, listed", gateRequest{url: "http://media.example.com" + path, remote: proxy, headers: map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "198.51.100.77"}}, false},
		{"proxy, http, listed", gateRequest{url: "http://media.example.com" + path, remote: proxy, headers: map[string]string{"X-Forwarded-Proto": "http", "X-Forwarded-For": "198.51.100.77"}}, true},
		{"proxy, https, unlisted", gateRequest{url: "http://other.example.com" + path, remote: proxy, headers: map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "198.51.100.77"}}, true},
		{"home (loopback), any name, plain", gateRequest{url: "http://anything" + path, remote: "127.0.0.1:42"}, false},
		// Spoofed forwarding: the client wrote a home address at the left and
		// the proxy appended the real one. The rightmost hop the proxy added
		// is the client, so this is outside, and an unlisted Host is refused.
		{"proxy, spoofed home X-Forwarded-For, unlisted", gateRequest{url: "http://other.example.com" + path, remote: proxy, headers: map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "192.168.1.10, 198.51.100.77"}}, true},
		// The same spoof over forwarded plain HTTP, even for a listed name.
		{"proxy, spoofed home X-Forwarded-For, http", gateRequest{url: "http://media.example.com" + path, remote: proxy, headers: map[string]string{"X-Forwarded-Proto": "http", "X-Forwarded-For": "192.168.1.10, 198.51.100.77"}}, true},
		// A proxied request with no usable chain is judged as from outside,
		// never as the proxy's own address.
		{"proxy at home, no X-Forwarded-For, unlisted", gateRequest{url: "http://other.example.com" + path, remote: homeProxy}, true},
		{"proxy at home, malformed X-Forwarded-For, unlisted", gateRequest{url: "http://other.example.com" + path, remote: homeProxy, headers: map[string]string{"X-Forwarded-For": "not-an-address"}}, true},
		// A real home client through the proxy is home.
		{"proxy at home, home client", gateRequest{url: "http://other.example.com" + path, remote: homeProxy, headers: map[string]string{"X-Forwarded-For": "192.168.1.10"}}, false},
	} {
		code, aborted := serveGated(t, f.handler, c.q)
		if aborted != c.aborted || (!aborted && code != 200) {
			t.Errorf("%s: code %d aborted %v, want aborted %v", c.name, code, aborted, c.aborted)
		}
	}
	names, _ := gate.Refused.Snapshot()
	seen := map[string]bool{}
	for _, n := range names {
		seen[n.Name] = true
	}
	if !seen["evil.example"] || !seen["other.example.com"] {
		t.Fatalf("refused names: %+v", names)
	}

	// Standard answers plain HTTP from outside at any name; saving it changes
	// the live policy at once.
	patchConnectivity(t, f, map[string]any{"remoteSecurity": "standard"})
	if code, aborted := serveGated(t, f.handler, gateRequest{url: "http://myserver.mydomain.example" + path, remote: outside}); aborted || code != 200 {
		t.Fatalf("Standard: %d aborted %v", code, aborted)
	}
	// Remote access Off refuses outside clients in either mode, home still works.
	patchConnectivity(t, f, map[string]any{"remoteAccess": "off"})
	if _, aborted := serveGated(t, f.handler, gateRequest{url: "https://own.ns.portico.direct" + path, remote: outside, serverName: "own.ns.portico.direct"}); !aborted {
		t.Fatal("Off answered outside")
	}
	if code, aborted := serveGated(t, f.handler, gateRequest{url: "http://box" + path, remote: "127.0.0.1:42"}); aborted || code != 200 {
		t.Fatalf("Off at home: %d %v", code, aborted)
	}
}

func TestRemoteGateSettingsValidation(t *testing.T) {
	f := administrationFixtureNewWith(t, func(d *Dependencies) { d.RemoteGate = remotegate.New() })
	doc, _ := f.deps.Console.Settings(context.Background(), operations.AllowServerScope)
	for _, body := range []string{`{"remoteSecurity":"loose"}`, `{"remoteAccess":"sometimes"}`} {
		raw := strings.TrimSuffix(body, "}") + fmt.Sprintf(`,"expectedRevision":%d,"operationId":"remote-invalid-1"}`, doc.Revision)
		if w := home(f, "PATCH", "/v1/admin/connectivity/policy", f.owner, raw); w.Code != 400 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	w := home(f, "GET", "/v1/admin/connectivity/policy", f.owner, "")
	var policy struct {
		RemoteSecurity string `json:"remoteSecurity"`
		RemoteAccess   string `json:"remoteAccess"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &policy); err != nil || policy.RemoteSecurity != "enhanced" || policy.RemoteAccess != "on" {
		t.Fatalf("defaults %+v %v %s", policy, err, w.Body)
	}
}

func TestRefusedAddressesAndAllow(t *testing.T) {
	gate := remotegate.New()
	f := administrationFixtureNewWith(t, func(d *Dependencies) { d.RemoteGate = gate })
	w := home(f, "GET", "/v1/admin/connectivity/refused-addresses", f.owner, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"remoteSecurity":"enhanced"`) || !strings.Contains(w.Body.String(), `"names":[]`) {
		t.Fatalf("refused addresses %d %s", w.Code, w.Body)
	}
	if w := home(f, "GET", "/v1/admin/connectivity/refused-addresses", f.member, ""); w.Code == 200 {
		t.Fatal("a member read the refused addresses")
	}
	gate.Record("media.mine.example", remotegate.ReasonName)
	doc, _ := f.deps.Console.Settings(context.Background(), operations.AllowServerScope)
	allow := func(name string, revision int64) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"name": name, "expectedRevision": revision, "operationId": "allow-" + strings.ReplaceAll(name, ".", "-")})
		return home(f, "POST", "/v1/admin/connectivity/refused-addresses/allow", f.owner, string(raw))
	}
	for _, bad := range []string{"203.0.113.1", "box", "", "localhost"} {
		if w := allow(bad, doc.Revision); w.Code != 400 {
			t.Fatalf("allowed %q: %d %s", bad, w.Code, w.Body)
		}
	}
	if w := allow("Media.Mine.Example", doc.Revision); w.Code != 200 {
		t.Fatalf("allow %d %s", w.Code, w.Body)
	}
	stored, _ := f.deps.Console.Settings(context.Background(), operations.AllowServerScope)
	if len(stored.Effective.AccessURLs) != 1 || stored.Effective.AccessURLs[0] != "https://media.mine.example" {
		t.Fatalf("custom addresses %v", stored.Effective.AccessURLs)
	}
	if names, _ := gate.Refused.Snapshot(); len(names) != 0 {
		t.Fatalf("still refused after allowing: %+v", names)
	}
	if !gate.Policy().Listed("media.mine.example") {
		t.Fatal("the allowed name isn't live")
	}
	// Allowing it again adds nothing.
	if w := allow("media.mine.example", stored.Revision); w.Code != 200 {
		t.Fatalf("allow again %d %s", w.Code, w.Body)
	}
	again, _ := f.deps.Console.Settings(context.Background(), operations.AllowServerScope)
	if len(again.Effective.AccessURLs) != 1 {
		t.Fatalf("duplicated %v", again.Effective.AccessURLs)
	}
}

// TestLegitimateSetups walks what real owners run, through the real router,
// in Enhanced and in Standard. Anything a real setup hits either works, or is
// refused with a reason the owner sees under Refused addresses (whose fix the
// reason names). TLS handshake cases (no certificate for a name, or none at
// all) are in networking's TestRemoteGateLegitimateSetupsTLS.
func TestLegitimateSetups(t *testing.T) {
	gate := remotegate.New()
	f := administrationFixtureNewWith(t, func(d *Dependencies) { d.RemoteGate = gate })
	// The owner's settings: a custom domain (its own certificate, or behind
	// the home proxy), the home proxy at 192.168.1.5, home is 192.168.1.0/24.
	patchConnectivity(t, f, map[string]any{"accessUrls": []string{"https://media.example.com"}, "trustedProxies": []string{"192.168.1.5/32"}, "lanNetworks": []string{"192.168.1.0/24"}})
	const (
		own      = "own.ns.portico.direct"
		outside  = "198.51.100.1:42"
		hosted   = "203.0.113.50:42"
		lan      = "192.168.1.20:42"
		proxy    = "192.168.1.5:42"
		tailnet  = "100.101.102.103:42"
		loopback = "127.0.0.1:42"
		path     = "/v1/networking/connection"
	)
	type outcome struct {
		answered bool
		recorded string // name recorded with its reason, "name reason"; "" for none
	}
	for _, c := range []struct {
		setup              string
		access             string
		q                  gateRequest
		enhanced, standard outcome
	}{
		{"Portico app at its portico.direct name", "on", gateRequest{url: "https://" + own + path, remote: outside, serverName: own}, outcome{true, ""}, outcome{true, ""}},
		{"Portico Services' reachability check", "on", gateRequest{url: "https://" + own + "/v1/networking/endpoint-proof", remote: hosted, serverName: own}, outcome{true, ""}, outcome{true, ""}},
		{"LAN client by IP over HTTP", "on", gateRequest{url: "http://192.168.1.10:32500" + path, remote: lan}, outcome{true, ""}, outcome{true, ""}},
		{"LAN client at the portico.direct name over HTTPS", "on", gateRequest{url: "https://" + own + path, remote: lan, serverName: own}, outcome{true, ""}, outcome{true, ""}},
		{"Tailscale client, Private only", "private", gateRequest{url: "http://100.64.1.1:32500" + path, remote: tailnet}, outcome{true, ""}, outcome{true, ""}},
		{"Tailscale client by IP, Remote access On", "on", gateRequest{url: "http://100.64.1.1:32500" + path, remote: tailnet}, outcome{false, ""}, outcome{true, ""}},
		{"cloudflared (or another tunnel) on this machine", "on", gateRequest{url: "http://media.example.com" + path, remote: loopback, headers: map[string]string{"X-Forwarded-For": "198.51.100.77", "Cf-Connecting-Ip": "198.51.100.77", "X-Forwarded-Proto": "https"}}, outcome{true, ""}, outcome{true, ""}},
		{"Caddy at home (XFF and XFP)", "on", gateRequest{url: "http://media.example.com" + path, remote: proxy, headers: map[string]string{"X-Forwarded-For": "198.51.100.77", "X-Forwarded-Proto": "https"}}, outcome{true, ""}, outcome{true, ""}},
		{"Traefik at home (XFF, XFP and X-Forwarded-Host)", "on", gateRequest{url: "http://media.example.com" + path, remote: proxy, headers: map[string]string{"X-Forwarded-For": "198.51.100.77", "X-Forwarded-Proto": "https", "X-Forwarded-Host": "media.example.com"}}, outcome{true, ""}, outcome{true, ""}},
		{"nginx at home (XFF, no XFP)", "on", gateRequest{url: "http://media.example.com" + path, remote: proxy, headers: map[string]string{"X-Forwarded-For": "198.51.100.77"}}, outcome{true, ""}, outcome{true, ""}},
		{"proxy with no XFF at all", "on", gateRequest{url: "http://media.example.com" + path, remote: proxy}, outcome{true, ""}, outcome{true, ""}},
		{"proxy passing its upstream address as Host", "on", gateRequest{url: "http://192.168.1.10:32500" + path, remote: proxy, headers: map[string]string{"X-Forwarded-For": "198.51.100.77"}}, outcome{false, "192.168.1.5 proxy"}, outcome{true, ""}},
		{"proxy forwarding a name not added yet", "on", gateRequest{url: "http://myhome.example.net" + path, remote: proxy, headers: map[string]string{"X-Forwarded-For": "198.51.100.77", "X-Forwarded-Proto": "https"}}, outcome{false, "myhome.example.net name"}, outcome{true, ""}},
		{"custom domain with the owner's own certificate", "on", gateRequest{url: "https://media.example.com" + path, remote: outside, serverName: "media.example.com"}, outcome{true, ""}, outcome{true, ""}},
		{"DDNS name, no certificate, plain HTTP", "on", gateRequest{url: "http://myserver.duckdns.org:32500" + path, remote: outside}, outcome{false, "myserver.duckdns.org http"}, outcome{true, ""}},
		{"outside client by raw IP over HTTP", "on", gateRequest{url: "http://203.0.113.1:32500" + path, remote: outside}, outcome{false, ""}, outcome{true, ""}},
	} {
		for _, mode := range []string{"enhanced", "standard"} {
			want := c.enhanced
			if mode == "standard" {
				want = c.standard
			}
			patchConnectivity(t, f, map[string]any{"remoteSecurity": mode, "remoteAccess": c.access})
			before, _ := gate.Refused.Snapshot()
			_, aborted := serveGated(t, f.handler, c.q)
			if aborted == want.answered {
				t.Errorf("%s, %s: answered %v, want %v", c.setup, mode, !aborted, want.answered)
			}
			after, _ := gate.Refused.Snapshot()
			got := ""
			for _, n := range after {
				fresh := true
				for _, b := range before {
					if b.Name == n.Name && b.Count == n.Count {
						fresh = false
					}
				}
				if fresh {
					got = n.Name + " " + n.Reason
				}
			}
			if got != want.recorded {
				t.Errorf("%s, %s: recorded %q, want %q", c.setup, mode, got, want.recorded)
			}
		}
	}
	// The fix each reason names works. A name the proxy forwarded: Allow this
	// address, and it's answered.
	patchConnectivity(t, f, map[string]any{"remoteSecurity": "enhanced", "remoteAccess": "on"})
	doc, _ := f.deps.Console.Settings(context.Background(), operations.AllowServerScope)
	raw, _ := json.Marshal(map[string]any{"name": "myhome.example.net", "expectedRevision": doc.Revision, "operationId": "allow-myhome"})
	if w := home(f, "POST", "/v1/admin/connectivity/refused-addresses/allow", f.owner, string(raw)); w.Code != 200 {
		t.Fatalf("allow %d %s", w.Code, w.Body)
	}
	if _, aborted := serveGated(t, f.handler, gateRequest{url: "http://myhome.example.net" + path, remote: proxy, headers: map[string]string{"X-Forwarded-For": "198.51.100.77", "X-Forwarded-Proto": "https"}}); aborted {
		t.Fatal("an allowed name through the proxy is still refused")
	}
	// A DDNS name over plain HTTP: Standard, and it's answered.
	patchConnectivity(t, f, map[string]any{"remoteSecurity": "standard"})
	if _, aborted := serveGated(t, f.handler, gateRequest{url: "http://myserver.duckdns.org:32500" + path, remote: outside}); aborted {
		t.Fatal("Standard still refuses the DDNS name")
	}
}
