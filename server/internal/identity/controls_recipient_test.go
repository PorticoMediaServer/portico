package identity

import (
	"context"
	"testing"
)

func TestControlsRecipientKeepsOriginalFamilyAndGeneration(t *testing.T) {
	for _, change := range []string{"generation", "revoked"} {
		t.Run(change, func(t *testing.T) {
			db, s, p := transactionAuthorityFixture(t)
			ctx := context.Background()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			original, err := s.CaptureControlsRecipientTx(ctx, tx, p)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.CheckControlsRecipientTx(ctx, tx, original, p); err != nil {
				t.Fatal(err)
			}
			wrong := p
			wrong.ProfileID = "another-profile"
			if err = s.CheckControlsRecipientTx(ctx, tx, original, wrong); err == nil {
				t.Fatal("another original viewer accepted")
			}
			narrowed := p
			narrowed.Grant = GrantCastTarget
			if _, err = s.CaptureControlsRecipientTx(ctx, tx, narrowed); err == nil {
				t.Fatal("narrowed grant captured own-account control audience")
			}
			tx.Rollback()
			statement := `UPDATE authorization_session_families SET current_generation=current_generation+1 WHERE id=?`
			if change == "revoked" {
				statement = `UPDATE authorization_session_families SET revoked=1 WHERE id=?`
			}
			if _, err = db.ExecContext(ctx, statement, original.family.ID); err != nil {
				t.Fatal(err)
			}
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = s.CheckControlsRecipientTx(ctx, tx, original, p); err == nil {
				t.Fatal("retired original recipient survived", change)
			}
		})
	}
}
