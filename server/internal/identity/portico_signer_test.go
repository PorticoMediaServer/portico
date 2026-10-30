package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// selectPorticoFixture models a password-free Portico database, including
// upgraded installations retaining confirmed factors. No guard is disabled.
func selectPorticoFixture(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico'`); err != nil {
		t.Fatal(err)
	}
}

func linkedMember(t *testing.T, s *Service, local, hosted string) {
	t.Helper()
	selectPorticoFixture(t, s)
	if _, err := s.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,x'',?)`, local, local, local+"-profile"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES(?,?)`, local, hosted); err != nil {
		t.Fatal(err)
	}
}

func revokeKey(t *testing.T, s *Service, key string) int {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO hosted_signing_revocations(key_id) VALUES(?)`, key); err != nil {
		t.Fatal(err)
	}
	n, err := s.RevokeSessionsAdmittedByRevokedKeysTx(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return n
}

// A television admitted by the Portico device-authorization identity assertion
// records that signer and ends with it, alongside the same account's phone.
func TestAPorticoTelevisionSessionEndsWithItsSigningKey(t *testing.T) {
	ctx := context.Background()
	s, _, _ := directFixture(t)
	linkedMember(t, s, "linked", "acc_linked")
	in, err := s.PorticoSignIn(ctx, PorticoIdentity{AccountID: "acc_linked", Username: "linked", SignerKeyID: "key-a"})
	if err != nil || in.Session == nil {
		t.Fatalf("%+v %v", in, err)
	}
	tv := onDevice(t, memberTV, "Living room")
	admittedTV, err := s.PorticoSignIn(tv, PorticoIdentity{AccountID: "acc_linked", Username: "linked", SignerKeyID: "key-a"})
	if err != nil || admittedTV.Session == nil {
		t.Fatalf("television identity admission: %+v %v", admittedTV, err)
	}
	television := *admittedTV.Session
	var signer string
	if err = s.db.QueryRow(`SELECT signer_key_id FROM portico_admitted_families WHERE family_id=?`, television.SessionFamilyID).Scan(&signer); err != nil || signer != "key-a" {
		t.Fatalf("the television's session records signing key %q: %v", signer, err)
	}
	// Revoking an unrelated key ends nothing.
	if n := revokeKey(t, s, "key-unrelated"); n != 0 {
		t.Fatalf("revoking an unrelated key ended %d sessions", n)
	}
	if _, err = s.Authenticate(television.AccessToken); err != nil {
		t.Fatal(err)
	}
	if n := revokeKey(t, s, "key-a"); n != 2 {
		t.Fatalf("revoking the key ended %d sessions, want the phone's and the television's", n)
	}
	for name, token := range map[string]string{"the phone": in.Session.AccessToken, "the television": television.AccessToken} {
		if _, err = s.Authenticate(token); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%s is still signed in after its signing key was revoked: %v", name, err)
		}
	}
	// A second revocation pass finds nothing left to end.
	if n := revokeKey(t, s, "key-a"); n != 0 {
		t.Fatalf("a repeated pass ended %d sessions", n)
	}
	// A session whose signing key was never recorded is not swept up by an
	// empty key id.
	plain, err := s.PorticoSignIn(ctx, PorticoIdentity{AccountID: "acc_linked", Username: "linked"})
	if err != nil || plain.Session == nil {
		t.Fatal(err)
	}
	if n := revokeKey(t, s, ""); n != 0 {
		t.Fatalf("an empty key id ended %d sessions", n)
	}
}

// The explicit selected authority, rather than an incidental claim read,
// determines the method offered by sign-in and invitation screens.
func TestHostedSignInMethodUsesSelectedKind(t *testing.T) {
	s, _, _ := directFixture(t)
	if s.Hosted() {
		t.Fatal("Local server reports Portico sign-in")
	}
	s.SetupClaimReadyTx = func(_ context.Context, tx *sql.Tx) (bool, error) {
		if tx == nil {
			t.Fatal("claim read outside transaction")
		}
		return true, nil
	}
	if s.Hosted() {
		t.Fatal("installed claim silently changed selected Local kind")
	}
	selectPorticoFixture(t, s)
	if !s.Hosted() {
		t.Fatal("Portico selected kind does not report Portico sign-in")
	}
	capabilities, err := s.Capabilities(context.Background())
	if err != nil || !capabilities.HostedAttach {
		t.Fatalf("capabilities: %+v %v", capabilities, err)
	}
	s.SetupClaimReadyTx = func(context.Context, *sql.Tx) (bool, error) { return false, errors.New("unreadable") }
	if !s.Hosted() {
		t.Fatal("claim observation replaced canonical selected kind")
	}
}
