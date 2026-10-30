package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/connectivity"
)

func setDLNA(t *testing.T, f *v1Fixture, on bool) {
	t.Helper()
	var current connectivity.Policy
	w := f.rawFrom(homePeer, "GET", "/v1/admin/connectivity/policy", f.owner.AccessToken, nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil || w.Code != 200 {
		t.Fatalf("read policy: %d %s", w.Code, w.Body.String())
	}
	// b3ed48789 limits defaults to attached subnets. Name the fixture's home
	// explicitly rather than relying on the test runner's real interfaces.
	body := map[string]any{"expectedRevision": current.Revision, "operationId": "dlna-switch-" + strconv.FormatBool(on) + "-" + strconv.FormatInt(current.Revision, 10), "dlnaEnabled": on, "lanNetworks": []string{"192.168.1.0/24"}}
	if w = f.rawFrom(homePeer, "PATCH", "/v1/admin/connectivity/policy", f.owner.AccessToken, nil, body); w.Code != 200 || !strings.Contains(w.Body.String(), `"dlnaEnabled":`+strconv.FormatBool(on)) {
		t.Fatalf("set DLNA %v: %d %s", on, w.Code, w.Body.String())
	}
}

// The owner's switch is off by default, applies on save without a restart,
// and opens the DLNA routes to the home network only.
func TestDLNASwitchIsHotAndHomeOnly(t *testing.T) {
	f := remotePolicyFixture(t)
	browse := func(peer string) *httptest.ResponseRecorder {
		return f.rawFrom(peer, "POST", "/dlna/control/cds", "", map[string]string{"Content-Type": `text/xml; charset="utf-8"`, "SOAPACTION": `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"`},
			`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><ObjectID>0</ObjectID><BrowseFlag>BrowseDirectChildren</BrowseFlag><StartingIndex>0</StartingIndex><RequestedCount>0</RequestedCount></u:Browse></s:Body></s:Envelope>`)
	}
	if w := f.rawFrom(homePeer, "GET", "/dlna/device.xml", "", nil, nil); w.Code != 404 {
		t.Fatalf("off by default: %d", w.Code)
	}
	setDLNA(t, f, true)
	if w := f.rawFrom(homePeer, "GET", "/dlna/device.xml", "", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "urn:schemas-upnp-org:device:MediaServer:1") {
		t.Fatalf("on, from home: %d %s", w.Code, w.Body.String())
	}
	if w := browse(homePeer); w.Code != 200 || !strings.Contains(w.Body.String(), "<TotalMatches>") {
		t.Fatalf("Browse from home: %d %s", w.Code, w.Body.String())
	}
	// A private address outside the fixture home is remote too. Anonymous
	// DLNA must never gain blanket RFC1918 trust from its numeric address.
	for _, peer := range []string{remotePeer, "10.254.253.252:5000"} {
		for _, path := range []string{"/dlna/device.xml", "/dlna/media/anything"} {
			if w := f.rawFrom(peer, "GET", path, "", nil, nil); w.Code != 403 {
				t.Fatalf("%s from outside %s while on: %d", path, peer, w.Code)
			}
		}
		if w := browse(peer); w.Code != 403 {
			t.Fatalf("Browse from outside %s: %d", peer, w.Code)
		}
	}
	setDLNA(t, f, false)
	if w := f.rawFrom(homePeer, "GET", "/dlna/device.xml", "", nil, nil); w.Code != 404 {
		t.Fatalf("switched off: %d", w.Code)
	}
}

// Media bodies stream for as long as a player plays, so they run in the
// media-body lane, not the default lane's five-second budget.
func TestDLNAMediaRunsInTheMediaBodyLane(t *testing.T) {
	remotePolicyFixture(t) // builds the live router RoutePattern reads
	for path, want := range map[string]string{"/dlna/media/token.mkv": laneMediaBody, "/dlna/device.xml": laneDefault, "/dlna/control/cds": laneDefault} {
		r := httptest.NewRequest("GET", path, nil)
		pattern := RoutePattern(r)
		if routeLanes[pattern] != want {
			t.Fatalf("%s: pattern %q lane %q, want %q", path, pattern, routeLanes[pattern], want)
		}
	}
}
