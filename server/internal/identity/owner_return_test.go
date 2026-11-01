package identity_test

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// The installed credential/remote route here are explicit local facts. These
// tests exercise the real normalized identity writer, not Hosted PG, ACME or
// the future HTTP initiation/exchange composition.
func ownerReturnFixture(t *testing.T) (ownerCaptureFixture, identity.Principal, serverreturn.OwnerExchange, identity.OwnerReturnRoute) {
	t.Helper()
	f, retrieving := prepareOwnerInstallFixture(t)
	ctx := context.Background()
	secret, err := networking.NewSecret([]byte(strings.Repeat("local-result-fixture-", 3)))
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Clear()
	err = f.runner.Do(ctx, func(ctx context.Context) error {
		installed, err := f.store.Install(ctx, retrieving, networking.Result{Commit: networking.Commit{OperationID: retrieving.OperationID, ServerID: retrieving.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}, Credential: secret})
		if err != nil {
			return err
		}
		_, err = f.store.Acknowledged(ctx, installed)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.s.OwnerClaimAuthority(ctx, f.bearer)
	if err != nil {
		t.Fatal(err)
	}
	q := serverreturn.OwnerExchange{TransactionID: "actual_pending_transaction", State: f.start.State, Verifier: f.verifier, InstallationID: f.start.InstallationID, ServerChallenge: f.start.ServerChallenge, Purpose: "switch"}
	if _, err = f.db.Exec(`INSERT INTO networking_remote_state(singleton,generation) VALUES(1,7) ON CONFLICT(singleton) DO UPDATE SET generation=7`); err != nil {
		t.Fatal(err)
	}
	route := identity.OwnerReturnRoute{Registration: serverreturn.Registration{Origin: "https://current.server.example"}, Generation: 7}
	route.Registration.ID = serverreturn.RegistrationID(f.s.ID(), f.intent.OperationID, 1, 1, route.Generation, route.Registration.Origin)
	q.RegistrationID = route.Registration.ID
	return f, p, q, route
}

func bindOwnerReturn(t *testing.T, f ownerCaptureFixture, p identity.Principal, route identity.OwnerReturnRoute) {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		bound, err := f.s.BindOwnerReturnRegistrationTx(context.Background(), tx, p, f.intent.OperationID, route)
		if err == nil && !bound {
			t.Fatal("genuine first registration not bound")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func ownerReturnIdentity(f ownerCaptureFixture, q serverreturn.OwnerExchange) identity.PorticoIdentity {
	return identity.PorticoIdentity{AccountID: "account-approved", Username: "approved_owner", DisplayName: "Approved owner", Consent: "verified-unchanged-envelope-fixture", SignerKeyID: "current-document-key", Return: &identity.PorticoReturnBinding{Purpose: "switch", InstallationID: q.InstallationID, RegistrationID: q.RegistrationID, OperationID: f.intent.OperationID, ClaimGeneration: 1, CredentialGeneration: 1, ExpiresAt: time.Now().Add(45 * time.Second)}}
}

func TestOwnerReturnCancellationRetiresOnlyInstalledContinuation(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "callback rollback"}[rollback], func(t *testing.T) {
			ctx := context.Background()
			f, p, q, route := ownerReturnFixture(t)
			bindOwnerReturn(t, f, p, route)
			callbackErr := errors.New("retirement callback refused")
			if rollback {
				f.s.OnFamilyRevokedTx = func(context.Context, *sql.Tx, identity.FamilyState) error { return callbackErr }
			}
			cancel := func(operation string) error {
				return dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
					return f.s.CancelOwnerReturnTx(ctx, tx, p, operation)
				})
			}
			if err := cancel(identity.Token()); err == nil {
				t.Fatal("different operation cancelled current continuation")
			}
			if err := cancel(f.intent.OperationID); rollback {
				if !errors.Is(err, callbackErr) {
					t.Fatal("retirement failure ignored", err)
				}
				if _, err = f.s.OwnerClaimAuthority(ctx, f.bearer); err != nil {
					t.Fatal("failed denial spent continuation", err)
				}
				f.s.OnFamilyRevokedTx = nil
				if err = cancel(f.intent.OperationID); err != nil {
					t.Fatal("cancel retry", err)
				}
			} else if err != nil {
				t.Fatal("cancel", err)
			}
			var phase, kind, claim, link string
			var revoked, enabled bool
			var credentials, outbox, challenges int
			err := f.db.QueryRow(`SELECT c.phase,s.sign_in_kind,i.stage,l.hosted_account_id,a.revoked,r.enabled,(SELECT count(*) FROM networking_claim_credentials),(SELECT count(*) FROM networking_claim_cancellations),(SELECT count(*) FROM hosted_identity_challenges) FROM identity_owner_completions c JOIN identity_state s JOIN networking_claim_intents i ON i.operation_id=c.operation_id JOIN account_portico_links l ON l.account_id=c.owner_id JOIN authorization_session_families a ON a.id=c.family_id JOIN identity_managed_route r`).Scan(&phase, &kind, &claim, &link, &revoked, &enabled, &credentials, &outbox, &challenges)
			if err != nil || phase != "cancelled" || kind != "portico" || claim != "installed" || link != "account-approved" || !revoked || !enabled || credentials != 1 || outbox != 0 || challenges != 0 {
				t.Fatal("continuation cancellation changed installed authority", err, phase, kind, claim, revoked, enabled, credentials, outbox, challenges)
			}
			if _, err = f.s.OwnerClaimAuthority(ctx, f.bearer); err == nil {
				t.Fatal("cancelled continuation restored")
			}
			who := ownerReturnIdentity(f, q)
			who.Return = nil
			in, err := f.s.PorticoSignIn(ctx, who)
			if err != nil || in.Session == nil || in.Account.ID != f.p.AccountID {
				t.Fatal("fresh ordinary identity lost installed owner", err)
			}
		})
	}
}

func TestOwnerReturnRegistrationFreezesAndSupersedesWithoutLocalRollback(t *testing.T) {
	f, p, _, route := ownerReturnFixture(t)
	bindOwnerReturn(t, f, p, route)
	bindOwnerReturn(t, f, p, route)
	if _, err := f.db.Exec(`UPDATE networking_remote_state SET generation=8`); err != nil {
		t.Fatal(err)
	}
	route.Generation = 8
	route.Registration.ID = serverreturn.RegistrationID(f.s.ID(), f.intent.OperationID, 1, 1, route.Generation, route.Registration.Origin)
	err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		bound, err := f.s.BindOwnerReturnRegistrationTx(context.Background(), tx, p, f.intent.OperationID, route)
		if bound {
			t.Fatal("changed actual generation retargeted transaction")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var phase, kind, registration string
	var revoked bool
	err = f.db.QueryRow(`SELECT c.phase,s.sign_in_kind,c.registration_id,f.revoked FROM identity_owner_completions c JOIN identity_state s JOIN authorization_session_families f ON f.id=c.family_id`).Scan(&phase, &kind, &registration, &revoked)
	if err != nil || phase != "superseded" || kind != "portico" || registration == route.Registration.ID || !revoked {
		t.Fatal("superseded installed state", err, phase, kind, revoked)
	}
}

func TestOwnerReturnAttemptConsumesBeforeExchangeAndAtomicAdmission(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"success", "retirement rollback", "lost response", "revoked during exchange", "wrong approved account", "wrong approved username", "revoked signer", "route changed", "expired during exchange"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, p, q, route := ownerReturnFixture(t)
			bindOwnerReturn(t, f, p, route)
			var operation, attempt string
			err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
				var err error
				operation, attempt, err = f.s.BeginOwnerReturnTx(ctx, tx, p, q, route)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.OwnerClaimAuthority(ctx, f.bearer); err == nil {
				t.Fatal("consumed attempt restored as public authority")
			}
			err = dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
				_, _, err := f.s.BeginOwnerReturnTx(ctx, tx, p, q, route)
				return err
			})
			if err == nil {
				t.Fatal("second exchange admitted")
			}
			err = nil
			if mode == "lost response" {
				err = dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error { return f.s.AbandonOwnerReturnTx(ctx, tx, operation, attempt) })
				if err != nil {
					t.Fatal(err)
				}
				// A fresh ordinary identity can admit only the already-bound owner.
				who := ownerReturnIdentity(f, q)
				who.Return = nil
				in, err := f.s.PorticoSignIn(ctx, who)
				if err != nil || in.Session == nil {
					t.Fatal("ordinary exact bound owner recovery", err)
				}
				return
			}
			who := ownerReturnIdentity(f, q)
			callbackErr := errors.New("retirement failed")
			switch mode {
			case "retirement rollback":
				f.s.OnFamilyRevokedTx = func(context.Context, *sql.Tx, identity.FamilyState) error { return callbackErr }
			case "revoked during exchange":
				_, err = f.db.Exec(`UPDATE authorization_session_families SET revoked=1`)
			case "wrong approved account":
				who.AccountID = "another-actual-account"
			case "wrong approved username":
				who.Username = "different_username"
			case "revoked signer":
				_, err = f.db.Exec(`INSERT INTO hosted_signing_revocations(key_id) VALUES(?)`, who.SignerKeyID)
			case "route changed":
				_, err = f.db.Exec(`UPDATE networking_remote_state SET generation=generation+1`)
			case "expired during exchange":
				_, err = f.db.Exec(`UPDATE identity_owner_completions SET expires_ms=created_ms+1`)
			}
			if err != nil {
				t.Fatal(err)
			}
			nonce := identity.Token()
			var out identity.DirectSignIn
			complete := func() error {
				return dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
					var err error
					out, err = f.s.CompleteOwnerReturnTx(ctx, tx, p, operation, attempt, q, route, who, nonce)
					return err
				})
			}
			err = complete()
			if mode != "success" {
				if err == nil {
					t.Fatal("failed fence admitted owner")
				}
				var phase string
				var challenge, used int
				if e := f.db.QueryRow(`SELECT phase,(SELECT count(*) FROM hosted_identity_challenges),(SELECT count(*) FROM hosted_identity_nonces) FROM identity_owner_completions`).Scan(&phase, &challenge, &used); e != nil || phase != "consumed" || challenge != 1 || used != 0 {
					t.Fatal("failed final writer spent proof", e, phase, challenge, used)
				}
				if mode != "retirement rollback" {
					return
				}
				if !errors.Is(err, callbackErr) {
					t.Fatal(err)
				}
				f.s.OnFamilyRevokedTx = nil
				if err = complete(); err != nil {
					t.Fatal("same in-flight writer retry", err)
				}
			}
			if out.Session == nil || out.Session.AccessToken == f.bearer || out.Account.ID != f.p.AccountID {
				t.Fatal("admission did not preserve bound local owner")
			}
			var phase string
			var revoked bool
			var challenge, used int
			if err = f.db.QueryRow(`SELECT c.phase,f.revoked,(SELECT count(*) FROM hosted_identity_challenges),(SELECT count(*) FROM hosted_identity_nonces) FROM identity_owner_completions c JOIN authorization_session_families f ON f.id=c.family_id`).Scan(&phase, &revoked, &challenge, &used); err != nil || phase != "completed" || !revoked || challenge != 0 || used != 1 {
				t.Fatal("non-atomic completion", err, phase, revoked, challenge, used)
			}
			if err = complete(); err == nil {
				t.Fatal("completed attempt replay issued twice")
			}
		})
	}
}

func TestOwnerReturnRejectsAlteredCallbackAndProofBeforeConsumption(t *testing.T) {
	f, p, q, route := ownerReturnFixture(t)
	bindOwnerReturn(t, f, p, route)
	changes := []func(*serverreturn.OwnerExchange){func(q *serverreturn.OwnerExchange) { q.TransactionID = "other_transaction" }, func(q *serverreturn.OwnerExchange) { q.State = identity.Token() }, func(q *serverreturn.OwnerExchange) { q.Verifier = identity.Token() }, func(q *serverreturn.OwnerExchange) { q.InstallationID = "e297a1dc-3adc-4b1c-a7f2-fbd4640f69e8" }, func(q *serverreturn.OwnerExchange) { q.ServerChallenge = identity.Token() }, func(q *serverreturn.OwnerExchange) { q.RegistrationID = "ret_other" }, func(q *serverreturn.OwnerExchange) { q.Purpose = "sign-in" }, func(q *serverreturn.OwnerExchange) { q.State += "\n" }}
	for i, change := range changes {
		next := q
		change(&next)
		err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
			_, _, e := f.s.BeginOwnerReturnTx(context.Background(), tx, p, next, route)
			return e
		})
		if err == nil {
			t.Fatalf("altered binding %d consumed", i)
		}
	}
	var phase string
	if err := f.db.QueryRow(`SELECT phase FROM identity_owner_completions`).Scan(&phase); err != nil || phase != "installed" {
		t.Fatal("invalid proof spent attempt", err, phase)
	}
}

func TestOwnerReturnComputerResetRetiresAttemptAndUsesExplicitSignedLocalLeave(t *testing.T) {
	f, p, q, route := ownerReturnFixture(t)
	bindOwnerReturn(t, f, p, route)
	ctx, err := identity.WithIssuingDevice(context.Background(), identity.DeviceRegistration{InstallationID: f.start.InstallationID, Name: "Owner browser", Platform: "web", App: "portico"}, "127.0.0.1:1234")
	if err != nil {
		t.Fatal(err)
	}
	var operation, attempt string
	err = dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		var err error
		operation, attempt, err = f.s.BeginOwnerReturnTx(ctx, tx, p, q, route)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	location, err := f.s.RequestOwnerReset(ctx, identity.OwnerResetRequest{Username: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(storagepaths.At(f.root).Keys, location.FileName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(file), "\n")
	clear(file)
	if len(lines) < 4 || len(lines[2]) != 43 {
		t.Fatal("private reset file format")
	}
	reset, err := f.s.VerifyOwnerReset(ctx, identity.OwnerResetVerify{Code: lines[2]})
	lines = nil
	if err != nil || reset.Session == nil || !reset.ResetFileOwnerSession || reset.Session.RefreshToken != "" {
		t.Fatal("actual computer reset did not issue bounded owner session", err)
	}
	if _, err = f.s.OwnerClaimAuthority(ctx, f.bearer); err == nil {
		t.Fatal("reset did not end original owner capability")
	}
	err = dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		_, err := f.s.CompleteOwnerReturnTx(ctx, tx, p, operation, attempt, q, route, ownerReturnIdentity(f, q), identity.Token())
		return err
	})
	if err == nil {
		t.Fatal("reset-revoked attempt admitted owner")
	}
	var kind, linked string
	if err = f.db.QueryRow(`SELECT s.sign_in_kind,l.hosted_account_id FROM identity_state s JOIN account_portico_links l ON l.account_id=?`, p.AccountID).Scan(&kind, &linked); err != nil || kind != "portico" || linked != "account-approved" {
		t.Fatal("reset silently undid installed claim", err, kind)
	}
	// The exact local fixture server key signs the deny-only leave contract.
	// Actual ProtectedKeys signing/delivery is covered by the networking/Hosted
	// boundary tests; this case checks reset -> explicit move -> durable outbox.
	f.s.PrepareIdentityDeparture = func(ctx context.Context, reason string) (*identity.DepartureNotice, error) {
		wire := identity.DepartureWire{Kind: "portico.server.leave", Version: "1", Audience: "https://hosted.example", OperationID: f.intent.OperationID, ServerID: f.s.ID(), PublicKey: base64.RawURLEncoding.EncodeToString(f.key.Public().(ed25519.PublicKey)), ClaimGeneration: "1", CredentialGeneration: "1", NoticeID: identity.Token(), Reason: reason}
		raw, err := json.Marshal(wire)
		if err != nil {
			return nil, err
		}
		return &identity.DepartureNotice{Wire: wire, Payload: raw, Signature: ed25519.Sign(f.key, raw)}, nil
	}
	move, err := f.s.PreviewAccountMove(ctx, reset.Session.AccessToken, identity.MovePreviewRequest{To: identity.SignInLocal})
	if err != nil {
		t.Fatal("reset owner cannot preview explicit Local recovery", err)
	}
	in, err := f.s.ConfirmLocalMove(ctx, reset.Session.AccessToken, identity.MoveConfirmRequest{MoveID: move.ID, ExpectedRevision: move.Revision, Username: "restored_owner", Password: "Fresh-password1!"})
	if err != nil || in.Session == nil {
		t.Fatal("explicit local recovery failed", err)
	}
	var notices, credentials int
	if err = f.db.QueryRow(`SELECT s.sign_in_kind,(SELECT count(*) FROM identity_service_outbox WHERE delivered_ms=0),(SELECT count(*) FROM networking_claim_credentials) FROM identity_state s`).Scan(&kind, &notices, &credentials); err != nil || kind != "local" || notices != 1 || credentials != 0 {
		t.Fatal("explicit leave/outbox not atomic", err, kind, notices, credentials)
	}
}
