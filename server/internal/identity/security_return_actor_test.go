package identity

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"testing"
	"time"
)

// Actual Portico admission creates the family; fixture SQL only narrows its
// existing expiry. No password, MFA, grant or session guard is disabled.
func TestSecurityReturnActorCarriesOriginalTokenAndFamilyDeadline(t *testing.T) {
	s, db, _ := directFixture(t)
	linkedMember(t, s, "linked", "acc_linked")
	ctx := context.Background()
	admitted, e := s.PorticoSignIn(ctx, PorticoIdentity{AccountID: "acc_linked", Username: "linked", SignerKeyID: "security-fixture-key"})
	if e != nil || admitted.Session == nil {
		t.Fatal("actual Portico admission failed", e)
	}
	token := admitted.Session.AccessToken
	var family string
	if e = db.QueryRow(`SELECT family_id FROM authorization_family_tokens WHERE token_hash=?`, Digest(token)).Scan(&family); e != nil {
		t.Fatal(e)
	}
	tokenEnd := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Millisecond)
	familyEnd := tokenEnd.Add(-time.Minute)
	if _, e = db.Exec(`UPDATE authorization_family_tokens SET expires_at=? WHERE token_hash=?`, tokenEnd.Format(time.RFC3339Nano), Digest(token)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, familyEnd.Format(time.RFC3339Nano), family); e != nil {
		t.Fatal(e)
	}
	read := func() (string, string, time.Time, error) {
		g, e := dbwork.BeginSnapshot(ctx, db)
		if e != nil {
			t.Fatal(e)
		}
		defer g.Rollback()
		account, binding, expiry, e := s.SecurityReturnActorTx(ctx, g.Tx(), token)
		if e == nil {
			e = g.Commit()
		}
		return account, binding, expiry, e
	}
	account, binding, expiry, e := read()
	if e != nil || account != "acc_linked" || binding == "" || !expiry.Equal(familyEnd) {
		t.Fatal("original family bound was not retained", account, expiry, e)
	}
	// A different authoritative horizon cannot silently renew the old opening.
	narrower := familyEnd.Add(-20 * time.Second)
	if _, e = db.Exec(`UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, narrower.Format(time.RFC3339Nano), family); e != nil {
		t.Fatal(e)
	}
	_, next, nextExpiry, e := read()
	if e != nil || next == binding || !nextExpiry.Equal(narrower) {
		t.Fatal("changed original authority kept its old binding", e)
	}
	if _, e = db.Exec(`UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), family); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = read(); e == nil {
		t.Fatal("expired original server authority was accepted")
	}
}
