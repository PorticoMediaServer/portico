package identity_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func countProof(t *testing.T, f ownerCaptureFixture, preview string, revision int64, count int) claimweb.Signed {
	t.Helper()
	return ownerCaptureSigned(t, f.key, claimweb.OwnerSwitchCount{
		Kind: claimweb.OwnerSwitchCountKind, ServerID: f.claim.ServerID, PublicKey: f.claim.PublicKey,
		OperationID: f.claim.OperationID, Purpose: "switch", PreviewID: preview, PreviewRevision: revision,
		LocalGeneration: f.claim.LocalGeneration, ClaimHash: f.start.ClaimHash, OtherPeopleCount: count,
		IssuedAt: time.Now().UTC(), ExpiresAt: f.start.ExpiresAt,
	})
}

func TestOwnerSwitchCountFreezesOriginalProofAndNeverResignsOnRetry(t *testing.T) {
	f := newOwnerCaptureFixture(t)
	ctx := context.Background()
	calls := 0
	var first, replay *claimweb.Signed
	sign := func(_ context.Context, _ *sql.Tx, preview string, revision int64, count int) (claimweb.Signed, error) {
		calls++
		return countProof(t, f, preview, revision, count), nil
	}
	for _, output := range []**claimweb.Signed{&first, &replay} {
		if err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
			var err error
			*output, err = f.s.CaptureOwnerClaimWithCountTx(ctx, tx, f.p, f.original, f.proof, f.claim.Audience, sign)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || first == nil || replay == nil || *first != *replay {
		t.Fatal("retry changed or regenerated the captured optional proof")
	}
}

func TestOwnerSwitchCountFreezesAbsenceAndInvalidInfoDoesNotBlockApproval(t *testing.T) {
	for _, mode := range []string{"unavailable signer", "wrong preview"} {
		t.Run(mode, func(t *testing.T) {
			f := newOwnerCaptureFixture(t)
			ctx := context.Background()
			calls := 0
			sign := func(_ context.Context, _ *sql.Tx, preview string, revision int64, count int) (claimweb.Signed, error) {
				calls++
				if mode == "unavailable signer" {
					return claimweb.Signed{}, errors.New("signer unavailable")
				}
				return countProof(t, f, preview+"_other", revision, count), nil
			}
			for i := 0; i < 2; i++ {
				if err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
					proof, err := f.s.CaptureOwnerClaimWithCountTx(ctx, tx, f.p, f.original, f.proof, f.claim.Audience, sign)
					if err == nil && proof != nil {
						t.Fatal("invalid optional info escaped")
					}
					if err != nil {
						return err
					}
					return f.s.CheckOwnerClaimTx(ctx, tx, f.p, f.intent.OperationID)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 1 {
				t.Fatal("retry attempted to append a previously absent count")
			}
		})
	}
}

func TestOwnerSwitchCountCannotHideAuthorityRetirementDuringSigning(t *testing.T) {
	f := newOwnerCaptureFixture(t)
	ctx := context.Background()
	err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		_, err := f.s.CaptureOwnerClaimWithCountTx(ctx, tx, f.p, f.original, f.proof, f.claim.Audience,
			func(ctx context.Context, tx *sql.Tx, preview string, revision int64, count int) (claimweb.Signed, error) {
				var family string
				if err := tx.QueryRowContext(ctx, `SELECT family_id FROM authorization_family_tokens WHERE token_hash=?`, f.p.Hash).Scan(&family); err != nil {
					return claimweb.Signed{}, err
				}
				if err := identity.RevokeFamilyTx(ctx, tx, family, identity.RevokedExplicitSignout); err != nil {
					return claimweb.Signed{}, err
				}
				return countProof(t, f, preview, revision, count), nil
			})
		return err
	})
	if err == nil {
		t.Fatal("retired owner authority committed an optional proof")
	}
	var rows int
	if err = f.db.QueryRow(`SELECT count(*) FROM identity_owner_completions`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("failed guarded capture did not roll back atomically")
	}
}
