package networking

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClaimHTTPNonceWireEnvelope(t *testing.T) {
	tr, v, p, _ := httpFixture(t)
	nonce := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	tr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/server-claims/nonce" || r.Header.Get("Authorization") != "" {
			t.Fatal("nonce used wrong endpoint/authority")
		}
		var in proofEnvelope
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Payload != base64.RawURLEncoding.EncodeToString(p.Payload) || in.Signature != base64.RawURLEncoding.EncodeToString(p.Signature) {
			t.Fatal("nonce changed signed bytes")
		}
		return reply(200, `{"requestDigest":"digest","nonceId":"nonce1","nonce":"`+nonce+`","issuedAt":"2026-09-06T03:00:00Z","expiresAt":"2026-09-06T03:00:30Z"}`), nil
	})
	challenge, e := tr.Challenge(fixtureClaimContext(t), v, FinalizePurpose, p)
	if e != nil {
		t.Fatal(e)
	}
	if challenge.NonceID != "nonce1" || challenge.RequestDigest != "digest" || len(challenge.Nonce) != 32 || challenge.ExpiresAt.Sub(challenge.IssuedAt) != 30*time.Second {
		t.Fatal("nonce response changed")
	}
	tr.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) {
		return reply(200, `{"requestDigest":"digest","nonceId":"nonce1","nonce":"AA","issuedAt":"2026-09-06T03:00:00Z","expiresAt":"2026-09-06T03:00:30Z"}`), nil
	})
	if _, e = tr.Challenge(fixtureClaimContext(t), v, ResultPurpose, p); !errors.Is(e, ErrInvalid) {
		t.Fatal("short nonce accepted")
	}
}
func TestClaimHTTPCancelExactReceipt(t *testing.T) {
	tr, _, _, f := httpFixture(t)
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	b := Binding{OperationID: "op", ServerID: "server", AccountID: "account", PublicKey: pub, LocalGeneration: 0}
	raw := []byte(`{"kind":"portico.claim.cancel","version":"1","audience":"https://hosted.example","operationId":"op","serverId":"server","accountId":"account","publicKey":"` + base64.RawURLEncoding.EncodeToString(pub) + `","localGeneration":"0","requestId":"cancel1","mode":"intent"}`)
	item := Cancellation{Binding: b, RequestID: "cancel1", Proof: SignedProof{Payload: raw, Signature: ed25519.Sign(key, raw)}}
	for _, response := range []struct {
		body string
		want error
	}{{`{"operationId":"op","requestId":"cancel1"}`, nil}, {`{"operationId":"op","requestId":"old"}`, ErrStale}, {`{"operationId":"successor","requestId":"cancel1"}`, ErrStale}} {
		tr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/server-claims/cancel" || r.Header.Get("Authorization") != "" {
				t.Fatal("cancel used account/server bearer")
			}
			return reply(200, response.body), nil
		})
		if e = tr.Cancel(fixtureClaimContext(t), item); !errors.Is(e, response.want) {
			t.Fatalf("wrong cancellation disposition: %v", e)
		}
	}
	if f.calls != 0 {
		t.Fatal("cancellation read installed credential")
	}
}
func TestClaimHTTPAckClearsOnFailure(t *testing.T) {
	for _, status := range []int{401, 503} {
		tr, v, _, f := httpFixture(t)
		tr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				t.Fatal("ack omitted credential")
			}
			return reply(status, `{"code":"unavailable"}`), nil
		})
		if e := tr.Acknowledge(fixtureClaimContext(t), v); !errors.Is(e, ErrUnavailable) {
			t.Fatalf("unexpected failure: %v", e)
		}
		if f.secret.Use(func([]byte) error { return nil }) == nil {
			t.Fatal("ack failure retained secret")
		}
	}
}
