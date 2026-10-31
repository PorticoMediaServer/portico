package identity_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

// rotate performs a real refresh of the fixture's browser family and returns the
// owner principal of the new access token.
func (f *ownerCaptureFixture) rotate(t *testing.T) identity.Principal {
	t.Helper()
	ctx := context.Background()
	if f.refresh == "" {
		t.Fatal("fixture session has no refresh credential")
	}
	renewed, err := f.s.RefreshSession(ctx, f.refresh, f.installation, identity.Token())
	if err != nil {
		t.Fatal("real refresh", err)
	}
	f.refresh = renewed.RefreshToken
	p, err := f.s.AuthenticateContext(ctx, renewed.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if p.Hash == f.p.Hash {
		t.Fatal("refresh did not rotate the access token")
	}
	return p
}

// R-01: the move binds to the owner's family, not the token captured at prepare.
func TestOwnerMoveFollowsSameFamilyRotation(t *testing.T) {
	f := newOwnerCaptureFixture(t)
	ctx := context.Background()
	if err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if err := f.capture(ctx, tx); err != nil {
			return err
		}
		return f.s.BindOwnerClaimTransactionTx(ctx, tx, f.p, f.intent.OperationID, "actual_pending_transaction")
	}); err != nil {
		t.Fatal(err)
	}
	old := f.p
	rotated := f.rotate(t)
	twice := f.rotate(t)
	err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if err := f.s.CheckOwnerClaimTx(ctx, tx, rotated, f.intent.OperationID); !errors.Is(err, identity.ErrUnauthorized) {
			t.Fatal("a superseded token of the family still authorizes", err)
		}
		if err := f.s.CheckOwnerClaimTx(ctx, tx, twice, f.intent.OperationID); err != nil {
			t.Fatal("the family's current token lost its move", err)
		}
		if err := f.s.CheckClaimRequestOwnerTx(ctx, tx, twice); err != nil {
			t.Fatal("the claim edge did not find the rotated family's move", err)
		}
		if op, err := f.s.OwnerClaimOperationTx(ctx, tx, twice); err != nil || op != f.intent.OperationID {
			t.Fatal("restart lookup lost the move", op, err)
		}
		if err := f.s.BindOwnerClaimTransactionTx(ctx, tx, twice, f.intent.OperationID, "actual_pending_transaction"); err != nil {
			t.Fatal("exact transaction replay after rotation", err)
		}
		// The original token stays narrowed to its own move: no fallback to
		// general owner authority on the claim edge.
		if err := f.s.CheckClaimRequestOwnerTx(ctx, tx, old); !errors.Is(err, identity.ErrUnauthorized) {
			t.Fatal("the original token fell back to general owner authority", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A revoked family still refuses.
	if _, err = f.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM identity_owner_completions)`); err != nil {
		t.Fatal(err)
	}
	err = dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		return f.s.CheckOwnerClaimTx(ctx, tx, twice, f.intent.OperationID)
	})
	if !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("a revoked family kept its move", err)
	}
}

// The installation hook has no caller: a closed tab stops refreshing, and the
// approved move must still install. A revoked family must not.
func TestOwnerInstallDoesNotNeedALiveAccessToken(t *testing.T) {
	for _, mode := range []string{"expired token", "rotated, then expired", "revoked family"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, retrieving := prepareOwnerInstallFixture(t)
			if mode == "rotated, then expired" {
				f.rotate(t)
			}
			mutation := `UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE family_id=(SELECT family_id FROM identity_owner_completions)`
			if mode == "revoked family" {
				mutation = `UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM identity_owner_completions)`
			}
			if _, err := f.db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			credential, err := networking.NewSecret([]byte(strings.Repeat("local-result-fixture-", 3)))
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Clear()
			err = f.runner.Do(ctx, func(ctx context.Context) error {
				_, err := f.store.Install(ctx, retrieving, networking.Result{Commit: networking.Commit{OperationID: retrieving.OperationID, ServerID: retrieving.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}, Credential: credential})
				return err
			})
			var phase string
			if e := f.db.QueryRow(`SELECT phase FROM identity_owner_completions`).Scan(&phase); e != nil {
				t.Fatal(e)
			}
			if mode == "revoked family" {
				if err == nil || phase != "prepared" {
					t.Fatal("a revoked family installed", err, phase)
				}
				return
			}
			if err != nil || phase != "installed" {
				t.Fatal("an approved move was stranded by an idle tab", err, phase)
			}
			// Narrowing caps the family's current token, whichever generation it is.
			var current int64
			var expires string
			if e := f.db.QueryRow(`SELECT f.current_generation,t.expires_at FROM authorization_session_families f JOIN authorization_family_tokens t ON t.family_id=f.id AND t.generation=f.current_generation WHERE f.id=(SELECT family_id FROM identity_owner_completions)`).Scan(&current, &expires); e != nil {
				t.Fatal(e)
			}
			if mode == "rotated, then expired" && current < 2 {
				t.Fatal("rotation did not advance the family", current)
			}
			if expires != "2000-01-01T00:00:00Z" {
				t.Fatal("narrowing extended an expired token", expires)
			}
		})
	}
}

// R-02: an earlier attempt that expired unconsumed no longer blocks a new one,
// measured on the real clock.
func TestOwnerMoveRetriesAfterAnExpiredAttempt(t *testing.T) {
	f := newOwnerCaptureFixture(t)
	ctx := context.Background()
	short := f.start
	short.ExpiresAt = time.Now().UTC().Add(1200 * time.Millisecond)
	if err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		return f.s.CaptureOwnerClaimTx(ctx, tx, f.p, f.original, ownerCaptureSigned(t, f.key, short), f.claim.Audience)
	}); err != nil {
		t.Fatal(err)
	}
	// Each retry starts after networking ended the earlier intent (in production,
	// the expiry sweeper's denial). Emulated here so the test isolates the
	// identity rule: networking's own transitions are covered in its package.
	second := func() error {
		op := identity.Token()
		if _, err := f.db.Exec(`UPDATE networking_claim_intents SET stage='cancelled' WHERE operation_id IN(SELECT pending_operation_id FROM networking_claim_identity WHERE singleton=1);UPDATE networking_claim_identity SET pending_operation_id=NULL WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		if err := f.runner.Do(ctx, func(ctx context.Context) error {
			_, err := f.store.PreparePurpose(ctx, networking.Binding{OperationID: op, ServerID: f.intent.ServerID, AccountID: networking.UnboundAccount, PublicKey: f.intent.PublicKey}, networking.LocalOwner{AccountID: f.p.AccountID, ProfileID: f.p.ProfileID, Epoch: int64(f.p.Epoch)}, networking.ClaimPurpose{Purpose: "switch"})
			return err
		}); err != nil {
			return err
		}
		now := time.Now().UTC()
		claim := f.claim
		claim.OperationID, claim.IssuedAt, claim.ExpiresAt = op, now, now.Add(10*time.Minute)
		original := ownerCaptureSigned(t, f.key, claim)
		raw, _ := base64.RawURLEncoding.DecodeString(original.Payload)
		hash := sha256.Sum256(raw)
		start := f.start
		start.OperationID, start.ClaimHash, start.ServerChallenge, start.IssuedAt, start.ExpiresAt = op, base64.RawURLEncoding.EncodeToString(hash[:]), identity.Token(), now, now.Add(4*time.Minute)
		return dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
			return f.s.CaptureOwnerClaimTx(ctx, tx, f.p, original, ownerCaptureSigned(t, f.key, start), claim.Audience)
		})
	}
	if err := second(); !errors.Is(err, identity.ErrConflict) {
		t.Fatal("a live attempt did not block a second one", err)
	}
	time.Sleep(time.Until(short.ExpiresAt) + 100*time.Millisecond)
	if err := second(); err != nil {
		t.Fatal("an expired attempt still blocks a new one", err)
	}
	var rows, challenges int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM identity_owner_completions),(SELECT count(*) FROM hosted_identity_challenges)`).Scan(&rows, &challenges); err != nil || rows != 1 || challenges != 1 {
		t.Fatal("the stale attempt was not replaced", rows, challenges, err)
	}
}
