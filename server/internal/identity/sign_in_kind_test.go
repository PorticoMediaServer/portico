package identity

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalSignInKindSetupAndDatabaseFences(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "accounts.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stubSetupRemote(s)
	if k, e := s.SignInKind(ctx); e != nil || k != SignInPortico {
		t.Fatalf("default %s %v", k, e)
	}
	token, err := os.ReadFile(s.setupPath)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := s.SetupWithOptions(ctx, SetupOptions{SetupToken: string(token), Name: "Fixture", Username: "owner", Password: "Testing1!", AuthMode: "local"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if k, e := s.SignInKind(ctx); e != nil || k != SignInLocal || s.Hosted() {
		t.Fatalf("local %s %v", k, e)
	}
	if _, err = s.PorticoSignIn(ctx, PorticoIdentity{AccountID: "someone"}); !errors.Is(err, ErrSignInKindLocal) {
		t.Fatalf("wrong kind %v", err)
	}
	if _, err = db.Exec(`INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES(?,?)`, setup.Envelope.Viewer.AccountID, "someone"); err == nil {
		t.Fatal("database admitted mixed local identity")
	}
	if _, err = db.Exec(`UPDATE identity_state SET sign_in_kind='portico'`); err == nil {
		t.Fatal("kind flipped over password")
	}
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if _, e := tx.Exec(`UPDATE accounts SET password_hash=X''`); e != nil {
			return e
		}
		_, e := tx.Exec(`UPDATE identity_state SET sign_in_kind='portico'`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectLogin(ctx, "owner", "Testing1!"); !errors.Is(err, ErrSignInKindPortico) {
		t.Fatalf("password wrong-kind %v", err)
	}
	if err = s.ChangeDirectPassword(ctx, setup.Envelope.AccessToken, "Testing1!", "Testing2!", ""); !errors.Is(err, ErrSignInKindPortico) {
		t.Fatalf("password mutation %v", err)
	}
	if _, err = s.EnrolTwoFactor(ctx, setup.Envelope.AccessToken, "Testing1!"); !errors.Is(err, ErrSignInKindPortico) {
		t.Fatalf("factor mutation %v", err)
	}
	if _, err = db.Exec(`UPDATE accounts SET password_hash=X'01'`); err == nil {
		t.Fatal("database admitted mixed Portico identity")
	}
	cap, err := s.Capabilities(ctx)
	if err != nil || cap.TwoFactor || cap.QuickConnect || len(cap.Methods) != 1 || cap.Methods[0] != "hosted" {
		t.Fatalf("capabilities %+v %v", cap, err)
	}
}
