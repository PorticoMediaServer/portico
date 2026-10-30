package identity_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestOwnerSwitchCountFactRequiresTheCapturedOwnerOperation(t *testing.T) {
	f := newOwnerCaptureFixture(t)
	ctx := context.Background()
	err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if err := f.capture(ctx, tx); err != nil {
			return err
		}
		fact, err := f.s.OwnerSwitchCountFactTx(ctx, tx, f.p, f.intent.OperationID)
		if err != nil {
			return err
		}
		if fact.PreviewID == "" || fact.PreviewRevision < 1 || fact.Count == nil || *fact.Count != 0 {
			t.Fatal("actual zero-count preview was not preserved")
		}
		other := f.p
		other.Role = "member"
		if _, err = f.s.OwnerSwitchCountFactTx(ctx, tx, other, f.intent.OperationID); err == nil {
			t.Fatal("a member read the owner signing fact")
		}
		if _, err = f.s.OwnerSwitchCountFactTx(ctx, tx, f.p, identity.Token()); err == nil {
			t.Fatal("an unrelated operation read the captured count")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.LogoutToken(ctx, f.bearer); err != nil {
		t.Fatal(err)
	}
	read, err := dbwork.BeginSnapshot(ctx, f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	if _, err = f.s.OwnerSwitchCountFactTx(ctx, read.Tx(), f.p, f.intent.OperationID); err == nil {
		t.Fatal("a signed-out owner retained count-signing authority")
	}
}
