package networking

import (
	"crypto/tls"
	"net"
	"testing"
)

// A58: a full handshake is fenced once, by GetCertificate; only a resumed
// session, which skipped GetCertificate, is fenced again in VerifyConnection.
func TestVerifyConnectionFencesOnlyResumedSessions(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	manager := &CertificateManager{bootError: "no authority"}
	l, err := NewDirectListener(inner, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err = l.config.VerifyConnection(tls.ConnectionState{ServerName: "c-x.ns.portico.direct"}); err != nil {
		t.Fatal("full handshake fenced twice:", err)
	}
	if err = l.config.VerifyConnection(tls.ConnectionState{ServerName: "c-x.ns.portico.direct", DidResume: true}); err == nil {
		t.Fatal("resumed session skipped the authority fence")
	}
}
