package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	hostedtrust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// POST /v1/setup/approval does in one call what a setup page did in four: with no
// claim active it prepares one, signs this caller's setup start and registers it with
// Portico Services, under the setup token's own guard and capture. A repeat with the
// same start replays the stored signed pair instead of preparing again.
func TestSetupApprovalPreparesSignsAndRegistersInOneCall(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gate, err := authoritygate.NewAuthorityGate(strings.Repeat("a", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAuthorityRunner(func(ctx context.Context) (LifecycleLease, error) { return gate.Acquire(ctx) })
	if err != nil {
		t.Fatal(err)
	}
	keys, err := OpenCurrentKeys(ctx, db, root, runner)
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Close()
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := id.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := id.SetupWithOptions(ctx, identity.SetupOptions{RequestID: "82a879b3-abf5-48c6-bba0-02ffd762f99c", SetupToken: secret, Name: "Setup server", AuthMode: "hosted"}, true); err != nil || out.Pending == nil {
		t.Fatal("pending hosted setup", err)
	}
	pending, proof, err := id.SetupClaimAuthority(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	hostedPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verify, err := NewApprovalVerifier("https://hosted.example", hostedtrust.KeyID(hostedPub), hostedPub)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := OpenClaimCipher(ctx, db, root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(db, cipher, GuardLocalOwner, GuardInstalledClaim, verify, "https://hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewHTTPTransport("https://hosted.example", store)
	if err != nil {
		t.Fatal(err)
	}
	setupActor := ClaimRequestOwner{Owner: LocalOwner{SetupOperationID: pending.RequestID, Epoch: pending.Revision}, WebOrigin: "http://localhost:32500",
		Guard: func(ctx context.Context, tx *sql.Tx) error {
			return id.CheckSetupClaimTx(ctx, tx, pending.RequestID, pending.Revision, proof)
		},
		SetupStart: &SetupStartAuthority{RequestID: pending.RequestID, Revision: pending.Revision,
			CaptureTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed) error {
				return id.CaptureSetupClaimTx(ctx, tx, pending, proof, original, start, audience)
			},
			RenewTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed) error {
				return id.RenewSetupClaimTx(ctx, tx, pending, proof, original, start, audience)
			},
			BindTx: func(ctx context.Context, tx *sql.Tx, operation, transaction string) error {
				return id.BindSetupClaimTransactionTx(ctx, tx, pending, proof, operation, transaction)
			}}}
	actor := setupActor
	h, err := NewClaimHandler(store, runner, transport, keys, func(*http.Request) (ClaimRequestOwner, error) { return actor, nil }, id.Name)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var registered []claimweb.SetupStart
	transport.client = &http.Client{Transport: ownerPendingRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://hosted.example/v1/server-claims/events":
			<-r.Context().Done() // the server's own wait for the decision
			return nil, r.Context().Err()
		case "https://hosted.example/v1/server-claims/pending":
		default:
			t.Errorf("unexpected Hosted request %s", r.URL)
			return nil, io.EOF
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var request claimweb.PendingRequest
		if err = json.Unmarshal(body, &request); err != nil || request.SetupStart == nil {
			t.Error("the setup start travels with its claim", err)
			return nil, io.EOF
		}
		claim, start, err := claimweb.VerifySetupStart(request.Original(), *request.SetupStart, "https://hosted.example", time.Now())
		if err != nil {
			t.Error("Hosted receives a verifiable signed pair", err)
			return nil, io.EOF
		}
		mu.Lock()
		registered = append(registered, start)
		mu.Unlock()
		out, _ := json.Marshal(claimweb.Pending{Purpose: "setup", Code: "claim_" + strings.Repeat("K", 43), SetupTransactionID: "setup_transaction", ServerID: claim.ServerID, Name: claim.Name, Address: claim.Address, RequestedAt: claim.IssuedAt, ExpiresAt: start.ExpiresAt, Status: "pending"})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(out))}, nil
	})}
	t.Cleanup(func() {
		h.webWatches.Range(func(key, value any) bool { h.StopSetupWatch(key.(string)); return true })
	})

	stateSum, pkce := sha256.Sum256([]byte(identity.Token())), sha256.Sum256([]byte(identity.Token()))
	start := ClaimSetupStartRequest{State: base64.RawURLEncoding.EncodeToString(stateSum[:]), CodeChallenge: base64.RawURLEncoding.EncodeToString(pkce[:]), InstallationID: identity.Token()}
	call := func(installation string) (int, ClaimStatus, string) {
		raw, _ := json.Marshal(SetupApprovalRequest{SetupStart: start})
		r := httptest.NewRequest("POST", "http://localhost:32500/v1/setup/approval", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Portico-Installation-Id", installation)
		w := httptest.NewRecorder()
		h.ServeSetupApproval(w, r)
		var out ClaimStatus
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, out, w.Body.String()
	}

	code, first, body := call(start.InstallationID)
	if code != 200 || first.Operation == nil || first.State != string(Prepared) || first.ApprovalURL == "" || first.ClaimCode == "" ||
		first.SetupContinuation == nil || first.SetupContinuation.TransactionID != "setup_transaction" || first.SetupContinuation.Claim == nil || first.SetupContinuation.Start == nil {
		t.Fatalf("first call: %d %s", code, body)
	}
	if len(registered) != 1 || registered[0].CodeChallenge != start.CodeChallenge || registered[0].InstallationID != start.InstallationID || registered[0].SetupRequestID != pending.RequestID {
		t.Fatalf("Portico Services got %+v", registered)
	}

	// The same start again: the stored signed pair, the same claim, nothing prepared anew.
	code, again, body := call(start.InstallationID)
	if code != 200 || again.Operation == nil || again.Operation.OperationID != first.Operation.OperationID || again.SetupContinuation == nil ||
		again.SetupContinuation.Start.Payload != first.SetupContinuation.Start.Payload {
		t.Fatalf("replay: %d %s", code, body)
	}

	// The installation must be the start's own.
	if code, _, body = call(identity.Token()); code == 200 {
		t.Fatalf("another installation was accepted: %s", body)
	}
	// Only the setup's own authority may ask: an owner without a setup start is refused.
	actor = ClaimRequestOwner{Owner: LocalOwner{AccountID: "account", ProfileID: "profile", Epoch: 1}, Guard: setupActor.Guard}
	if code, _, body = call(start.InstallationID); code != http.StatusUnauthorized {
		t.Fatalf("a caller without the setup's authority: %d %s", code, body)
	}
}
