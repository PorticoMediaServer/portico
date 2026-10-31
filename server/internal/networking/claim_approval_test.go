package networking

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"strings"
	"testing"
	"time"
)

func signedApprovalFixture(raw string, key ed25519.PrivateKey) []byte {
	var wire struct {
		IssuedAt time.Time `json:"issuedAt"`
	}
	_ = json.Unmarshal([]byte(raw), &wire)
	if wire.IssuedAt.IsZero() {
		wire.IssuedAt = time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC)
	}
	cert, _ := trust.Certify(key, key.Public().(ed25519.PublicKey), "pin1", "documents", wire.IssuedAt.Add(-24*time.Hour), wire.IssuedAt.Add(120*24*time.Hour), 1, nil)
	out, _ := json.Marshal(approvalEnvelope{Payload: base64.RawURLEncoding.EncodeToString([]byte(raw)), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(raw))), KeyID: "pin1", Certificate: cert})
	return out
}
func approvalFixture(t *testing.T) (string, ed25519.PublicKey, ed25519.PrivateKey, time.Time) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 9, 6, 3, 1, 0, 0, time.UTC)
	raw := `{"kind":"portico.claim.approval","version":"2","audience":"https://hosted.example","operationId":"op","serverId":"server","accountId":"account","publicKey":"` + base64.RawURLEncoding.EncodeToString(make([]byte, 32)) + `","localGeneration":"0","approvalRevision":"1","purpose":"setup","currentOwnerAccountId":"","previousOwnerRole":"","returnRegistrationId":"","issuedAt":"2026-09-06T03:00:00Z","expiresAt":"2026-09-06T03:05:00Z"}`
	return raw, pub, key, now
}
func TestClaimApprovalExactSignedBinding(t *testing.T) {
	raw, pub, key, now := approvalFixture(t)
	proof := signedApprovalFixture(raw, key)
	facts, e := verifyApprovalAt(context.Background(), "https://hosted.example", trust.KeyID(pub), pub, proof, now)
	if e != nil {
		t.Fatal(e)
	}
	if facts.OperationID != "op" || facts.ServerID != "server" || facts.AccountID != "account" || facts.LocalGeneration != 0 || facts.Revision != 1 || facts.ExpiresAt.Sub(facts.IssuedAt) != 5*time.Minute {
		t.Fatal("incorrect authenticated binding")
	}
	for _, change := range []struct {
		audience, id string
		pin          ed25519.PublicKey
	}{{"https://other.example", trust.KeyID(pub), pub}, {"https://hosted.example", trust.KeyID(pub), make([]byte, 32)}, {"https://hosted.example", "pin1", pub}, {"https://hosted.example", trust.KeyID(make([]byte, 32)), pub}} {
		if _, e = verifyApprovalAt(context.Background(), change.audience, change.id, change.pin, proof, now); e == nil {
			t.Fatal("wrong trust accepted")
		}
	}
}
func TestClaimApprovalRejectsSignedNoncanonicalPayload(t *testing.T) {
	raw, pub, key, now := approvalFixture(t)
	cases := map[string]string{"duplicate": strings.Replace(raw, `"version":"2"`, `"version":"2","version":"2"`, 1), "numeric_alias": strings.Replace(raw, `"localGeneration":"0"`, `"localGeneration":"00"`, 1), "unknown": strings.TrimSuffix(raw, "}") + `,"unexpected":true}`, "offset_time": strings.Replace(raw, "03:00:00Z", "03:00:00+00:00", 1), "wrong_kind": strings.Replace(raw, "portico.claim.approval", "portico.claim.proof", 1), "zero_revision": strings.Replace(raw, `"approvalRevision":"1"`, `"approvalRevision":"0"`, 1), "trailing_document": raw + `{}`}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, e := verifyApprovalAt(context.Background(), "https://hosted.example", trust.KeyID(pub), pub, signedApprovalFixture(candidate, key), now); e == nil {
				t.Fatal("signed malformed facts accepted")
			}
		})
	}
}
// Hosted's plain responses decode open (decodeHostedResponse), but a signed
// statement never does: a field an older server ignored inside a signature
// could carry meaning it never checked. The approval is properly signed with
// the added field, and the verifier must still refuse it.
func TestSignedApprovalWithAnAddedFieldIsRefusedWhileResponsesDecodeOpen(t *testing.T) {
	raw, pub, key, now := approvalFixture(t)
	if _, e := verifyApprovalAt(context.Background(), "https://hosted.example", trust.KeyID(pub), pub, signedApprovalFixture(raw, key), now); e != nil {
		t.Fatalf("exact approval refused: %v", e)
	}
	added := strings.TrimSuffix(raw, "}") + `,"addedByANewerHosted":true}`
	if _, e := verifyApprovalAt(context.Background(), "https://hosted.example", trust.KeyID(pub), pub, signedApprovalFixture(added, key), now); e == nil {
		t.Fatal("signed approval with an added field accepted")
	}
	var plain struct {
		Kind string `json:"kind"`
	}
	if e := decodeHostedResponse([]byte(added), &plain); e != nil || plain.Kind != "portico.claim.approval" {
		t.Fatalf("the same bytes as a plain response did not decode open: %v", e)
	}
}
func TestClaimApprovalGrantLifetimeAndCancellation(t *testing.T) {
	raw, pub, key, now := approvalFixture(t)
	for _, candidate := range []string{strings.Replace(raw, "03:05:00Z", "03:06:00Z", 1), strings.Replace(raw, "03:05:00Z", "03:01:00Z", 1), strings.Replace(raw, "03:00:00Z", "03:01:06Z", 1)} {
		if _, e := verifyApprovalAt(context.Background(), "https://hosted.example", trust.KeyID(pub), pub, signedApprovalFixture(candidate, key), now); !errors.Is(e, ErrApprovalRequired) {
			t.Fatalf("invalid grant accepted: %v", e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := verifyApprovalAt(ctx, "https://hosted.example", trust.KeyID(pub), pub, signedApprovalFixture(raw, key), now); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled verifier succeeded")
	}
}
func TestClaimApprovalRejectsEnvelopeAndTampering(t *testing.T) {
	raw, pub, key, now := approvalFixture(t)
	proof := signedApprovalFixture(raw, key)
	candidates := [][]byte{append(append([]byte(nil), proof...), []byte(`{}`)...), []byte(strings.Replace(string(proof), `"keyId":"pin1"`, `"keyId":"pin1","keyId":"pin1"`, 1)), []byte(strings.Replace(string(proof), `"keyId":"pin1"`, `"keyId":"pin1","extra":1`, 1)), []byte(strings.Repeat("x", (16<<10)+1))}
	altered := append([]byte(nil), proof...)
	altered[15] = 'A'
	candidates = append(candidates, altered)
	for i, candidate := range candidates {
		if _, e := verifyApprovalAt(context.Background(), "https://hosted.example", trust.KeyID(pub), pub, candidate, now); e == nil {
			t.Fatalf("bad envelope accepted %d", i)
		}
	}
}
