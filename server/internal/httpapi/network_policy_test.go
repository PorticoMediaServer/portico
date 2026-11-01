package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"strconv"
	"testing"
)

func TestSetupOriginAndContentType(t *testing.T) {
	d := Dependencies{SetupHosts: []string{"media.example"}, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}
	for _, tc := range []struct {
		origin, content, site string
		want                  bool
	}{
		{"https://media.example", "application/json", "same-origin", true},
		{"https://another.example", "application/json", "cross-site", false},
		{"https://media.example", "text/plain", "same-origin", false},
		{"https://media.example", "application/jsonp", "same-origin", false},
		{"", "application/json", "", true},
		{"", "application/x-www-form-urlencoded", "", false},
	} {
		r := httptest.NewRequest("POST", "http://backend/v1/setup", nil)
		r.RemoteAddr = "127.0.0.1:5"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "media.example")
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.content)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		if got := d.setupRequestAllowed(r); got != tc.want {
			t.Fatalf("%+v: %v", tc, got)
		}
	}
}
func TestConnectivityUsesOwnerNetworksAndProxyChain(t *testing.T) {
	f := administrationFixtureNew(t)
	document, err := f.deps.Console.Settings(context.Background(), operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"expectedRevision":` + strconv.FormatInt(document.Revision, 10) + `,"operationId":"networks","lanNetworks":[],"trustedProxies":["127.0.0.1","10.0.0.2"],"advertiseUrl":"http://media.example:32500"}`
	if w := f.call("PATCH", "/v1/admin/connectivity/policy", f.owner, body); w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "http://backend/v1/admin/connectivity/status", nil)
	r.RemoteAddr = "127.0.0.1:5"
	r.Header.Set("Authorization", "Bearer "+f.owner)
	r.Header.Set("X-Forwarded-For", "192.168.1.9, 203.0.113.9, 10.0.0.2")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	var status struct {
		Connection ConnectionDocument `json:"connection"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || w.Code != 200 {
		t.Fatalf("%d %s %v", w.Code, w.Body, err)
	}
	out := status.Connection
	if !out.Remote || !out.PlainHTTPOutsideHome || out.ClientAddress != "203.0.113.9" || len(out.AccessURLs) != 1 {
		t.Fatalf("%+v", out)
	}
	// The external HTTPS origin works for bundled web writes through the proxy.
	r = httptest.NewRequest("PATCH", "http://backend/v1/admin/connectivity/policy", bytes.NewBufferString(`{"expectedRevision":`+strconv.FormatInt(document.Revision+1, 10)+`,"operationId":"networks-again","lanNetworks":["203.0.113.0/24"]}`))
	r.RemoteAddr = "127.0.0.1:5"
	r.Header.Set("Authorization", "Bearer "+f.owner)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://media.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "media.example")
	r.Header.Set("X-Forwarded-For", "192.168.1.9")
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("proxy write %d %s", w.Code, w.Body)
	}
}

func TestConfiguredSetupProxyHostNeedsNoAPIOriginAllowList(t *testing.T) {
	d := Dependencies{SetupHosts: []string{"media.example"}}
	r := httptest.NewRequest("POST", "http://media.example/v1/setup/browser", nil)
	r.RemoteAddr = "203.0.113.9:42"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://media.example")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if !d.setupRequestAllowed(r) {
		t.Fatal("TLS-terminating proxy cannot bootstrap with preserved Host")
	}
	r.Header.Set("Origin", "https://another.example")
	if d.setupRequestAllowed(r) {
		t.Fatal("another site's origin accepted")
	}
}

func TestLargeUploadsHaveTheirOwnBoundedBudget(t *testing.T) {
	spec := laneSpecs[laneUpload]
	if spec.capacity != 2 || spec.budget != 0 || !spec.endsWithClient || spec.queueWait != 0 {
		t.Fatalf("%+v", spec)
	}
	for _, route := range []string{"POST /v1/admin/live-sources", "POST /v1/admin/live-sources/preview", "POST /v1/diagnostics/client-logs"} {
		if routeLanes[route] != laneUpload {
			t.Fatalf("%s has no upload budget", route)
		}
	}
}
