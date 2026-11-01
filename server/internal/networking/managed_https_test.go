package networking

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

// This is a local current-state boundary, using real SQLite, canonical schema,
// the actual installed credential and real key/CSR/X509 validation. The certificate
// issuer and successful route observation are controlled fixtures, not ACME or a
// real Hosted dial-back acceptance claim.
func managedHTTPSFixture(t *testing.T) (*sqlFixture, *ClaimHandler, context.Context, string) {
	t.Helper()
	f, remote, _, _, ctx := contactFixture(t)
	h := remote.h
	h.remote = remote // as ConfigureRemote wires it: managed HTTPS reads Remote access from it
	m := h.certificates
	h.runner = m.runner
	installSyntheticCertificate(t, m, ctx)
	m.SetListening(32500, true)
	f.store.installedGuard = GuardInstalledClaim
	if _, e := f.db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'portico')`); e != nil {
		t.Fatal(e)
	}
	if _, e := m.observedPublicAddress(ctx, "188.68.34.120"); e != nil {
		t.Fatal(e)
	}
	config, q, _, e := m.snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	host := "188-68-34-120." + fixtureCertificateNamespace + ".portico.direct"
	origin := "https://" + host + ":32500"
	if e = m.publishRoute(ctx, q, config, certificateRoute{Hostname: host, BaseURL: origin, Namespace: q.Namespace}); e != nil {
		t.Fatal(e)
	}
	c, _, authority, _, key, e := remote.snapshot(ctx)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	c.Enabled = true
	if e = remote.configure(ctx, authority, c); e != nil {
		t.Fatal(e)
	}
	c, _, authority, state, key, e := remote.snapshot(ctx)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	state.RouteDigest = "controlled-observation-digest"
	now := time.Now().UTC().Add(-time.Second)
	status := RemoteStatus{AuthorityID: authority, Config: c, Generation: state.Generation, State: "reachable", Candidates: []RemoteCandidate{{BaseURL: origin, Class: "public", State: "reachable", VerifiedAt: &now}}}
	if e = remote.saveState(ctx, authority, c, state, status); e != nil {
		t.Fatal(e)
	}
	m.transport.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("read-only address reader made an outbound request")
		return nil, errors.New("unexpected outbound request")
	})
	return f, h, ctx, origin
}
func readManagedHTTPS(t *testing.T, f *sqlFixture, h *ClaimHandler, ctx context.Context, want string) {
	t.Helper()
	var before, after int64
	if e := f.db.QueryRow(`SELECT total_changes()`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	got, e := h.CurrentManagedHTTPS(ctx)
	if e != nil || got != want {
		t.Fatalf("managed HTTPS=%q want=%q error=%v", got, want, e)
	}
	if e = f.db.QueryRow(`SELECT total_changes()`).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if after != before {
		t.Fatalf("address observation wrote to database: %d -> %d", before, after)
	}
}
func TestCurrentManagedHTTPSIsReadOnlyAndRequiresCurrentLiveTuple(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, h, ctx, origin := managedHTTPSFixture(t)
	readManagedHTTPS(t, f, h, ctx, origin)
	var observations int
	if e := f.db.QueryRow(`SELECT count(*) FROM networking_claim_authority`).Scan(&observations); e != nil || observations != 0 {
		t.Fatalf("fresh serving authority required a fabricated observation: count=%d error=%v", observations, e)
	}
	tests := []struct{ name, change, restore string }{
		{"Local kind", `UPDATE identity_state SET sign_in_kind='local'`, `UPDATE identity_state SET sign_in_kind='portico'`},
		{"managed off", `UPDATE identity_managed_route SET enabled=0`, `UPDATE identity_managed_route SET enabled=1`},
		{"TLS off", `UPDATE networking_certificate_settings SET enabled=0`, `UPDATE networking_certificate_settings SET enabled=1`},
		{"not acknowledged", `UPDATE networking_claim_intents SET installation_acknowledged=0`, `UPDATE networking_claim_intents SET installation_acknowledged=1 WHERE stage='installed'`},
		{"remote authority changed", `UPDATE networking_remote_settings SET authority='different'`, `UPDATE networking_remote_settings SET authority=(SELECT scope_id FROM networking_certificate_state)`},
		{"route generation changed", `UPDATE networking_remote_state SET generation=generation+1`, `UPDATE networking_remote_state SET generation=generation-1`},
		{"config revision changed", `UPDATE networking_remote_settings SET revision=revision+1`, `UPDATE networking_remote_settings SET revision=revision-1`},
		{"no route digest", `UPDATE networking_remote_state SET route_digest=''`, `UPDATE networking_remote_state SET route_digest='controlled-observation-digest'`},
		{"detected address changed", `UPDATE networking_certificate_settings SET public_address='188.68.34.121'`, `UPDATE networking_certificate_settings SET public_address='188.68.34.120'`},
		{"public port changed", `UPDATE networking_remote_settings SET public_port=443`, `UPDATE networking_remote_settings SET public_port=32500`},
		{"local generation changed", `UPDATE networking_claim_identity SET reset_generation=reset_generation+1`, `UPDATE networking_claim_identity SET reset_generation=reset_generation-1`},
		{"credential generation changed", `UPDATE networking_claim_credentials SET credential_generation='999'`, `UPDATE networking_claim_credentials SET credential_generation=(SELECT credential_generation FROM networking_claim_intents WHERE networking_claim_intents.operation_id=networking_claim_credentials.operation_id)`},
		{"non-origin route", `UPDATE networking_certificate_state SET route_url=route_url||'/untrusted'`, `UPDATE networking_certificate_state SET route_url=?`},
		{"route error", `UPDATE networking_certificate_state SET route_error_code='route_not_ready'`, `UPDATE networking_certificate_state SET route_error_code=''`},
		{"certificate state not issued", `UPDATE networking_certificates SET state='submitted'`, `UPDATE networking_certificates SET state='issued'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, e := f.db.Exec(tt.change); e != nil {
				t.Fatal(e)
			}
			readManagedHTTPS(t, f, h, ctx, "")
			var args []any
			if tt.name == "non-origin route" {
				args = []any{origin}
			}
			if _, e := f.db.Exec(tt.restore, args...); e != nil {
				t.Fatal(e)
			}
			readManagedHTTPS(t, f, h, ctx, origin)
		})
	}
	t.Run("remote off", func(t *testing.T) {
		useRemoteAccess(h.remote, "off")
		readManagedHTTPS(t, f, h, ctx, "")
		useRemoteAccess(h.remote, "on")
		readManagedHTTPS(t, f, h, ctx, origin)
	})
	t.Run("listener not bound", func(t *testing.T) {
		h.certificates.SetListening(32500, false)
		readManagedHTTPS(t, f, h, ctx, "")
		h.certificates.SetListening(32500, true)
		readManagedHTTPS(t, f, h, ctx, origin)
	})
	t.Run("staging issuer", func(t *testing.T) {
		h.certificates.environment = "staging"
		readManagedHTTPS(t, f, h, ctx, "")
		h.certificates.environment = "production"
		readManagedHTTPS(t, f, h, ctx, origin)
	})
	t.Run("untrusted certificate", func(t *testing.T) {
		old := h.certificates.roots
		h.certificates.roots = x509.NewCertPool()
		readManagedHTTPS(t, f, h, ctx, "")
		h.certificates.roots = old
		readManagedHTTPS(t, f, h, ctx, origin)
	})
	t.Run("missing current credential", func(t *testing.T) {
		f, h, ctx, origin := managedHTTPSFixture(t)
		readManagedHTTPS(t, f, h, ctx, origin)
		if _, e := f.db.Exec(`DELETE FROM networking_claim_credentials`); e != nil {
			t.Fatal(e)
		}
		readManagedHTTPS(t, f, h, ctx, "")
	})
	t.Run("terminal denial remains sticky after expiry", func(t *testing.T) {
		f, h, ctx, origin := managedHTTPSFixture(t)
		readManagedHTTPS(t, f, h, ctx, origin)
		v, e := f.store.InstalledIntent(ctx)
		if e != nil {
			t.Fatal(e)
		}
		proof, verify := authorityFixture(t, v, "revoked", 1)
		consumer, e := NewClaimAuthorityConsumer(f.store, verify, func(context.Context, *sql.Tx, Intent, string) error { return nil })
		if e != nil {
			t.Fatal(e)
		}
		if e := consumer.Apply(ctx, v, proof); e != nil {
			t.Fatal(e)
		}
		readManagedHTTPS(t, f, h, ctx, "")
		if _, e := f.db.Exec(`UPDATE networking_claim_authority SET expires_at='2000-01-01T00:00:00Z'`); e != nil {
			t.Fatal(e)
		}
		readManagedHTTPS(t, f, h, ctx, "")
	})
}
func TestCurrentManagedHTTPSDoesNotPromoteUnverifiedCandidates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, h, ctx, origin := managedHTTPSFixture(t)
	var raw string
	if e := f.db.QueryRow(`SELECT status FROM networking_remote_state`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	tests := []struct {
		name   string
		mutate func(*RemoteStatus)
	}{
		{"LAN", func(v *RemoteStatus) { v.Candidates[0].Class = "lan" }},
		{"manual", func(v *RemoteStatus) { v.Candidates[0].Class = "manual" }},
		{"unknown class", func(v *RemoteStatus) { v.Candidates[0].Class = "other" }},
		{"not proven", func(v *RemoteStatus) { v.Candidates[0].State = "probe_required" }},
		{"no proof time", func(v *RemoteStatus) { v.Candidates[0].VerifiedAt = nil }},
		{"zero proof time", func(v *RemoteStatus) { z := time.Time{}; v.Candidates[0].VerifiedAt = &z }},
		{"future proof time", func(v *RemoteStatus) { z := time.Now().Add(time.Hour); v.Candidates[0].VerifiedAt = &z }},
		{"foreign origin", func(v *RemoteStatus) { v.Candidates[0].BaseURL = "https://foreign.example" }},
		{"foreign authority", func(v *RemoteStatus) { v.AuthorityID = "another" }},
		{"foreign generation", func(v *RemoteStatus) { v.Generation++ }},
		{"foreign revision", func(v *RemoteStatus) { v.Config.Revision++ }},
		{"remote off", func(v *RemoteStatus) { v.Config.Enabled = false }},
		{"other port", func(v *RemoteStatus) { v.Config.PublicPort++ }},
		{"unavailable status", func(v *RemoteStatus) { v.State = "hosted_unavailable" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v RemoteStatus
			if e := json.Unmarshal([]byte(raw), &v); e != nil {
				t.Fatal(e)
			}
			tt.mutate(&v)
			changed, e := json.Marshal(v)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.db.Exec(`UPDATE networking_remote_state SET status=?`, string(changed)); e != nil {
				t.Fatal(e)
			}
			readManagedHTTPS(t, f, h, ctx, "")
			if _, e = f.db.Exec(`UPDATE networking_remote_state SET status=?`, raw); e != nil {
				t.Fatal(e)
			}
			readManagedHTTPS(t, f, h, ctx, origin)
		})
	}
}
