package networking

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func remoteStatusOf(t *testing.T, f *sqlFixture) RemoteStatus {
	t.Helper()
	var raw string
	if e := f.db.QueryRow(`SELECT status FROM networking_remote_state WHERE singleton=1`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var out RemoteStatus
	if e := json.Unmarshal([]byte(raw), &out); e != nil {
		t.Fatal(e)
	}
	return out
}

// routeHostedFake ACKs observations and records actual per-origin proof requests.
func routeHostedFake(t *testing.T, cert *CertificateManager, ctx context.Context) *contactCounts {
	t.Helper()
	counts := &contactCounts{}
	cert.transport.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/routes/prove"):
			var in struct {
				Endpoint struct {
					BaseURL string `json:"baseUrl"`
				} `json:"endpoint"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				t.Fatal(err)
			}
			counts.names++
			body, _ := json.Marshal(EndpointReceipt{BaseURL: in.Endpoint.BaseURL, VerifiedAt: time.Now()})
			return reply(http.StatusOK, string(body)), nil
		case strings.HasSuffix(r.URL.Path, "/routes"):
			var in RouteObservation
			if err := json.Unmarshal(raw, &in); err != nil {
				t.Fatal(err)
			}
			counts.publishes++
			body, _ := json.Marshal(struct {
				Generation int64 `json:"generation,string"`
			}{in.Generation})
			return reply(http.StatusOK, string(body)), nil
		}
		t.Errorf("unexpected Hosted call %s", r.URL.Path)
		return reply(http.StatusServiceUnavailable, `{"code":"unavailable"}`), nil
	})
	return counts
}

func TestIPv6AllowedByDefaultAndDisableOverrideRemovesCandidate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, topo, _, ctx := contactFixture(t)
	cert := m.h.certificates
	m.h.endpoints.pending = map[string]endpointPending{}
	installSyntheticCertificate(t, cert, ctx)
	cert.SetListening(32500, true)
	counts := routeHostedFake(t, cert, ctx)
	config, _, _, _, key, err := m.snapshot(ctx)
	clear(key)
	if err != nil || !config.IPv6Open {
		t.Fatalf("IPv6 default: %+v %v", config, err)
	}
	if _, err = f.db.Exec(`UPDATE networking_remote_settings SET enabled=1,mapping=0`); err != nil {
		t.Fatal(err)
	}
	topo.Public = []string{"2a03:4000:6:8::1"}
	if err = m.step(ctx); err != nil {
		t.Fatal(err)
	}
	status := remoteStatusOf(t, f)
	if status.IPv6 != "verified" || counts.names != 1 {
		t.Fatalf("IPv6 proof: %+v proofs=%d", status, counts.names)
	}
	found := false
	for _, c := range status.Candidates {
		if c.Address == topo.Public[0] {
			found = true
			if c.Port != 32500 || c.State != "reachable" {
				t.Fatalf("candidate %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("missing IPv6 candidate")
	}
	if _, err = f.db.Exec(`UPDATE networking_remote_settings SET ipv6_open=0,revision=revision+1`); err != nil {
		t.Fatal(err)
	}
	if err = m.step(ctx); err != nil {
		t.Fatal(err)
	}
	status = remoteStatusOf(t, f)
	for _, c := range status.Candidates {
		if c.Address == topo.Public[0] {
			t.Fatal("disabled IPv6 still published", c)
		}
	}
	if status.IPv6 != "disabled" || counts.names != 1 {
		t.Fatalf("override %+v proofs=%d", status, counts.names)
	}
}

// IGDv2 pinhole replies use unprefixed argument names, and its error codes map
// onto the mapping errors the manager already understands.
func TestPinholeSOAPReplies(t *testing.T) {
	service := "urn:schemas-upnp-org:service:WANIPv6FirewallControl:1"
	ok := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:AddPinholeResponse xmlns:u="` + service + `"><UniqueID>42</UniqueID></u:AddPinholeResponse></s:Body></s:Envelope>`
	out, e := parseUPnPResponseFields([]byte(ok), 200, service, "AddPinhole", "")
	if e != nil || out["UniqueID"] != "42" {
		t.Fatal(out, e)
	}
	for code, want := range map[string]error{"704": errUPnPMissing, "702": errFirewallDisabled, "703": errMappingDenied} {
		fault := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>` + code + `</errorCode></UPnPError></detail></s:Fault></s:Body></s:Envelope>`
		if _, e = parseUPnPResponseFields([]byte(fault), 500, service, "AddPinhole", ""); e != want {
			t.Fatalf("%s: %v", code, e)
		}
	}
}

type pinholeGateway struct{ opened, closed int }

func (g *pinholeGateway) Prepare(_ context.Context, m Mapping) (Mapping, error) {
	if m.Protocol != pinholeProtocol {
		return m, errMappingUnsupported
	}
	return m, nil
}
func (g *pinholeGateway) Map(_ context.Context, m Mapping, lifetime uint32) (Mapping, error) {
	if lifetime == 0 {
		g.closed++
		m.PinholeID = ""
		return grantedMapping(m, 0, 0)
	}
	g.opened++
	m.PinholeID = "7"
	m.ExternalAddress, m.ExternalPort = m.PinholeClient, m.InternalPort
	return grantedMapping(m, lifetime, 0)
}

// With mapping and UPnP on, the server opens a pinhole for its global IPv6
// address, publishes the address through it, and closes the pinhole when the
// address goes away.
func TestIPv6PinholeOpensPublishesAndCloses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, topo, _, ctx := contactFixture(t)
	cert := m.h.certificates
	m.h.endpoints.pending = map[string]endpointPending{}
	installSyntheticCertificate(t, cert, ctx)
	cert.SetListening(32500, true)
	counts := routeHostedFake(t, cert, ctx)
	gateway := &pinholeGateway{}
	m.mapper = gateway
	if _, _, _, _, key, e := m.snapshot(ctx); e != nil {
		t.Fatal(e)
	} else {
		clear(key)
	}
	if _, e := f.db.Exec(`UPDATE networking_remote_settings SET enabled=1,mapping=1,pcp=0,natpmp=0,upnp=1`); e != nil {
		t.Fatal(e)
	}
	topo.Public = []string{"2a03:4000:6:8::1"}
	_ = m.step(ctx)
	if status := remoteStatusOf(t, f); status.IPv6 != "verified" || gateway.opened != 1 || counts.names == 0 {
		t.Fatalf("ipv6=%q opened=%d mappings=%v", status.IPv6, gateway.opened, status.Mappings)
	}
	topo.Public = []string{}
	_ = m.step(ctx)
	if gateway.closed != 1 {
		t.Fatalf("pinhole for a vanished address was not closed: %d", gateway.closed)
	}
}

type noFirewallControl struct{ probes int }

func (g *noFirewallControl) Prepare(_ context.Context, m Mapping) (Mapping, error) {
	if m.Protocol == pinholeProtocol {
		g.probes++
	}
	return m, errMappingUnsupported
}
func (g *noFirewallControl) Map(_ context.Context, m Mapping, _ uint32) (Mapping, error) {
	return m, errMappingUnsupported
}

// A84: a router without IPv6 firewall control is probed with a doubling
// backoff up to a day, again at once when the address changes, and never when
// the server has no global IPv6 address.
func TestPinholeProbeBacksOffWithoutFirewallControl(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, topo, _, ctx := contactFixture(t)
	cert := m.h.certificates
	m.h.endpoints.pending = map[string]endpointPending{}
	installSyntheticCertificate(t, cert, ctx)
	cert.SetListening(32500, true)
	routeHostedFake(t, cert, ctx)
	gateway := &noFirewallControl{}
	m.mapper = gateway
	now := time.Now()
	m.now = func() time.Time { return now }
	if _, _, _, _, key, e := m.snapshot(ctx); e != nil {
		t.Fatal(e)
	} else {
		clear(key)
	}
	if _, e := f.db.Exec(`UPDATE networking_remote_settings SET enabled=1,mapping=1,pcp=0,natpmp=0,upnp=1`); e != nil {
		t.Fatal(e)
	}
	topo.Public = []string{"188.68.34.120"}
	for range 3 {
		_ = m.step(ctx)
		now = now.Add(time.Hour)
	}
	if gateway.probes != 0 {
		t.Fatalf("probed %d times without a global IPv6 address", gateway.probes)
	}
	topo.Public = []string{"2a03:4000:6:8::1"}
	// Two simulated days in 10-minute steps: 5, 10, 20, 40 min, 1h20, 2h40,
	// 5h20, 10h40, then the 24 h cap gives about ten probes, not 288.
	for range 288 {
		_ = m.step(ctx)
		now = now.Add(10 * time.Minute)
	}
	if gateway.probes < 8 || gateway.probes > 12 {
		t.Fatalf("probes over two days: %d", gateway.probes)
	}
	before := gateway.probes
	topo.Public = []string{"2a03:4000:6:8::2"}
	_ = m.step(ctx)
	if gateway.probes != before+1 {
		t.Fatalf("a new address was not probed at once: %d -> %d", before, gateway.probes)
	}
	if d := pinholeBackoff(40); d < 24*time.Hour || d > 24*time.Hour+144*time.Minute {
		t.Fatalf("cap %v", d)
	}
}

// A88: without PCP, NAT-PMP or UPnP the IPv4 mapping probes back off to a day
// instead of every two minutes; a topology change probes again at once.
func TestMappingProbeBacksOffWithoutGatewaySupport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, topo, _, ctx := contactFixture(t)
	cert := m.h.certificates
	m.h.endpoints.pending = map[string]endpointPending{}
	installSyntheticCertificate(t, cert, ctx)
	cert.SetListening(32500, true)
	routeHostedFake(t, cert, ctx)
	gateway := &natUnsupported{}
	m.mapper = gateway
	now := time.Now()
	m.now = func() time.Time { return now }
	if _, _, _, _, key, e := m.snapshot(ctx); e != nil {
		t.Fatal(e)
	} else {
		clear(key)
	}
	if _, e := f.db.Exec(`UPDATE networking_remote_settings SET enabled=1,mapping=1`); e != nil {
		t.Fatal(e)
	}
	topo.Public = []string{"188.68.34.120"}
	for range 1440 { // two days in two-minute steps
		_ = m.step(ctx)
		now = now.Add(2 * time.Minute)
	}
	if gateway.probes > 60 {
		t.Fatalf("%d gateway probes in two days", gateway.probes)
	}
	before := gateway.probes
	topo.Gateway = "192.168.1.254"
	_ = m.step(ctx)
	if gateway.probes == before {
		t.Fatal("a new gateway was not probed at once")
	}
}

type natUnsupported struct{ probes int }

func (g *natUnsupported) Prepare(_ context.Context, m Mapping) (Mapping, error) {
	if m.Protocol != pinholeProtocol {
		g.probes++
	}
	return m, errMappingUnsupported
}
func (g *natUnsupported) Map(_ context.Context, m Mapping, _ uint32) (Mapping, error) {
	return m, errMappingUnsupported
}
