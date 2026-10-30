package hosted

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
	"time"
)

func TestRootRevocationSurvivesRestartAndOldCertificateReplay(t *testing.T) {
	f := newClaimFixture(t)
	oldCert := testSigningCertificate(f.hostedKey, "pin1")
	old, _ := trust.Sign(f.hostedKey, oldCert, []byte("fixture"))
	if _, err := f.service.verifyEnvelope(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	next, err := trust.Certify(f.hostedKey, public, "next", "documents", now.Add(-time.Minute), now.Add(time.Hour), 2, []string{"pin1"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := trust.Sign(key, next, []byte("fixture"))
	if _, err = f.service.verifyEnvelope(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.verifyEnvelope(t.Context(), old); err == nil {
		t.Fatal("old certificate undid a known root revocation")
	}
	restarted, err := New(f.db, f.id, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(f.hostedKey.Public().(ed25519.PublicKey)), trust.KeyID(f.hostedKey.Public().(ed25519.PublicKey)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.verifyEnvelope(t.Context(), old); err == nil {
		t.Fatal("revocation lost on restart")
	}
}

// A89: learning a signing-key revocation ends the local families admitted by
// that key, while password-authenticated families keep working.
func TestRootRevocationEndsSignedAdmissionsAndLeavesPasswordFamilies(t *testing.T) {
	f := newClaimFixture(t)
	server := f.id.ID()
	if _, err := f.db.Exec(`INSERT INTO hosted_policy_signers(server_id,key_id) VALUES(?,'pin1')`, server); err != nil {
		t.Fatal(err)
	}
	horizon := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, row := range [][2]string{{"family-portico", "local"}, {"family-local", "local"}} {
		if _, err := f.db.Exec(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES(?,?,'account','profile',?,'member',1,?,1,0)`, row[0], server, row[1], horizon); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`INSERT INTO portico_admitted_families(family_id,signer_key_id) VALUES('family-portico','pin1')`); err != nil {
		t.Fatal(err)
	}
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	next, err := trust.Certify(f.hostedKey, public, "next", "documents", now.Add(-time.Minute), now.Add(time.Hour), 2, []string{"pin1"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := trust.Sign(key, next, []byte("fixture"))
	if _, err = f.service.verifyEnvelope(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"family-portico": 1, "family-local": 0} {
		var revoked int
		if err = f.db.QueryRow(`SELECT revoked FROM authorization_session_families WHERE id=?`, id).Scan(&revoked); err != nil || revoked != want {
			t.Fatalf("%s revoked=%d want %d (%v)", id, revoked, want, err)
		}
	}
}

// A person signs in with a Portico Account; the assertion was signed by key
// "pin1". Later this server learns, from a newer certificate, that "pin1" was
// revoked. The session that assertion admitted ends, with every session
// derived from it, because whoever held the key may be the one signed in.
// Sessions that never depended on the key are left alone.
func TestLearningASigningKeyWasRevokedEndsTheSessionsItAdmitted(t *testing.T) {
	f := newClaimFixture(t)
	ctx := t.Context()
	addLinkedMember(t, f, "local-m", "acc_member")
	addLinkedMember(t, f, "local-n", "acc_other")
	var hooked []string
	f.id.OnFamilyRevokedTx = func(_ context.Context, _ *sql.Tx, family identity.FamilyState) error {
		hooked = append(hooked, family.ID)
		return nil
	}

	who, err := verify(f, freshClaims(f, "acc_member"))
	if err != nil || who.SignerKeyID != "pin1" {
		t.Fatalf("the verified assertion does not name its signing key: %+v %v", who.SignerKeyID, err)
	}
	admitted, err := f.id.PorticoSignIn(ctx, who)
	if err != nil || admitted.Session == nil {
		t.Fatal(err)
	}
	// A profile chosen from that sign-in is a session derived from it.
	chosen, err := f.id.SelectDirectProfile(ctx, admitted.Session.AccessToken, admitted.Account.PrimaryProfileID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	// Another member, admitted by a different key that stays good.
	other, err := f.id.PorticoSignIn(ctx, identity.PorticoIdentity{AccountID: "acc_other", Username: "local-n", SignerKeyID: "pin-good"})
	if err != nil || other.Session == nil {
		t.Fatal(err)
	}
	// A session that never involved Hosted at all.
	local, err := f.id.Issue("local-n", "local-n-profile", "local", identity.TierMember, 1)
	if err != nil {
		t.Fatal(err)
	}
	signer := func(family string) string {
		var key string
		if err := f.db.QueryRow(`SELECT COALESCE((SELECT signer_key_id FROM portico_admitted_families WHERE family_id=?),'<none>')`, family).Scan(&key); err != nil {
			t.Fatal(err)
		}
		return key
	}
	if signer(admitted.Session.SessionFamilyID) != "pin1" || signer(chosen.Session.SessionFamilyID) != "pin1" || signer(other.Session.SessionFamilyID) != "pin-good" || signer(local.SessionFamilyID) != "<none>" {
		t.Fatalf("recorded signing keys: %q %q %q %q", signer(admitted.Session.SessionFamilyID), signer(chosen.Session.SessionFamilyID), signer(other.Session.SessionFamilyID), signer(local.SessionFamilyID))
	}

	// A certificate that revokes nothing changes nothing.
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	harmless, err := trust.Certify(f.hostedKey, public, "next", "documents", now.Add(-time.Minute), now.Add(time.Hour), 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := trust.Sign(key, harmless, []byte("fixture"))
	if _, err = f.service.verifyEnvelope(ctx, envelope); err != nil {
		t.Fatal(err)
	}
	if _, err = f.id.Authenticate(chosen.Session.AccessToken); err != nil {
		t.Fatalf("a newer certificate that revokes nothing ended a session: %v", err)
	}

	// The revocation of pin1 arrives.
	revoking, err := trust.Certify(f.hostedKey, public, "next", "documents", now.Add(-time.Minute), now.Add(time.Hour), 3, []string{"pin1"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ = trust.Sign(key, revoking, []byte("fixture"))
	if _, err = f.service.verifyEnvelope(ctx, envelope); err != nil {
		t.Fatal(err)
	}
	if _, err = f.id.Authenticate(chosen.Session.AccessToken); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("a session derived from the revoked key's sign-in is still alive: %v", err)
	}
	for _, family := range []string{admitted.Session.SessionFamilyID, chosen.Session.SessionFamilyID} {
		var revoked int
		var reason string
		if err = f.db.QueryRow(`SELECT revoked,revoked_reason FROM authorization_session_families WHERE id=?`, family).Scan(&revoked, &reason); err != nil || revoked != 1 || reason != string(identity.RevokedSigningKeyRevoked) {
			t.Fatalf("family %s: revoked=%d reason=%q %v", family, revoked, reason, err)
		}
	}
	if len(hooked) != 2 {
		t.Fatalf("the playback clean-up ran for %d sessions, want the 2 that ended", len(hooked))
	}
	// The renewal credential of an ended session is dead too.
	if _, err = f.id.RefreshSession(ctx, chosen.Session.RefreshToken, chosen.Session.InstallationID, "renew-after-revocation"); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("an ended session renewed: %v", err)
	}
	// Untouched: the other key's session and the one that never used Hosted.
	if _, err = f.id.Authenticate(other.Session.AccessToken); err != nil {
		t.Fatalf("a session admitted by a key that is still good was ended: %v", err)
	}
	if _, err = f.id.Authenticate(local.AccessToken); err != nil {
		t.Fatalf("a session that never involved Hosted was ended: %v", err)
	}
	// And the revoked key admits nobody new.
	if _, err = verify(f, freshClaims(f, "acc_member")); err == nil {
		t.Fatal("the revoked key's assertion was accepted afterwards")
	}
}

// A server nobody signs in to with a Portico Account sees no signed document,
// so it learns that a key was revoked from the certificate Hosted sends with a
// check-in. Only a certificate the root really signed, for documents and in
// date, teaches it anything; none of them can make it forget.
func TestTheCheckInCertificateTeachesRevocationsAndNothingElseDoes(t *testing.T) {
	f := newClaimFixture(t)
	ctx := t.Context()
	addLinkedMember(t, f, "local-m", "acc_member")
	who, err := verify(f, freshClaims(f, "acc_member"))
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := f.id.PorticoSignIn(ctx, who)
	if err != nil || admitted.Session == nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	certify := func(root ed25519.PrivateKey, purpose string, before, after time.Time, revision uint64, revoked []string) *trust.Certificate {
		t.Helper()
		c, err := trust.Certify(root, public, "next", purpose, before, after, revision, revoked)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	alive := func(why string) {
		t.Helper()
		if _, err := f.id.Authenticate(admitted.Session.AccessToken); err != nil {
			t.Fatalf("%s ended the session: %v", why, err)
		}
		var known int
		if err := f.db.QueryRow(`SELECT count(*) FROM hosted_signing_revocations`).Scan(&known); err != nil || known != 0 {
			t.Fatalf("%s taught this server %d revocations: %v", why, known, err)
		}
	}
	// Nothing sent (an older Hosted): nothing learned, and no failure.
	if err = f.service.learnSigningCertificate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	alive("a check-in with no certificate")
	tampered := certify(f.hostedKey, "documents", now.Add(-time.Minute), now.Add(time.Hour), 5, nil)
	tampered.Revoked = []string{"pin1"}
	for why, c := range map[string]*trust.Certificate{
		"a certificate another root signed":     certify(stranger, "documents", now.Add(-time.Minute), now.Add(time.Hour), 5, []string{"pin1"}),
		"a certificate for another purpose":     certify(f.hostedKey, "wake", now.Add(-time.Minute), now.Add(time.Hour), 5, []string{"pin1"}),
		"a certificate that has expired":        certify(f.hostedKey, "documents", now.Add(-2*time.Hour), now.Add(-time.Hour), 5, []string{"pin1"}),
		"a certificate that is not yet valid":   certify(f.hostedKey, "documents", now.Add(time.Hour), now.Add(2*time.Hour), 5, []string{"pin1"}),
		"a certificate changed after signing":   tampered,
		"a certificate that revokes nothing":    certify(f.hostedKey, "documents", now.Add(-time.Minute), now.Add(time.Hour), 2, nil),
		"a certificate no newer than the known": certify(f.hostedKey, "documents", now.Add(-time.Minute), now.Add(time.Hour), 1, []string{"pin1"}),
	} {
		if err = f.service.learnSigningCertificate(ctx, c); err != nil {
			t.Fatalf("%s: %v", why, err)
		}
		alive(why)
	}
	// The real one: the key that admitted the session is revoked.
	if err = f.service.learnSigningCertificate(ctx, certify(f.hostedKey, "documents", now.Add(-time.Minute), now.Add(time.Hour), 3, []string{"pin1"})); err != nil {
		t.Fatal(err)
	}
	if _, err = f.id.Authenticate(admitted.Session.AccessToken); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("the session the revoked key admitted is still alive: %v", err)
	}
	var reason string
	if err = f.db.QueryRow(`SELECT revoked_reason FROM authorization_session_families WHERE id=?`, admitted.Session.SessionFamilyID).Scan(&reason); err != nil || reason != string(identity.RevokedSigningKeyRevoked) {
		t.Fatalf("ended for %q: %v", reason, err)
	}
	if _, err = verify(f, freshClaims(f, "acc_member")); err == nil {
		t.Fatal("the revoked key's assertion was accepted afterwards")
	}
	// An older certificate that leaves the key out does not bring it back.
	if err = f.service.learnSigningCertificate(ctx, certify(f.hostedKey, "documents", now.Add(-time.Minute), now.Add(time.Hour), 2, nil)); err != nil {
		t.Fatal(err)
	}
	var known int
	var revision uint64
	if err = f.db.QueryRow(`SELECT (SELECT count(*) FROM hosted_signing_revocations WHERE key_id='pin1'),(SELECT revision FROM hosted_trust_state WHERE singleton=1)`).Scan(&known, &revision); err != nil || known != 1 || revision != 3 {
		t.Fatalf("after an older certificate: pin1 known=%d revision=%d %v", known, revision, err)
	}
}

func TestOversizedCertificateRevisionNeverReachesPersistence(t *testing.T) {
	f := newClaimFixture(t)
	cert := testSigningCertificate(f.hostedKey, "pin1")
	cert.Revision = 1 << 63
	cert.Revoked = []string{"other"}
	if err := f.service.learnRevocations(t.Context(), cert); !errors.Is(err, trust.ErrTrust) {
		t.Fatal("oversized revision reached SQL", err)
	}
	var revision uint64
	var revoked int
	if err := f.db.QueryRow(`SELECT revision,(SELECT count(*) FROM hosted_signing_revocations) FROM hosted_trust_state WHERE singleton=1`).Scan(&revision, &revoked); err != nil {
		t.Fatal(err)
	}
	if revision != 0 || revoked != 0 {
		t.Fatal("oversized certificate changed trust", revision, revoked)
	}
}

func TestLargestSharedCertificateRevisionPersists(t *testing.T) {
	f := newClaimFixture(t)
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	cert, err := trust.Certify(f.hostedKey, public, "last-revision", "documents", now.Add(-time.Minute), now.Add(time.Hour), trust.MaximumCertificateRevision, nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := trust.Sign(key, cert, []byte("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.verifyEnvelope(t.Context(), envelope); err != nil {
		t.Fatal("maximum exact revision refused", err)
	}
	var revision uint64
	if err = f.db.QueryRow(`SELECT revision FROM hosted_trust_state WHERE singleton=1`).Scan(&revision); err != nil || revision != trust.MaximumCertificateRevision {
		t.Fatal("maximum revision not durable", revision, err)
	}
}
