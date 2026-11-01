package hosted

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Confirming a preview cannot turn an ordinary registered exchange into an
// owner-control-plane exception. Only the separate signed claim initiation may
// start while Local; owner return exchange requires the actual installed tuple.
func TestLocalPreviewCannotAuthorizeOrdinaryReturnEgress(t *testing.T) {
	for _, mode := range []string{"no-preview", "switch-confirmed", "start-over-confirmed"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			db, e := persistence.Open(filepath.Join(root, "server.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { db.Close() })
			id, e := identity.New(db, root)
			if e != nil {
				t.Fatal(e)
			}
			operations.WireSetupRemote(id)
			ctx, e := identity.WithIssuingDevice(context.Background(), identity.DeviceRegistration{InstallationID: identity.Token(), Name: "Owner browser", Platform: "web", App: "portico"}, "127.0.0.1:1234")
			if e != nil {
				t.Fatal(e)
			}
			secret, e := id.BrowserSetupToken(ctx)
			if e != nil {
				t.Fatal(e)
			}
			setup, e := id.SetupWithOptions(ctx, identity.SetupOptions{SetupToken: secret, AuthMode: "local", Username: "owner", Password: "Local-password1!", Name: "Fixture"}, true)
			if e != nil {
				t.Fatal(e)
			}
			if mode != "no-preview" {
				preview, e := id.PreviewAccountMove(ctx, setup.Envelope.AccessToken, identity.MovePreviewRequest{To: identity.SignInPortico, StartOver: mode == "start-over-confirmed"})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = id.ConfirmPorticoMove(ctx, setup.Envelope.AccessToken, identity.MoveConfirmRequest{MoveID: preview.ID, ExpectedRevision: preview.Revision, Confirmation: preview.ServerName}); e != nil {
					t.Fatal(e)
				}
			}
			pub, _, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				t.Fatal(e)
			}
			service, e := New(db, id, "https://hosted.example", base64.RawURLEncoding.EncodeToString(pub), trust.KeyID(pub))
			if e != nil {
				t.Fatal(e)
			}
			requests := 0
			service.client = &http.Client{Transport: returnRoundTripper(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, errors.New("unexpected ordinary exchange while Local")
			})}
			before := func() (families, links, claims int) {
				t.Helper()
				if e := db.QueryRow(`SELECT (SELECT count(*) FROM authorization_session_families),(SELECT count(*) FROM account_portico_links),(SELECT count(*) FROM networking_claim_intents)`).Scan(&families, &links, &claims); e != nil {
					t.Fatal(e)
				}
				return
			}
			families, links, claims := before()
			q := RegisteredReturnExchange{Code: strings.Repeat("c", 43), TransactionID: "return_transaction", Verifier: strings.Repeat("v", 43), RegistrationID: "ret_valid_shape", InstallationID: strings.Repeat("i", 43), ServerChallenge: strings.Repeat("s", 43), Purpose: "switch"}
			if _, e = service.ExchangeRegisteredReturn(ctx, q); !errors.Is(e, identity.ErrSignInKindLocal) || requests != 0 {
				t.Fatalf("Local ordinary return refusal=%v outbound requests=%d", e, requests)
			}
			gotFamilies, gotLinks, gotClaims := before()
			if gotFamilies != families || gotLinks != links || gotClaims != claims {
				t.Fatal("refused exchange changed durable identity")
			}
			if kind, e := id.SignInKind(ctx); e != nil || kind != identity.SignInLocal {
				t.Fatal("refusal changed server kind", e)
			}
			if _, e = id.Authenticate(setup.Envelope.AccessToken); e != nil {
				t.Fatal("refusal retired original owner", e)
			}
		})
	}
}

// The ordinary code exchange stays credential-free and purpose/tuple fenced
// after the Local exception is removed. This controlled HTTP reply carries a
// genuine fixture Ed25519 assertion; it is not a real Hosted PostgreSQL pair.
func TestPorticoOrdinaryReturnKeepsCodeAndCredentialFreeTransport(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	installation := strings.Repeat("i", 43)
	challenge, _, e := f.service.IssueChallenge(ctx, installation)
	if e != nil {
		t.Fatal(e)
	}
	q := RegisteredReturnExchange{Code: strings.Repeat("c", 43), TransactionID: "return_transaction", Verifier: strings.Repeat("v", 43), RegistrationID: "ret_valid_shape", InstallationID: installation, ServerChallenge: challenge, Purpose: "sign-in"}
	now := time.Now().UTC()
	c := serverReturnAssertion{Kind: "portico.server-return.v1", AccountID: f.installed.AccountID, Username: "verified_owner", ServerID: f.id.ID(), Challenge: challenge, Nonce: identity.Token(), IssuedAt: now, ExpiresAt: now.Add(time.Minute), Purpose: q.Purpose, InstallationID: q.InstallationID, RegistrationID: q.RegistrationID, ClaimOperationID: f.installed.OperationID, ClaimGeneration: 1, CredentialGeneration: 1}
	raw, _ := json.Marshal(c)
	signed := Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.hostedKey, raw)), KeyID: "pin1", Certificate: testSigningCertificate(f.hostedKey, "pin1")}
	grant, _ := json.Marshal(struct {
		Assertion Signed `json:"assertion"`
	}{signed})
	requests := 0
	f.service.client = &http.Client{Transport: returnRoundTripper(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.String() != "https://hosted.example/v1/server-returns/exchange" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("ordinary exchange transport authority changed")
		}
		var got RegisteredReturnExchange
		if json.NewDecoder(r.Body).Decode(&got) != nil || got != q || got.Code == "" {
			t.Fatal("ordinary exchange code/tuple changed")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(grant))}, nil
	})}
	who, e := f.service.ExchangeRegisteredReturn(ctx, q)
	if e != nil || requests != 1 || who.AccountID != f.installed.AccountID || who.Return == nil || who.Return.Purpose != "sign-in" {
		t.Fatal("ordinary Portico return refused", e)
	}
}
