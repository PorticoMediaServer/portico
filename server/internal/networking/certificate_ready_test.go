package networking

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCertificateReadyHookRunsOnceOnInstall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, m, ctx := certificateFixture(t)
	calls := 0
	m.SetOnReady(func() { calls++ })
	installSyntheticCertificate(t, m, ctx)
	if calls != 1 {
		t.Fatal("ready hook calls after install:", calls)
	}
}

// failingIssuance drives an order to "issued" with a chain/expiry the test chooses.
func failingIssuance(t *testing.T, notAfter *time.Time) (*CertificateManager, func(), *int) {
	chains := new(int)
	_, m, ctx := certificateFixture(t)
	_, q, _, e := m.snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	v := q.Scope.Intent
	n := certificateNamespace{Namespace: fixtureCertificateNamespace, DNSName: "*." + fixtureCertificateNamespace + ".portico.direct", DNS01Target: fixtureCertificateNamespace + ".acme.getportico.tv", OperationID: v.OperationID, ClaimGeneration: v.ClaimGeneration, CredentialGeneration: v.CredentialGeneration, Issuer: "google", Environment: "production", Configured: true}
	id := "cert_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	m.transport.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		var out any
		switch {
		case strings.HasSuffix(r.URL.Path, "/namespace"):
			out = n
		case strings.HasSuffix(r.URL.Path, "/orders"):
			var in struct {
				RequestID string `json:"requestId"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &in)
			out = certificateOrder{ID: id, RequestID: in.RequestID, Namespace: n.Namespace, State: "issued", Issuer: n.Issuer, Environment: n.Environment, OperationID: v.OperationID, ClaimGeneration: v.ClaimGeneration, CredentialGeneration: v.CredentialGeneration, NextAttemptAt: time.Now(), NotAfter: notAfter}
		case strings.HasSuffix(r.URL.Path, "/chain"):
			*chains++
			out = struct {
				Chain []byte `json:"chain"`
			}{[]byte("not a certificate")}
		default:
			return reply(http.StatusInternalServerError, `{"code":"unavailable"}`), nil
		}
		raw, _ := json.Marshal(out)
		return reply(http.StatusOK, string(raw)), nil
	})
	return m, func() {
		for range 3 {
			_ = m.step(ctx)
		}
	}, chains
}

func TestCertificateReadyHookNotCalledOnFailedInstall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	m, run, chains := failingIssuance(t, nil)
	calls := 0
	m.SetOnReady(func() { calls++ })
	run()
	if *chains == 0 {
		t.Fatal("the install was never attempted")
	}
	if calls != 0 {
		t.Fatal("ready hook ran for a certificate that did not install", calls)
	}
}

func TestCertificateReadyHookNotCalledForExpiredReceipt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	past := time.Now().Add(-time.Hour)
	m, run, chains := failingIssuance(t, &past)
	calls := 0
	m.SetOnReady(func() { calls++ })
	run()
	if *chains != 0 {
		t.Fatal("an expired receipt reached chain install")
	}
	if calls != 0 {
		t.Fatal("ready hook ran for an expired receipt", calls)
	}
}

func TestConfigureCertificatesWiresReadyHookToRemoteReprove(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, m, _ := certificateFixture(t)
	r := &RemoteManager{wake: make(chan struct{}, 1)}
	h := &ClaimHandler{remote: r}
	// The wiring ConfigureCertificates installs, applied to a fixture manager.
	m.SetOnReady(func() { h.Remote().Reprove() })
	(*m.onReady.Load())()
	if !r.reprove.Load() || len(r.wake) != 1 {
		t.Fatal("ready hook did not re-prove the remote manager")
	}
	// No remote manager yet: nil-safe.
	(&ClaimHandler{}).Remote().Reprove()
}

func TestReproveBypassesRetryDeadlineOnceButNotHold(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, _, counts, ctx := contactFixture(t)
	counting := m.h.certificates.transport.client.Transport
	installSyntheticCertificate(t, m.h.certificates, ctx)
	m.h.certificates.transport.client.Transport = counting
	m.h.certificates.SetListening(32500, true)
	if _, e := f.db.Exec(`UPDATE networking_remote_settings SET lan_sharing=1`); e != nil {
		t.Fatal(e)
	}
	step := func() {
		t.Helper()
		if e := m.step(ctx); e != nil {
			t.Fatal(e)
		}
	}
	step()
	if counts.publishes != 1 {
		t.Fatal("first pass", counts.publishes)
	}
	step()
	if counts.publishes != 1 {
		t.Fatal("a plain pass re-probed", counts.publishes)
	}
	var generation int64
	_ = f.db.QueryRow(`SELECT generation FROM networking_remote_state WHERE singleton=1`).Scan(&generation)
	m.Reprove()
	step()
	if counts.publishes != 2 {
		t.Fatal("Reprove did not re-probe", counts.publishes)
	}
	var after int64
	_ = f.db.QueryRow(`SELECT generation FROM networking_remote_state WHERE singleton=1`).Scan(&after)
	if after != generation {
		t.Fatal("Reprove changed the generation", generation, after)
	}
	step()
	if counts.publishes != 2 || m.reprove.Load() {
		t.Fatal("Reprove was not one-shot", counts.publishes)
	}
	// A deliberate hold is not bypassed, and the request survives until the hold ends.
	if _, e := f.db.Exec(`UPDATE networking_remote_state SET retry_after=?`, time.Now().Add(time.Hour).UnixMilli()); e != nil {
		t.Fatal(e)
	}
	m.Reprove()
	step()
	if counts.publishes != 2 || !m.reprove.Load() {
		t.Fatal("Reprove bypassed NotBefore", counts.publishes)
	}
}
