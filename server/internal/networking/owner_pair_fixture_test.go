package networking

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// OwnerPairCommitRequestForTest reuses the real transport's ordered wire DTO.
// A map changes JSON field order and the strict Hosted decoder rejects it
// before the cancellation/credential authority check under test.
func OwnerPairCommitRequestForTest(commit Commit) any {
	return commitRequest(Intent{Binding: Binding{OperationID: commit.OperationID, ServerID: commit.ServerID}, ClaimGeneration: commit.ClaimGeneration, CredentialGeneration: commit.CredentialGeneration})
}

// OwnerPairHTTPSForTest controls only CA issuance and the successful route
// observation after the real pair has installed/ACKed its own claim. It does
// not create a claim, credential, registration, approval or authority observation.
// The real TLS listener and signing key are exercised. This is not ACME/DNS.
func OwnerPairHTTPSForTest(t *testing.T, h *ClaimHandler) (origin string, generation int64) {
	t.Helper()
	if h == nil || h.certificates == nil || h.remote == nil {
		t.Fatal("actual networking composition required")
	}
	err := h.runner.Do(context.Background(), func(ctx context.Context) error {
		v, err := h.store.InstalledIntent(ctx)
		if err != nil || !v.InstallationAcknowledged || v.Purpose != "switch" {
			t.Fatal("controlled route preceded genuine installation", err)
		}
		var observations int
		if err := h.store.db.QueryRowContext(ctx, `SELECT count(*) FROM networking_claim_authority`).Scan(&observations); err != nil || observations != 0 {
			t.Fatal("pair fabricated an authority observation", err)
		}
		m, remote := h.certificates, h.remote
		transport := m.transport.client.Transport
		installSyntheticCertificate(t, m, ctx)
		m.transport.client.Transport = transport
		if _, err := m.observedPublicAddress(ctx, "188.68.34.120"); err != nil {
			return err
		}
		config, q, _, err := m.snapshot(ctx)
		if err != nil {
			return err
		}
		host := "188-68-34-120." + fixtureCertificateNamespace + ".portico.direct"
		origin = "https://" + host + ":" + strconv.Itoa(config.PublicPort)
		if err = m.publishRoute(ctx, q, config, certificateRoute{Hostname: host, BaseURL: origin, Namespace: q.Namespace}); err != nil {
			return err
		}
		c, _, authority, state, key, err := remote.snapshot(ctx)
		clear(key)
		if err != nil || !c.Enabled {
			t.Fatal("real installed lifecycle did not enable its managed route", err)
		}
		state.RouteDigest = "explicit-controlled-owner-pair-route-fact"
		at := time.Now().UTC().Add(-time.Millisecond)
		if err = remote.saveState(ctx, authority, c, state, RemoteStatus{AuthorityID: authority, Config: c, Generation: state.Generation, State: "reachable", Candidates: []RemoteCandidate{{BaseURL: origin, Class: "public", State: "reachable", VerifiedAt: &at}}}); err != nil {
			return err
		}
		generation = state.Generation
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		direct, err := NewDirectListener(listener, m)
		if err != nil {
			listener.Close()
			return err
		}
		m.SetListening(listener.Addr().(*net.TCPAddr).Port, true)
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), ReadHeaderTimeout: time.Second}
		done := make(chan struct{})
		go func() { defer close(done); _ = server.Serve(direct) }()
		t.Cleanup(func() { _ = server.Close(); m.SetListening(0, false); <-done })
		peer, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", listener.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: m.roots, ServerName: host})
		if err != nil {
			return err
		}
		return peer.Close()
	})
	if err != nil {
		t.Fatal("controlled HTTPS route failed", err)
	}
	actual, err := h.CurrentManagedHTTPS(context.Background())
	if err != nil || actual != origin {
		t.Fatal("actual serving authority did not expose the controlled route", err)
	}
	return
}
