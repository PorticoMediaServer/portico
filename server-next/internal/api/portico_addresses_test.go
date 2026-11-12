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

// Review desk A, #2889. The certificate client can be inside a call to Portico Services when
// the link ends; its report, arriving afterwards, must not bring the zone back.
func TestReviewAALateCertificateReportDoesNotBringTheZoneBackAfterTheLinkEnded(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	p.s.porticoCertificate(certclient.Status{State: "issued", NameSpace: "abc123", CertificateReady: true})
	p.s.porticoUnlinked()
	// The call that was in flight ends: Portico Services was busy, try again later.
	p.s.porticoCertificate(certclient.Status{State: "waiting", NameSpace: "abc123", CertificateReady: true})
	if zone := p.s.porticoZone(); zone != "" {
		t.Errorf("after the link ended a late report set the zone to %q; it stays until the next restart", zone)
	}
	// A new link makes the client's reports count again.
	p.s.porticoLinked(p.clock.Now())
	p.s.porticoCertificate(certclient.Status{State: "issued", NameSpace: "abc123", CertificateReady: true})
	if zone := p.s.porticoZone(); zone == "" {
		t.Error("after linking again a certificate report did not set the zone")
	}
}
