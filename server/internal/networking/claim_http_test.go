package networking

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type claimRoundTrip func(*http.Request) (*http.Response, error)

func (f claimRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type credentialFixture struct {
	secret *Secret
	calls  int
}

func (f *credentialFixture) Current(context.Context, Intent) error { return nil }

func (f *credentialFixture) InstalledCredential(context.Context, Intent) (*Secret, error) {
	f.calls++
	return f.secret, nil
}
func httpFixture(t *testing.T) (*HTTPTransport, Intent, SignedProof, *credentialFixture) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	v := Intent{Binding: Binding{OperationID: "op", ServerID: "server", AccountID: "account", PublicKey: pub, LocalGeneration: 0}, Revision: 1, Stage: Installed, ApprovalRevision: 1, ClaimGeneration: "1", CredentialGeneration: "2"}
	raw := []byte(`{"kind":"test-only-signed-by-server"}`)
	p := SignedProof{Payload: raw, Signature: ed25519.Sign(key, raw)}
	secret, e := NewSecret([]byte(strings.Repeat("s", 32)))
	if e != nil {
		t.Fatal(e)
	}
	f := &credentialFixture{secret: secret}
	transport, e := NewHTTPTransport("https://hosted.example", f)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(transport.CloseIdleConnections)
	return transport, v, p, f
}
func reply(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

const exactCommit = `{"operationId":"op","serverId":"server","claimGeneration":"1","credentialGeneration":"2"}`

func TestClaimHTTPTransportConfiguration(t *testing.T) {
	f := &credentialFixture{}
	for _, origin := range []string{"http://hosted.example", "https://user@hosted.example", "https://hosted.example/path", "https://hosted.example?next=x", "https://HOSTED.example"} {
		if _, e := NewHTTPTransport(origin, f); e == nil {
			t.Fatalf("accepted invalid origin %q", origin)
		}
	}
	tr, _, _, _ := httpFixture(t)
	rt := tr.client.Transport.(*http.Transport)
	if rt.Proxy != nil || rt.TLSClientConfig.InsecureSkipVerify || rt.MaxConnsPerHost != 2 || !rt.DisableCompression {
		t.Fatal("transport safety bounds missing")
	}
	if tr.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect allowed")
	}
}
func TestClaimHTTPAckUsesAndClearsExactCredential(t *testing.T) {
	tr, v, _, f := httpFixture(t)
	calls := 0
	tr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://hosted.example/v1/server-claims/ack" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
			t.Fatal("incorrect scoped ack request")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != exactCommit {
			t.Fatalf("wrong ack identity: %s", body)
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 8*time.Second {
			t.Fatal("no bounded deadline")
		}
		return reply(200, exactCommit), nil
	})
	if e := tr.Acknowledge(fixtureClaimContext(t), v); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || f.calls != 1 || f.secret.Use(func([]byte) error { return nil }) == nil {
		t.Fatal("credential lifetime not bounded")
	}
}
func TestClaimHTTPResultRejectsWrongGeneration(t *testing.T) {
	tr, v, p, f := httpFixture(t)
	tr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Fatal("credential leaked to proof exchange")
		}
		return reply(200, `{"operationId":"op","serverId":"server","claimGeneration":"1","credentialGeneration":"3","serverCredential":"`+strings.Repeat("x", 32)+`"}`), nil
	})
	result, e := tr.Retrieve(fixtureClaimContext(t), v, p)
	if !errors.Is(e, ErrStale) || result.Credential != nil || f.calls != 0 {
		t.Fatal("different credential generation accepted")
	}
}
func TestClaimHTTPResultScopedSecret(t *testing.T) {
	tr, v, p, _ := httpFixture(t)
	tr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		var body proofEnvelope
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Payload == "" || body.Signature == "" {
			t.Fatal("missing signed envelope")
		}
		return reply(200, strings.TrimSuffix(exactCommit, "}")+`,"serverCredential":"`+strings.Repeat("x", 32)+`"}`), nil
	})
	result, e := tr.Retrieve(fixtureClaimContext(t), v, p)
	if e != nil {
		t.Fatal(e)
	}
	defer result.Credential.Clear()
	if _, e = json.Marshal(result); e == nil {
		t.Fatal("credential serializable")
	}
	if e = result.Credential.Use(func(b []byte) error {
		if string(b) != strings.Repeat("x", 32) {
			t.Fatal("wrong result bytes")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestClaimHTTPRejectsMalformedResponses(t *testing.T) {
	for name, body := range map[string]string{"extra_document": exactCommit + `{}`, "unknown_field": strings.TrimSuffix(exactCommit, "}") + `,"extra":1}`, "oversize": strings.Repeat(" ", claimHTTPBound) + exactCommit, "invalid_json": "{"} {
		t.Run(name, func(t *testing.T) {
			tr, v, p, _ := httpFixture(t)
			tr.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) { return reply(200, body), nil })
			if _, e := tr.Finalize(fixtureClaimContext(t), v, p); e == nil {
				t.Fatal("malformed response accepted")
			}
		})
	}
}
func TestClaimHTTPFailuresDoNotBecomePermanentDenial(t *testing.T) {
	for _, code := range []int{301, 401, 403, 500, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			tr, v, p, f := httpFixture(t)
			tr.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) {
				return reply(code, `{"error":{"code":"claim_cancelled","message":"x","retryable":false}}`), nil
			})
			if _, e := tr.Finalize(fixtureClaimContext(t), v, p); !errors.Is(e, ErrUnavailable) {
				t.Fatalf("generic response granted terminal semantics: %v", e)
			}
			if f.calls != 0 {
				t.Fatal("proof exchange read installed secret")
			}
		})
	}
}
func TestClaimHTTPExplicitConflictAndCancellation(t *testing.T) {
	tr, v, p, _ := httpFixture(t)
	for code, want := range map[string]error{"approval_required": ErrApprovalRequired, "claim_cancelled": ErrCancelled, "claim_stale": ErrStale} {
		tr.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) {
			return reply(409, `{"error":{"code":"`+code+`","message":"x","retryable":false}}`), nil
		})
		if _, e := tr.Finalize(fixtureClaimContext(t), v, p); e != want {
			t.Fatalf("wrong conflict: %v", e)
		}
	}
	ctx, cancel := context.WithCancel(fixtureClaimContext(t))
	cancel()
	tr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
	if _, e := tr.Finalize(ctx, v, p); e != context.Canceled {
		t.Fatalf("lost cancellation: %v", e)
	}
}

// Hosted writes every error as {"error":{"code","message","retryable"}} (see
// fail in portico-internal/hosted-services/hosted/internal/httpapi/handler.go). The 409 branch must
// read that nested envelope; the old top-level {"code"} shape never came from
// Hosted and stays ErrUnavailable.
func TestClaimHTTPDecodesHostedConflictEnvelope(t *testing.T) {
	tr, _, _, _ := httpFixture(t)
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	b := Binding{OperationID: "op", ServerID: "server", AccountID: "account", PublicKey: pub, LocalGeneration: 0}
	raw := []byte(`{"kind":"portico.claim.cancel","version":"1","audience":"https://hosted.example","operationId":"op","serverId":"server","accountId":"account","publicKey":"` + base64.RawURLEncoding.EncodeToString(pub) + `","localGeneration":"0","requestId":"cancel1","mode":"intent"}`)
	item := Cancellation{Binding: b, RequestID: "cancel1", Proof: SignedProof{Payload: raw, Signature: ed25519.Sign(key, raw)}}
	for code, want := range map[string]error{"approval_required": ErrApprovalRequired, "claim_cancelled": ErrCancelled, "claim_stale": ErrStale} {
		tr.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) {
			return reply(409, `{"error":{"code":"`+code+`","message":"x","retryable":false}}`), nil
		})
		if e = tr.Cancel(fixtureClaimContext(t), item); !errors.Is(e, want) {
			t.Fatalf("nested %s mapped to %v", code, e)
		}
	}
	tr.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) { return reply(409, `{"code":"claim_cancelled"}`), nil })
	if e = tr.Cancel(fixtureClaimContext(t), item); !errors.Is(e, ErrUnavailable) {
		t.Fatalf("top-level code accepted: %v", e)
	}
}
