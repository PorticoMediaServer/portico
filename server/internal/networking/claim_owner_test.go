package networking

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"

	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func TestClaimOwnerRealAccountConsent(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "owner.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// Install the current account and recovery-owner membership schema together.
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','local',x'01','profile',1)`); e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	owner := LocalOwner{AccountID: "owner", ProfileID: "profile", Epoch: 1}
	if e = GuardLocalOwner(context.Background(), tx, owner); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='owner'`); e != nil {
		t.Fatal(e)
	}
	if e = GuardLocalOwner(context.Background(), tx, owner); !errors.Is(e, ErrStale) {
		t.Fatal("disabled owner retained consent", e)
	}
	if _, e = tx.Exec(`UPDATE direct_memberships SET disabled=0 WHERE account_id='owner'`); e != nil {
		t.Fatal(e)
	}
	for _, candidate := range []LocalOwner{{AccountID: "other", ProfileID: "profile", Epoch: 1}, {AccountID: "owner", ProfileID: "other", Epoch: 1}, {AccountID: "owner", ProfileID: "profile", Epoch: 2}} {
		if e = GuardLocalOwner(context.Background(), tx, candidate); !errors.Is(e, ErrStale) {
			t.Fatalf("stale consent accepted: %v", e)
		}
	}
	if _, e = tx.Exec(`UPDATE accounts SET epoch=2 WHERE id='owner'`); e != nil {
		t.Fatal(e)
	}
	if e = GuardLocalOwner(context.Background(), tx, owner); !errors.Is(e, ErrStale) {
		t.Fatal("same transaction epoch change missed")
	}
	owner.Epoch = 2
	if e = GuardLocalOwner(context.Background(), tx, owner); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(`DELETE FROM accounts WHERE id='owner'`); e != nil {
		t.Fatal(e)
	}
	if e = GuardLocalOwner(context.Background(), tx, owner); !errors.Is(e, ErrStale) {
		t.Fatal("deleted owner retained consent")
	}
}
func TestClaimOwnerCanceledTransaction(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "owner.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = GuardLocalOwner(ctx, tx, LocalOwner{AccountID: "owner", ProfileID: "profile", Epoch: 1}); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancellation lost: %v", e)
	}
}

func TestClaimOwnerPendingSetupNeverBecomesLocalMembership(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "pending.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	const op = "33333333-3333-4333-8333-333333333333"
	if _, e = db.Exec(`INSERT INTO onboarding_state_v1(singleton,auth_mode,operation_id,request_hash,setup_hash,receipt_until) VALUES(1,'hosted',?,'hash','hash',0)`, op); e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	actor := LocalOwner{SetupOperationID: op, Epoch: 1}
	if e = GuardLocalOwner(context.Background(), tx, actor); e != nil {
		t.Fatal("pending setup refused", e)
	}
	if validOwner(LocalOwner{SetupOperationID: op, Epoch: 1, AccountID: "fake", ProfileID: "fake"}) {
		t.Fatal("pending setup accepted fake member/profile")
	}
	if _, e = tx.Exec(`UPDATE onboarding_state_v1 SET auth_mode='local',revision=revision+1`); e != nil {
		t.Fatal(e)
	}
	if e = GuardLocalOwner(context.Background(), tx, actor); !errors.Is(e, ErrStale) {
		t.Fatal("method change retained pending claim authority", e)
	}
}
