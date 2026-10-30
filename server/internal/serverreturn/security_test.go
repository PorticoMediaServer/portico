package serverreturn

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func signedSecurityTest(t *testing.T, key ed25519.PrivateKey, value any) SecuritySigned {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return SecuritySigned{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw))}
}

func TestSecurityContextIsCanonicalFiniteAndNeverASignInPurpose(t *testing.T) {
	public, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	c := SecurityContext{SettingsScope: "account", SettingsSection: "profile", Purpose: "password_set_or_change", Kind: SecurityContextKind, ServerID: "srv_" + strings.Repeat("s", 43), PublicKey: base64.RawURLEncoding.EncodeToString(public), RegistrationID: "ret_" + strings.Repeat("r", 43), Origin: "https://original.example", HostedOrigin: "https://web.getportico.tv", ClaimOperationID: "claim-original", AccountID: "account-original", ActorBinding: strings.Repeat("a", 43), OpeningID: "opening-original-123", TaskID: "task-original-123", OperationID: "operation-original-123", ProviderIdentityID: "identity-original", ProviderRevision: 7, Nonce: strings.Repeat("n", 43), IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute), AuthorityExpiresAt: now.Add(5 * time.Minute)}
	if _, _, e = VerifySecurityContext(signedSecurityTest(t, key, c), public, now); e != nil {
		t.Fatal(e)
	}
	secret := strings.Repeat("v", 43)
	exchange := Exchange{Code: secret, TransactionID: "transaction", Verifier: secret, RegistrationID: "registration", ServerChallenge: secret, InstallationID: secret, Purpose: "sign-in"}
	if !exchange.Valid() {
		t.Fatal("ordinary sign-in exchange fixture is invalid")
	}
	exchange.Purpose = "security"
	if Purpose("security") || exchange.Valid() {
		t.Fatal("Security became a sign-in/claim exchange purpose")
	}
	for _, change := range []func(*SecurityContext){
		func(v *SecurityContext) { v.AuthorityExpiresAt = now.Add(4 * time.Minute) }, func(v *SecurityContext) { v.SettingsScope = "server" }, func(v *SecurityContext) { v.SettingsSection = "signin" }, func(v *SecurityContext) { v.Purpose = "account_export" },
		func(v *SecurityContext) { v.Kind = SecurityConfirmationKind }, func(v *SecurityContext) { v.Origin += "/path" }, func(v *SecurityContext) { v.HostedOrigin = "http://web.getportico.tv" }, func(v *SecurityContext) { v.ProviderRevision = 0 }, func(v *SecurityContext) { v.TaskID = "" }, func(v *SecurityContext) { v.ActorBinding = "" }, func(v *SecurityContext) { v.ExpiresAt = now }, func(v *SecurityContext) { v.ExpiresAt = now.Add(5*time.Minute + time.Nanosecond) },
	} {
		bad := c
		change(&bad)
		if _, _, e = VerifySecurityContext(signedSecurityTest(t, key, bad), public, now); e == nil {
			t.Fatal("invalid Security context accepted", bad.Kind)
		}
	}
	signed := signedSecurityTest(t, key, c)
	raw, _ := base64.RawURLEncoding.DecodeString(signed.Payload)
	// Even a genuine signature cannot make duplicate/unknown/noncanonical fields
	// a second spelling of the closed purpose.
	for _, altered := range [][]byte{append([]byte(" "), raw...), append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"kind":"portico.security-return-prepare.v1"}`)...), append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"grant":"login"}`)...)} {
		q := SecuritySigned{Payload: base64.RawURLEncoding.EncodeToString(altered), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, altered))}
		if _, _, e = VerifySecurityContext(q, public, now); e == nil {
			t.Fatal("noncanonical signed context accepted")
		}
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, _, e = VerifySecurityContext(signed, other, now); e == nil {
		t.Fatal("another server key accepted")
	}
}

func TestSecurityConfirmationBindsOriginalContextTransactionCodeAndHorizon(t *testing.T) {
	public, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	code := "scr_" + strings.Repeat("c", 43)
	c := SecurityConfirmation{Kind: SecurityConfirmationKind, ContextDigest: SecurityDigest([]byte("original context")), TransactionID: "oid_original", CodeDigest: SecurityDigest([]byte(code)), IssuedAt: now, ExpiresAt: now.Add(time.Minute)}
	q := signedSecurityTest(t, key, c)
	if _, e = VerifySecurityConfirmation(q, public, c.ContextDigest, c.TransactionID, code, now); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][3]string{{"other", c.TransactionID, code}, {c.ContextDigest, "oid_other", code}, {c.ContextDigest, c.TransactionID, "scr_other"}} {
		if _, e = VerifySecurityConfirmation(q, public, args[0], args[1], args[2], now); e == nil {
			t.Fatal("confirmation adopted another original")
		}
	}
	if _, e = VerifySecurityConfirmation(q, public, c.ContextDigest, c.TransactionID, code, c.ExpiresAt); e == nil {
		t.Fatal("expired confirmation accepted")
	}
	c.ExpiresAt = now.Add(time.Minute + time.Nanosecond)
	if _, e = VerifySecurityConfirmation(signedSecurityTest(t, key, c), public, c.ContextDigest, c.TransactionID, code, now); e == nil {
		t.Fatal("confirmation renewed its bound")
	}
}
