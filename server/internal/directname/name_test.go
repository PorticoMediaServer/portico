package directname

import (
	"crypto/x509"
	"net/netip"
	"testing"
)

func TestCanonicalNamesMatchWildcardAndRoundTrip(t *testing.T) {
	const ns = "ptc-aaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct{ ip, label string }{{"188.68.34.120", "188-68-34-120"}, {"192.168.1.20", "192-168-1-20"}, {"2605:a144:2327:3137::1", "2605-a144-2327-3137--1"}, {"fd00::20", "fd00--20"}, {"2605::", "2605-0-0-0-0-0-0-0"}, {"::2", "0-0-0-0-0-0-0-2"}} {
		ip := netip.MustParseAddr(tc.ip)
		if EncodeIP(ip) != tc.label {
			t.Fatal(ip, EncodeIP(ip))
		}
		for _, domain := range []string{"portico.direct", "direct.example.org"} {
			name := IPHostname(ip, ns, domain)
			decoded, namespace, err := ParseIPHostname(name, domain)
			if err != nil || decoded != ip || namespace != ns {
				t.Fatal(name, decoded, namespace, err)
			}
			if err := (&x509.Certificate{DNSNames: []string{"*." + ns + "." + domain}}).VerifyHostname(name); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, label := range []string{"ns1", "ns2", "192-168-01-1", "2605-0-0-0-0-0-0-1", "127-0-0-1", "0-0-0-0", "255-255-255-255", "ff02--1", "fe80--1"} {
		if _, err := DecodeIP(label); err == nil {
			t.Fatal("noncanonical or unsafe label", label)
		}
	}
	if ValidNamespace("ns1") || ValidNamespace("ns2") {
		t.Fatal("authority names collide with namespaces")
	}
}
