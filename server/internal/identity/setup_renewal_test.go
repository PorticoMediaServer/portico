package identity_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

// A certificate issued after several attempts can outlive the five-minute setup start.
// The same pending setup may then renew its start once the claim has installed; owner
// admission stays the setup return and completeAccountSetupTx, for the claim's account.

func (f setupContinuationFixture) expireStart(t *testing.T) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE identity_setup_completions SET created_ms=created_ms-1200000,expires_ms=expires_ms-1200000`); err != nil {
		t.Fatal(err)
	}
}

type renewal struct {
	original, proof claimweb.Signed
	start           claimweb.SetupStart
	verifier        string
}

func (f setupContinuationFixture) renewal(t *testing.T, change func(*claimweb.Request, *claimweb.SetupStart)) renewal {
	t.Helper()
	now := time.Now().UTC()
	claim := f.claim
	claim.IssuedAt, claim.ExpiresAt = now, now.Add(10*time.Minute)
	start := f.start
	verifier := identity.Token()
	pkce := sha256.Sum256([]byte(verifier))
	start.State, start.CodeChallenge = identity.Token(), base64.RawURLEncoding.EncodeToString(pkce[:])
	start.InstallationID, start.ServerChallenge = identity.Token(), identity.Token()
	start.IssuedAt, start.ExpiresAt = now, now.Add(claimweb.SetupStartLifetime)
	if change != nil {
		change(&claim, &start)
	}
	original := ownerCaptureSigned(t, f.key, claim)
	raw, _ := base64.RawURLEncoding.DecodeString(original.Payload)
	hash := sha256.Sum256(raw)
	start.ClaimHash = base64.RawURLEncoding.EncodeToString(hash[:])
	return renewal{original: original, proof: ownerCaptureSigned(t, f.key, start), start: start, verifier: verifier}
}

func (f setupContinuationFixture) renew(r renewal, transaction string) error {
	return f.write(func(tx *sql.Tx) error {
		if err := f.s.RenewSetupClaimTx(context.Background(), tx, f.pending, f.proofHash, r.original, r.proof, f.claim.Audience); err != nil {
			return err
		}
		return f.s.BindSetupClaimTransactionTx(context.Background(), tx, f.pending, f.proofHash, f.intent.OperationID, transaction)
	})
}

func TestSetupRenewalAfterSlowInstallAdmitsOnlyTheClaimAccount(t *testing.T) {
	for _, mode := range []string{"installed before expiry", "expired before install", "wrong account"} {
		t.Run(mode, func(t *testing.T) {
			f := newSetupContinuationFixture(t)
			f.capture(t)
			f.install(t)
			ctx := context.Background()
			var old serverreturn.SetupExchange
			if mode != "expired before install" {
				old = f.installed(t)
			}
			f.expireStart(t)
			if mode != "expired before install" {
				if err := f.write(func(tx *sql.Tx) error { _, _, e := f.s.BeginSetupReturnTx(ctx, tx, f.pending, f.proofHash, old); return e }); err == nil {
					t.Fatal("expired start still exchanged")
				}
			}
			r := f.renewal(t, nil)
			if err := f.renew(r, "renewed_setup_transaction"); err != nil {
				t.Fatal("renewal", err)
			}
			if mode == "expired before install" {
				// The installed claim now marks the live renewed start installed.
				f.installed(t)
			}
			q := serverreturn.SetupExchange{TransactionID: "renewed_setup_transaction", State: r.start.State, Verifier: r.verifier, InstallationID: r.start.InstallationID, ServerChallenge: r.start.ServerChallenge, Purpose: "setup"}
			// The original browser binding can't use the renewed start.
			stale := q
			stale.State, stale.Verifier = f.start.State, f.verifier
			if err := f.write(func(tx *sql.Tx) error { _, _, e := f.s.BeginSetupReturnTx(ctx, tx, f.pending, f.proofHash, stale); return e }); err == nil {
				t.Fatal("old binding used the renewed start")
			}
			var operation, attempt string
			if err := f.write(func(tx *sql.Tx) error {
				var e error
				operation, attempt, e = f.s.BeginSetupReturnTx(ctx, tx, f.pending, f.proofHash, q)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			who := identity.PorticoIdentity{AccountID: "account-approved", Username: "actual_setup_owner", DisplayName: "Actual setup owner", Consent: "verified-identity-fixture", SignerKeyID: "current-document-key",
				Return: &identity.PorticoReturnBinding{Purpose: "setup", InstallationID: q.InstallationID, RegistrationID: "", OperationID: operation, ClaimGeneration: 1, CredentialGeneration: 1, ExpiresAt: time.Now().Add(45 * time.Second)}}
			if mode == "wrong account" {
				who.AccountID = "another-account"
			}
			var out identity.DirectSignIn
			err := f.write(func(tx *sql.Tx) error {
				var e error
				out, e = f.s.CompleteSetupReturnTx(ctx, tx, f.pending, f.proofHash, operation, attempt, q, who, identity.Token())
				return e
			})
			if mode == "wrong account" {
				var accounts int
				if err == nil || f.db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&accounts) != nil || accounts != 0 {
					t.Fatal("a renewed start admitted another account")
				}
				return
			}
			if err != nil || out.Session == nil || out.Account.Role != identity.TierOwner || out.Account.HostedAccountID != "account-approved" {
				t.Fatal("renewed setup owner admission", err)
			}
		})
	}
}

func TestSetupRenewalRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	cases := []struct {
		name    string
		prepare func(t *testing.T, f setupContinuationFixture)
		change  func(*claimweb.Request, *claimweb.SetupStart)
	}{
		{"live start", func(t *testing.T, f setupContinuationFixture) { f.install(t) }, nil},
		{"not installed", func(t *testing.T, f setupContinuationFixture) { f.expireStart(t) }, nil},
		{"consumed start", func(t *testing.T, f setupContinuationFixture) {
			f.install(t)
			q := f.installed(t)
			if err := f.write(func(tx *sql.Tx) error {
				_, _, e := f.s.BeginSetupReturnTx(context.Background(), tx, f.pending, f.proofHash, q)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			f.expireStart(t)
		}, nil},
		{"changed name", func(t *testing.T, f setupContinuationFixture) { f.install(t); f.expireStart(t) },
			func(c *claimweb.Request, _ *claimweb.SetupStart) { c.Name = "Another server" }},
		{"changed address", func(t *testing.T, f setupContinuationFixture) { f.install(t); f.expireStart(t) },
			func(c *claimweb.Request, s *claimweb.SetupStart) { c.Address, s.Address = "https://elsewhere.example", "https://elsewhere.example" }},
		{"another setup revision", func(t *testing.T, f setupContinuationFixture) { f.install(t); f.expireStart(t) },
			func(_ *claimweb.Request, s *claimweb.SetupStart) { s.SetupRevision++ }},
		{"replaced setup", func(t *testing.T, f setupContinuationFixture) {
			f.install(t)
			f.expireStart(t)
			if _, err := f.db.Exec(`UPDATE onboarding_state_v1 SET revision=revision+1`); err != nil {
				t.Fatal(err)
			}
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSetupContinuationFixture(t)
			f.capture(t)
			c.prepare(t, f)
			var before []byte
			if err := f.db.QueryRow(`SELECT start_payload FROM identity_setup_completions`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := f.renew(f.renewal(t, c.change), "renewed_setup_transaction"); err == nil {
				t.Fatal("renewal accepted")
			}
			var after []byte
			if err := f.db.QueryRow(`SELECT start_payload FROM identity_setup_completions`).Scan(&after); err != nil || string(after) != string(before) {
				t.Fatal("a refused renewal changed the start", err)
			}
		})
	}
}
