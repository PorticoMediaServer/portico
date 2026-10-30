package identity_test

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

// Local committed-before-install reconciliation uses the original armed proof,
// actual cancellation writer/hook and a fresh ordinary Local sign-in. Hosted
// FINALIZE/Cancel HTTP acceptance and crash durability still need the MAIN pair.
func TestCapturedOwnerPreinstallCancellationRetiresOnlyItsFamilyAndPermitsFreshRestart(t *testing.T) {
	for _, mode := range []string{"expiry after central commit", "signout after central commit", "lost central commit response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, retrieving := prepareOwnerInstallFixture(t)
			switch mode {
			case "expiry after central commit":
				if _, err := f.db.Exec(`UPDATE identity_owner_completions SET expires_ms=created_ms+1`); err != nil {
					t.Fatal(err)
				}
			case "signout after central commit":
				if err := f.s.LogoutToken(ctx, f.bearer); err != nil {
					t.Fatal(err)
				}
			case "lost central commit response":
				if _, err := f.db.Exec(`UPDATE networking_claim_intents SET stage='finalizing',claim_generation='',credential_generation='' WHERE operation_id=?`, retrieving.OperationID); err != nil {
					t.Fatal(err)
				}
			}
			secret, err := networking.NewSecret([]byte(strings.Repeat("local-result-fixture-", 3)))
			if err != nil {
				t.Fatal(err)
			}
			defer secret.Clear()
			lateInstall := func() error {
				return f.runner.Do(ctx, func(ctx context.Context) error {
					_, err := f.store.Install(ctx, retrieving, networking.Result{Commit: networking.Commit{OperationID: retrieving.OperationID, ServerID: retrieving.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}, Credential: secret})
					return err
				})
			}
			if mode != "lost central commit response" {
				if err = lateInstall(); err == nil {
					t.Fatal("late revoked/expired install accepted")
				}
			}
			var queued networking.Intent
			var saved networking.Cancellation
			err = f.runner.Do(ctx, func(ctx context.Context) error {
				var err error
				queued, saved, err = f.store.QueuePreinstallCancellation(ctx, retrieving.OperationID)
				return err
			})
			if err != nil {
				t.Fatal("original-key denial failed", err)
			}
			if saved.ClaimGeneration != "" || !ed25519.Verify(saved.PublicKey, saved.Proof.Payload, saved.Proof.Signature) {
				t.Fatal("denial changed original intent proof")
			}
			var phase, move, kind string
			var revoked bool
			if err = f.db.QueryRow(`SELECT c.phase,m.stage,s.sign_in_kind,f.revoked FROM identity_owner_completions c JOIN identity_moves m ON m.id=c.move_id JOIN identity_state s JOIN authorization_session_families f ON f.id=c.family_id`).Scan(&phase, &move, &kind, &revoked); err != nil || phase != "cancelled" || move != "cancelled" || kind != "local" || !revoked {
				t.Fatal("denial left captured authority or frozen move", err, phase, move, kind, revoked)
			}
			if err = lateInstall(); err == nil {
				t.Fatal("installation won after cancellation CAS")
			}
			err = f.runner.Do(ctx, func(ctx context.Context) error {
				_, err := f.store.CancelAcknowledged(ctx, queued, saved.RequestID)
				return err
			})
			if err != nil {
				t.Fatal("exact cancellation receipt refused", err)
			}
			web, err := identity.WithIssuingDevice(ctx, identity.DeviceRegistration{InstallationID: f.start.InstallationID, Name: "Owner browser", Platform: "web", App: "portico"}, "127.0.0.1:1234")
			if err != nil {
				t.Fatal(err)
			}
			login, err := f.s.DirectLogin(web, "owner", "Local-password1!")
			if err != nil || login.Session == nil {
				t.Fatal("Local owner cannot start again after cancellation", err)
			}
			p, err := f.s.AuthenticateContext(web, login.Session.AccessToken)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := f.s.PreviewAccountMove(web, login.Session.AccessToken, identity.MovePreviewRequest{To: identity.SignInPortico})
			if err != nil {
				t.Fatal("cancelled move blocks fresh preview", err)
			}
			if _, err = f.s.ConfirmPorticoMove(web, login.Session.AccessToken, identity.MoveConfirmRequest{MoveID: preview.ID, ExpectedRevision: preview.Revision}); err != nil {
				t.Fatal(err)
			}
			var successor networking.Intent
			err = f.runner.Do(ctx, func(ctx context.Context) error {
				var err error
				successor, err = f.store.PreparePurpose(ctx, networking.Binding{OperationID: identity.Token(), ServerID: f.s.ID(), PublicKey: f.key.Public().(ed25519.PublicKey), AccountID: networking.UnboundAccount, LocalGeneration: 1}, networking.LocalOwner{AccountID: p.AccountID, ProfileID: p.ProfileID, Epoch: int64(p.Epoch)}, networking.ClaimPurpose{Purpose: "switch"})
				return err
			})
			if err != nil {
				t.Fatal("fresh family/op cannot prepare successor", err)
			}
			err = f.runner.Do(ctx, func(ctx context.Context) error {
				_, _, err := f.store.QueuePreinstallCancellation(ctx, retrieving.OperationID)
				return err
			})
			if err == nil {
				t.Fatal("old denial replay entered successor")
			}
			var pending string
			if err = f.db.QueryRow(`SELECT pending_operation_id FROM networking_claim_identity`).Scan(&pending); err != nil || pending != successor.OperationID {
				t.Fatal("old denial touched successor", err)
			}
		})
	}
}

func TestCapturedOwnerWithdrawalRetirementFailureRollsBack(t *testing.T) {
	f := newOwnerCaptureFixture(t)
	ctx := context.Background()
	if err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error { return f.capture(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetTerminalRevoker(func(ctx context.Context, tx *sql.Tx, v networking.Intent, _ string) error {
		return f.s.RetireOwnerCompletionForClaimTx(ctx, tx, v.OperationID)
	}); err != nil {
		t.Fatal(err)
	}
	callbackErr := errors.New("retirement refused")
	f.s.OnFamilyRevokedTx = func(context.Context, *sql.Tx, identity.FamilyState) error { return callbackErr }
	withdraw := func() error {
		return f.runner.Do(ctx, func(ctx context.Context) error {
			_, handled, err := f.store.WithdrawReplacement(ctx, f.intent)
			if err == nil && !handled {
				t.Fatal("pending capture not recognized")
			}
			return err
		})
	}
	if err := withdraw(); !errors.Is(err, callbackErr) {
		t.Fatal("retirement refusal not propagated", err)
	}
	var phase, stage string
	var challenge int
	if err := f.db.QueryRow(`SELECT c.phase,i.stage,(SELECT count(*) FROM hosted_identity_challenges) FROM identity_owner_completions c JOIN networking_claim_intents i ON i.operation_id=c.operation_id`).Scan(&phase, &stage, &challenge); err != nil || phase != "prepared" || stage != "prepared" || challenge != 1 {
		t.Fatal("failed withdrawal partially retired", err, phase, stage, challenge)
	}
	if _, err := f.s.Authenticate(f.bearer); err != nil {
		t.Fatal("failed withdrawal revoked owner", err)
	}
	f.s.OnFamilyRevokedTx = nil
	if err := withdraw(); err != nil {
		t.Fatal("same pending withdrawal cannot retry", err)
	}
	if _, err := f.s.Authenticate(f.bearer); err == nil {
		t.Fatal("withdrawn family remained live")
	}
}
