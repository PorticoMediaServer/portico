package networking

import (
	"crypto/x509"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestImmutableRouteNamesKeepFamilyPortsAndNeedNoHostedEffect(t *testing.T) {
	const ns = "ptc-aaaaaaaaaaaaaaaaaaaa"
	// No store, transport or issuer is available: route construction is pure.
	m := &RemoteManager{h: &ClaimHandler{certificates: &CertificateManager{directDomain: "portico.direct"}}}
	q := certificateState{Namespace: ns}
	for _, tc := range []struct {
		ip    string
		port  int
		label string
	}{{"188.68.34.120", 42424, "188-68-34-120"}, {"2605:a144:2327:3137::1", 32500, "2605-a144-2327-3137--1"}, {"192.168.1.20", 32500, "192-168-1-20"}, {"192.168.1.21", 443, "192-168-1-21"}} {
		first, err := m.routeName(q, tc.ip, tc.port)
		if err != nil {
			t.Fatal(err)
		}
		restarted := &RemoteManager{h: m.h}
		again, err := restarted.routeName(q, tc.ip, tc.port)
		if err != nil || first != again {
			t.Fatal("restart changed immutable route", first, again, err)
		}
		u, err := url.Parse(first)
		if err != nil || u.Hostname() != tc.label+"."+ns+".portico.direct" {
			t.Fatal(first, err)
		}
		if (tc.port == 443) != (u.Port() == "") {
			t.Fatal("default port not canonical", first)
		}
		if tc.port != 443 && !strings.HasSuffix(first, ":"+strconv.Itoa(tc.port)) {
			t.Fatal("wrong family port", first)
		}
		if err := (&x509.Certificate{DNSNames: []string{"*." + ns + ".portico.direct"}}).VerifyHostname(u.Hostname()); err != nil {
			t.Fatal(err)
		}
	}
}
