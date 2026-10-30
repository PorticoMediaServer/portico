package identity_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

// Production approval verification and the actual store installation writer
// meet here. The committed Result is a local fixture, not a Hosted HTTP/DB
// producer, certificate, browser or cross-repository acceptance claim.
func TestOwnerInstallBindsApprovedIdentityAndNarrowsOnlyAfterAtomicCommit(t *testing.T) {
	for _, mode := range []string{"success", "callback rollback", "expired after commit"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, retrieving := prepareOwnerInstallFixture(t)
			var err error
			if mode == "expired after commit" {
				if _, err = f.db.Exec(`UPDATE identity_owner_completions SET expires_ms=created_ms+1`); err != nil {
					t.Fatal(err)
				}
			}
			callbackError := errors.New("retirement callback refused")
			if mode == "callback rollback" {
				f.s.OnFamilyRevokedTx = func(context.Context, *sql.Tx, identity.FamilyState) error { return callbackError }
			}
			credential, err := networking.NewSecret([]byte(strings.Repeat("local-result-fixture-", 3)))
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Clear()
			install := func() error {
				return f.runner.Do(ctx, func(ctx context.Context) error {
					_, err := f.store.Install(ctx, retrieving, networking.Result{Commit: networking.Commit{OperationID: retrieving.OperationID, ServerID: retrieving.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}, Credential: credential})
					return err
				})
			}
			err = install()
			if mode != "success" {
				if err == nil {
					t.Fatal("failed guard/callback installed")
				}
				var kind, phase string
				var links, credentials, reserved int
				e := f.db.QueryRow(`SELECT s.sign_in_kind,c.phase,(SELECT count(*) FROM account_portico_links),(SELECT count(*) FROM networking_claim_credentials),i.owner_change_reserved FROM identity_state s JOIN identity_owner_completions c JOIN networking_claim_intents i ON i.operation_id=c.operation_id WHERE s.singleton=1`).Scan(&kind, &phase, &links, &credentials, &reserved)
				if e != nil || kind != "local" || phase != "prepared" || links != 0 || credentials != 0 || reserved != 1 {
					t.Fatal("failed install did not restore complete tuple", e, kind, phase, links, credentials, reserved)
				}
				if mode == "expired after commit" {
					return
				}
				if !errors.Is(err, callbackError) {
					t.Fatal(err)
				}
				f.s.OnFamilyRevokedTx = nil
				if err = install(); err != nil {
					t.Fatal("same committed operation could not retry local install", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var kind, phase, username, linked, grant string
			var reserved, refresh int
			err = f.db.QueryRow(`SELECT s.sign_in_kind,c.phase,a.username,l.hosted_account_id,g.grant_kind,i.owner_change_reserved,(SELECT count(*) FROM identity_refresh_credentials r WHERE r.family_id=c.family_id) FROM identity_state s JOIN identity_owner_completions c JOIN accounts a ON a.id=c.owner_id JOIN account_portico_links l ON l.account_id=a.id JOIN authorization_family_grants g ON g.family_id=c.family_id JOIN networking_claim_intents i ON i.operation_id=c.operation_id WHERE s.singleton=1`).Scan(&kind, &phase, &username, &linked, &grant, &reserved, &refresh)
			if err != nil || kind != "portico" || phase != "installed" || username != "approved_owner" || linked != "account-approved" || grant != identity.GrantOwnerClaimCompletion || reserved != 0 || refresh != 0 {
				t.Fatal("installed owner tuple", err, kind, phase, username, linked, grant, reserved, refresh)
			}
			if _, err = f.s.Authenticate(f.bearer); err == nil {
				t.Fatal("initiating family retained general authority")
			}
			if _, err = f.s.OwnerClaimAuthority(ctx, f.bearer); err != nil {
				t.Fatal("captured completion authority lost", err)
			}
		})
	}
}

// prepareOwnerInstallFixture uses the production signed approval verifier and
// actual claim-store progression. Result credentials remain a local fixture.
func prepareOwnerInstallFixture(t *testing.T) (ownerCaptureFixture, networking.Intent) {
	t.Helper()
	ctx := context.Background()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verify, err := networking.NewApprovalVerifier("https://hosted.example", trust.KeyID(pub), pub)
	if err != nil {
		t.Fatal(err)
	}
	f := newOwnerCaptureFixture(t, verify)
	err = dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if err := f.capture(ctx, tx); err != nil {
			return err
		}
		return f.s.BindOwnerClaimTransactionTx(ctx, tx, f.p, f.intent.OperationID, "actual_pending_transaction")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.SetInstallationHooks(func(ctx context.Context, tx *sql.Tx, v networking.Intent) error {
		return f.s.InstallOwnerClaimTx(ctx, tx, v.OperationID)
	}, func() {}); err != nil {
		t.Fatal(err)
	}
	if err = f.store.SetTerminalRevoker(func(ctx context.Context, tx *sql.Tx, v networking.Intent, _ string) error {
		return f.s.RetireOwnerCompletionForClaimTx(ctx, tx, v.OperationID)
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// Exact production approval order; the fixture signature is checked by
	// NewApprovalVerifier, rather than bypassing the authenticated facts.
	raw := []byte(fmt.Sprintf(`{"targetUsername":"approved_owner","kind":"portico.claim.approval","version":"2","audience":"https://hosted.example","operationId":%q,"serverId":%q,"accountId":"account-approved","publicKey":%q,"localGeneration":"0","approvalRevision":"1","purpose":"switch","currentOwnerAccountId":"","previousOwnerRole":"","returnRegistrationId":"","issuedAt":%q,"expiresAt":%q}`, f.intent.OperationID, f.intent.ServerID, f.claim.PublicKey, now.Format(time.RFC3339Nano), now.Add(4*time.Minute).Format(time.RFC3339Nano)))
	cert, err := trust.Certify(key, pub, "pin1", "documents", now.Add(-time.Hour), now.Add(time.Hour), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	proof, _ := json.Marshal(trust.Envelope{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw)), KeyID: "pin1", Certificate: cert})
	var retrieving networking.Intent
	err = f.runner.Do(ctx, func(ctx context.Context) error {
		v, err := f.store.Approve(ctx, f.intent, proof)
		if err != nil {
			return err
		}
		request := identity.Token()
		cancelRaw := []byte(fmt.Sprintf(`{"kind":"portico.claim.cancel","version":"1","audience":"https://hosted.example","operationId":%q,"serverId":%q,"accountId":%q,"publicKey":%q,"localGeneration":"%d","requestId":%q,"mode":"intent"}`, v.OperationID, v.ServerID, v.AccountID, base64.RawURLEncoding.EncodeToString(v.PublicKey), v.LocalGeneration, request))
		if _, err = f.store.ArmPreinstallCancellation(ctx, v, networking.Cancellation{Binding: v.Binding, RequestID: request, Proof: networking.SignedProof{Payload: cancelRaw, Signature: ed25519.Sign(f.key, cancelRaw)}}); err != nil {
			return err
		}
		v, err = f.store.BeginFinalizing(ctx, v)
		if err != nil {
			return err
		}
		retrieving, err = f.store.Committed(ctx, v, networking.Commit{OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, retrieving
}
