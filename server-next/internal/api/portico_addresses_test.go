package api

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/certclient"
)

// The Portico zone is set only once its certificate can be served: until then it is an
// address still waiting for its certificate, and when the link ends both go.
func TestThePorticoZoneWaitsForItsCertificate(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	zone := "abc123." + certclient.Zone
	stands := func(when, wantZone, wantWaiting string) {
		t.Helper()
		if p.s.porticoZone() != wantZone || p.s.porticoAddress() != wantWaiting {
			t.Errorf("%s: the zone is %q and the address waiting for a certificate is %q, want %q and %q",
				when, p.s.porticoZone(), p.s.porticoAddress(), wantZone, wantWaiting)
		}
	}
	p.s.porticoCertificate(certclient.Status{State: "working", NameSpace: "abc123", Message: "Getting your secure address."})
	stands("while the certificate is on its way", "", zone)
	p.s.porticoCertificate(certclient.Status{State: "issued", NameSpace: "abc123", CertificateReady: true})
	stands("with the certificate installed", zone, "")
	// A renewal that is waiting leaves a certificate that is still good in use.
	p.s.porticoCertificate(certclient.Status{State: "waiting", NameSpace: "abc123", CertificateReady: true})
	stands("while a renewal waits", zone, "")
	p.s.porticoCertificate(certclient.Status{State: "waiting", NameSpace: "abc123"})
	stands("once the certificate has run out", "", zone)
	p.s.porticoCertificate(certclient.Status{State: "issued", NameSpace: "abc123", CertificateReady: true})
	p.s.porticoUnlinked()
	stands("after the link has ended", "", "")
}
