package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"reflect"
	"testing"
)

func TestAccountDefaultSetupIsPendingWithoutAnyLocalOwnerOrPassword(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx := context.Background()
	if _, e := db.Exec(`DELETE FROM accounts`); e != nil {
		t.Fatal(e)
	}
	token, e := os.ReadFile(s.setupPath)
	if e != nil {
		t.Fatal(e)
	}
	q := SetupOptions{RequestID: "33333333-3333-4333-8333-333333333333", SetupToken: string(token), Name: "Home"}
	out, e := s.SetupWithOptions(ctx, q, false)
	if e != nil {
		t.Fatal(e)
	}
	if out.Pending == nil || !out.Pending.ClaimRequired || out.AccessToken != "" {
		t.Fatal("account setup fabricated local owner authority")
	}
	var accounts, families int
	if e = db.QueryRow(`SELECT (SELECT count(*) FROM accounts),(SELECT count(*) FROM authorization_session_families)`).Scan(&accounts, &families); e != nil || accounts != 0 || families != 0 {
		t.Fatal("account setup created local authority", e)
	}
	raw, _ := json.Marshal(out)
	if string(raw) == "" || string(raw) == "null" {
		t.Fatal("missing pending state")
	}
	var fields map[string]any
	if e = json.Unmarshal(raw, &fields); e != nil {
		t.Fatal(e)
	}
	if _, ok := fields["accessToken"]; ok {
		t.Fatal("pending setup exposed empty credential")
	}
	again, e := s.ResumeSetup(ctx, q.RequestID, q.SetupToken, false)
	if e != nil || !reflect.DeepEqual(out, again) {
		t.Fatal("pending initialization not resumable", e)
	}
	if _, e = s.ResumeSetup(ctx, q.RequestID, Token(), false); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("wrong continuation secret admitted", e)
	}
	// A server being set up for a Portico Account never gets a local owner, not even
	// with the original setup token (Justin, 31 Oct). Nothing is written.
	q.AuthMode = "local"
	q.Username = "owner"
	q.Password = "Local-password1!"
	if _, e = s.SetupWithOptions(ctx, q, false); !errors.Is(e, ErrPorticoSetup) {
		t.Fatal("local choice on a Portico Account setup", e)
	}
	var owners, rows int
	if e = s.db.QueryRow(`SELECT (SELECT count(*) FROM accounts),(SELECT count(*) FROM onboarding_state_v1 WHERE auth_mode='hosted')`).Scan(&owners, &rows); e != nil || owners != 0 || rows != 1 {
		t.Fatal("the refused local choice changed setup", owners, rows, e)
	}
}
func TestAccountSetupRejectsPasswordFallbackAndConcurrentReplacement(t *testing.T) {
	s, db, _ := familyFixture(t)
	if _, e := db.Exec(`DELETE FROM accounts`); e != nil {
		t.Fatal(e)
	}
	token, e := os.ReadFile(s.setupPath)
	if e != nil {
		t.Fatal(e)
	}
	q := SetupOptions{RequestID: "44444444-4444-4444-8444-444444444444", SetupToken: string(token), Name: "Home", Username: "owner", Password: "Local-password1!", AuthMode: "hosted"}
	if _, e = s.SetupWithOptions(context.Background(), q, true); !errors.Is(e, ErrDirectInput) {
		t.Fatal("Hosted accepted local password fallback", e)
	}
	q.Username = ""
	q.Password = ""
	if _, e = s.SetupWithOptions(context.Background(), q, true); e != nil {
		t.Fatal(e)
	}
	q.RequestID = "55555555-5555-4555-8555-555555555555"
	if _, e = s.SetupWithOptions(context.Background(), q, true); !errors.Is(e, ErrConflict) {
		t.Fatal("unrelated initialization replaced pending claim", e)
	}
}

func TestAccountSetupRestartCannotReissueBootstrapAndClaimGuardRevokes(t *testing.T) {
	s, db, dir := familyFixture(t)
	if _, e := db.Exec(`DELETE FROM accounts`); e != nil {
		t.Fatal(e)
	}
	token, e := os.ReadFile(s.setupPath)
	if e != nil {
		t.Fatal(e)
	}
	const op = "33333333-3333-4333-8333-333333333333"
	if _, e = s.SetupWithOptions(context.Background(), SetupOptions{RequestID: op, SetupToken: string(token), Name: "Home"}, true); e != nil {
		t.Fatal(e)
	}
	restarted, e := New(db, dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(restarted.setupPath); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("restart exposed another bootstrap", e)
	}
	// A live pending setup refuses honestly (setup_in_progress) and discloses nothing.
	var progress *SetupInProgressError
	if secret, e := restarted.BrowserSetupToken(context.Background()); secret != "" || !errors.As(e, &progress) {
		t.Fatal("public bootstrap disclosed pending secret", e)
	}
	pending, proof, e := restarted.SetupClaimAuthority(context.Background(), string(token))
	if e != nil {
		t.Fatal(e)
	}
	if e = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		return restarted.CheckSetupClaimTx(context.Background(), tx, op, pending.Revision, proof)
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE onboarding_state_v1 SET revision=revision+1`); e != nil {
		t.Fatal(e)
	}
	if e = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		return restarted.CheckSetupClaimTx(context.Background(), tx, op, pending.Revision, proof)
	}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("stale pending claim guard retained authority", e)
	}
}
