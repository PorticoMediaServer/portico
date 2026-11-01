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
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	hostedtrust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/setupflow"
)

// A Portico Account that denies a first-run setup ends that claim on this server
// (setupflow: hosted_denied, awaiting approval back to started): the claim is
// cancelled and no longer active, its signed start ends, and the setup request
// stays and says it was declined. Continue then prepares a new claim with a new
// request on Portico Services, never the denied one again, and the setup no longer says so.
func TestADeniedSetupClaimEndsHereAndContinueStartsANewOne(t *testing.T) {
	decided := make(chan struct{})
	var once sync.Once
	decide := func() { once.Do(func() { close(decided) }) }
	t.Cleanup(decide)
	// Portico Services denies the first request only. The claim Continue starts
	// afterwards is a new request, still awaiting its own decision; answering it
	// "denied" too raced the test's last check.
	var denied atomic.Bool
	events := func(r *http.Request) (*http.Response, error) {
		select {
		case <-decided:
			if denied.CompareAndSwap(false, true) {
				return reply(200, `{"status":"denied"}`), nil
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	f := setupDecisionFixture(t, events)
	call, phase, db, pending := f.call, f.phase, f.db, f.pending
	registered := f.registered
	code, first, body := call()
	if code != 200 || first.Operation == nil {
		t.Fatalf("first approval: %d %s", code, body)
	}
	if p := phase(); p.Phase != setupflow.AwaitingApproval {
		t.Fatalf("before the decision: %+v", p)
	}
	decide()
	waitFor(t, func() bool { return phase().Phase == setupflow.Started })
	if p := phase(); p.Operation != nil || p.Cancelling || p.RequestID != pending.RequestID || p.Outcome != "denied" {
		t.Fatalf("after the denial the setup request stays, with no claim, and says it was declined: %+v", p)
	}
	var stage string
	if err := db.QueryRow(`SELECT stage FROM networking_claim_intents WHERE operation_id=?`, first.Operation.OperationID).Scan(&stage); err != nil || stage != "cancelled" {
		t.Fatalf("the denied claim is %q (%v)", stage, err)
	}

	// Continue again: a new claim and a new request on Portico Services.
	code, again, body := call()
	if code != 200 || again.Operation == nil || again.Operation.OperationID == first.Operation.OperationID {
		t.Fatalf("continue after the denial: %d %s", code, body)
	}
	if n := registered(); n != 2 {
		t.Fatalf("Portico Services got %d requests, want a new one after the denial", n)
	}
	if p := phase(); p.Phase != setupflow.AwaitingApproval || p.Outcome != "" {
		t.Fatalf("a new request no longer says declined: %+v", p)
	}
}

type setupDecision struct {
	h          *ClaimHandler
	db         *sql.DB
	pending    identity.PendingAccountSetup
	call       func() (int, ClaimStatus, string)
	phase      func() setupflow.State
	registered func() int
}

// setupDecisionFixture is a hosted first-run setup whose Portico Services decision stream
// answers with events: one approval call prepares, signs and registers a claim.
func setupDecisionFixture(t *testing.T, events func(*http.Request) (*http.Response, error)) setupDecision {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
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
	t.Cleanup(func() { keys.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
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
			return events(r)
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
	call := func() (int, ClaimStatus, string) {
		raw, _ := json.Marshal(SetupApprovalRequest{SetupStart: start})
		r := httptest.NewRequest("POST", "http://localhost:32500/v1/setup/approval", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Portico-Installation-Id", start.InstallationID)
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
	phase := func() setupflow.State {
		state, _, err := id.SetupState(ctx, secret, time.Now(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}

	return setupDecision{h: h, db: db, pending: pending, call: call, phase: phase,
		registered: func() int { mu.Lock(); defer mu.Unlock(); return len(registered) }}
}

// Portico Services answers exactly approved, denied or closed. Any other answer decides
// nothing: the claim stays prepared and the setup stays awaiting approval, never ended
// or approved on a guess.
func TestAnUnknownDecisionChangesNothing(t *testing.T) {
	var asked atomic.Int32
	events := func(r *http.Request) (*http.Response, error) {
		asked.Add(1)
		return reply(200, `{"status":"rejected"}`), nil
	}
	f := setupDecisionFixture(t, events)
	code, first, body := f.call()
	if code != 200 || first.Operation == nil {
		t.Fatalf("approval: %d %s", code, body)
	}
	waitFor(t, func() bool {
		_, watching := f.h.webWatches.Load(first.Operation.OperationID)
		return asked.Load() > 0 && !watching
	})
	if p := f.phase(); p.Phase != setupflow.AwaitingApproval || p.Outcome != "" || p.Operation == nil || p.Operation.ID != first.Operation.OperationID {
		t.Fatalf("an unknown decision changed the setup: %+v", p)
	}
	var stage string
	if err := f.db.QueryRow(`SELECT stage FROM networking_claim_intents WHERE operation_id=?`, first.Operation.OperationID).Scan(&stage); err != nil || stage != "prepared" {
		t.Fatalf("an unknown decision left the claim %q (%v)", stage, err)
	}
}
