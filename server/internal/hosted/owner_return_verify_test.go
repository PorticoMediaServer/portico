package hosted

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

func TestOwnerReturnVerifiesUnchangedSignedIdentityWithoutSpendingProof(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	installation := "d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8"
	challenge, _, err := f.service.IssueChallenge(ctx, installation)
	if err != nil {
		t.Fatal(err)
	}
	q := serverreturn.OwnerExchange{TransactionID: "actual_owner_transaction", State: strings.Repeat("s", 43), Verifier: strings.Repeat("v", 43), RegistrationID: "ret_current_registration", InstallationID: installation, ServerChallenge: challenge, Purpose: "switch"}
	now := time.Now().UTC()
	c := serverReturnAssertion{Kind: "portico.server-return.v1", AccountID: f.installed.AccountID, Username: "approved_owner", DisplayName: "Current identity", Email: "verified@example.test", ServerID: f.id.ID(), Challenge: challenge, Nonce: strings.Repeat("n", 43), IssuedAt: now, ExpiresAt: now.Add(time.Minute), Purpose: q.Purpose, InstallationID: installation, RegistrationID: q.RegistrationID, ClaimOperationID: f.installed.OperationID, ClaimGeneration: 1, CredentialGeneration: 1}
	encode := func(c serverReturnAssertion) []byte {
		raw, _ := json.Marshal(c)
		signed := Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.hostedKey, raw)), KeyID: "pin1", Certificate: testSigningCertificate(f.hostedKey, "pin1")}
		grant, _ := json.Marshal(struct {
			Assertion Signed `json:"assertion"`
		}{signed})
		return grant
	}
	raw := encode(c)
	who, nonce, err := f.service.VerifyOwnerReturn(ctx, raw, q)
	if err != nil || who.AccountID != c.AccountID || who.Username != c.Username || nonce != c.Nonce || who.Return.Purpose != "switch" {
		t.Fatal("genuine unchanged signature refused", err)
	}
	var consent Signed
	if json.Unmarshal([]byte(who.Consent), &consent) != nil || consent.Payload == "" {
		t.Fatal("original discovery consent lost")
	}
	payload, _ := base64.RawURLEncoding.DecodeString(consent.Payload)
	var retained serverReturnAssertion
	if json.Unmarshal(payload, &retained) != nil || retained.Kind != "portico.server-return.v1" || retained.Email != c.Email {
		t.Fatal("assertion relabelled or fact fabricated")
	}
	for _, change := range []func(*serverReturnAssertion){func(c *serverReturnAssertion) { c.Purpose = "sign-in" }, func(c *serverReturnAssertion) { c.CurrentOwnerAccountID = "another_owner" }, func(c *serverReturnAssertion) { c.PreviousOwnerRole = "member" }, func(c *serverReturnAssertion) { c.ServerID = "srv_other" }, func(c *serverReturnAssertion) { c.RegistrationID = "ret_other" }, func(c *serverReturnAssertion) { c.InstallationID = strings.Repeat("i", 43) }, func(c *serverReturnAssertion) { c.Challenge = strings.Repeat("c", 32) }, func(c *serverReturnAssertion) { c.ExpiresAt = c.IssuedAt.Add(time.Minute + time.Nanosecond) }} {
		next := c
		change(&next)
		if _, _, err = f.service.VerifyOwnerReturn(ctx, encode(next), q); err == nil {
			t.Fatal("wrong signed owner binding accepted")
		}
	}
	for _, invalid := range [][]byte{[]byte(`null`), []byte(`{"assertion":null}`), []byte(`{"assertion":{},"unknown":true}`), append(append([]byte(nil), raw...), []byte(` {}`)...), []byte(`{"assertion":{},"assertion":{}}`)} {
		if _, _, err = f.service.VerifyOwnerReturn(ctx, invalid, q); err == nil {
			t.Fatal("non-closed grant accepted")
		}
	}
	var challenges, nonces int
	if err = f.db.QueryRow(`SELECT (SELECT count(*) FROM hosted_identity_challenges),(SELECT count(*) FROM hosted_identity_nonces)`).Scan(&challenges, &nonces); err != nil || challenges != 1 || nonces != 0 {
		t.Fatal("verification spent admission proof", err, challenges, nonces)
	}
	if _, err = f.db.Exec(`INSERT INTO hosted_signing_revocations(key_id) VALUES('pin1')`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.service.VerifyOwnerReturn(ctx, raw, q); err == nil {
		t.Fatal("revoked signing key accepted")
	}
}
