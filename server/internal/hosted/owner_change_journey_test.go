package hosted

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"sync"
	"testing"
	"time"
)

type repairReply struct {
	Status            int             `json:"status"`
	Body              json.RawMessage `json:"body"`
	Root, TargetToken string
	ServerToken       string
	Headers           http.Header
	BodyEmpty         bool
	Finalizes         int
}
type repairBridge struct {
	t           *testing.T
	cmd         *exec.Cmd
	in          io.WriteCloser
	out         *bufio.Scanner
	mu          sync.Mutex
	root        []byte
	targetToken string
	diagnostics *repairDiagnostics
	serverToken string
}

// stdout protocol diagnostics and os/exec's stderr copier run independently.
type repairDiagnostics struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (d *repairDiagnostics) Write(raw []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.buffer.Write(raw)
}
func (d *repairDiagnostics) WriteString(raw string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.buffer.WriteString(raw)
}
func (d *repairDiagnostics) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.buffer.String()
}

func startOwnerBridge(t *testing.T, f *claimFixture) *repairBridge {
	t.Helper()
	checkout := os.Getenv("PORTICO_RETURN_HOSTED_CHECKOUT")
	if checkout == "" {
		t.Skip("real isolated Hosted checkout required")
	}
	files, _ := filepath.Glob(storagepaths.Path(f.state, "networking-keys", "*.ed25519"))
	if len(files) != 1 {
		t.Fatal("fixture key")
	}
	key, e := os.ReadFile(files[0])
	if e != nil {
		t.Fatal(e)
	}
	defer clear(key)
	raw, _ := json.Marshal(map[string]string{"ServerID": f.id.ID(), "PublicKey": base64.RawURLEncoding.EncodeToString(ed25519.PrivateKey(key).Public().(ed25519.PublicKey)), "PrivateKey": base64.RawURLEncoding.EncodeToString(key), "OperationID": f.installed.OperationID})
	input := filepath.Join(t.TempDir(), "fixture.json")
	if e = os.WriteFile(input, raw, 0600); e != nil {
		t.Fatal(e)
	}
	clear(raw)
	binary := filepath.Join(t.TempDir(), "hosted-producer.test")
	build := exec.Command("go", "test", "-c", "-o", binary, "./internal/postgres")
	build.Dir = filepath.Join(checkout, "hosted-services/hosted")
	if raw, e := build.CombinedOutput(); e != nil {
		t.Fatalf("real producer build: %v %s", e, raw)
	}
	cmd := exec.Command(binary, "-test.run=^TestOwnerChangeRepairBridge$", "-test.v", "-test.timeout=120s")
	cmd.Dir = filepath.Join(checkout, "hosted-services/hosted")
	cmd.Env = append(os.Environ(), "PORTICO_OWNER_REPAIR_BRIDGE=1", "PORTICO_RETURN_CONSUMER_INPUT="+input)
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	var stderr repairDiagnostics
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	b := &repairBridge{t: t, cmd: cmd, in: in, out: bufio.NewScanner(out), diagnostics: &stderr}
	b.out.Buffer(make([]byte, 4096), 256<<10)
	initial := b.read()
	b.root, e = base64.RawURLEncoding.DecodeString(initial.Root)
	if e != nil || len(b.root) != 32 {
		t.Fatal("producer root")
	}
	b.targetToken = initial.TargetToken
	b.serverToken = initial.ServerToken
	t.Cleanup(func() {
		_, _ = io.WriteString(in, "{\"Action\":\"stop\"}\n")
		in.Close()
		if e := cmd.Wait(); e != nil {
			t.Error("real Hosted bridge failed", e)
		}
	})
	return b
}
func (b *repairBridge) read() repairReply {
	b.t.Helper()
	reply, err := b.readResult()
	if err != nil {
		b.t.Fatal(err)
	}
	return reply
}
func (b *repairBridge) readResult() (repairReply, error) {
	for b.out.Scan() {
		line := b.out.Text()
		if !strings.HasPrefix(line, "PORTICO_REPAIR_JSON:") {
			b.diagnostics.WriteString(line + "\n")
			continue
		}
		var reply repairReply
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "PORTICO_REPAIR_JSON:")), &reply); err != nil {
			return repairReply{}, fmt.Errorf("bad producer reply: %w", err)
		}
		return reply, nil
	}
	return repairReply{}, fmt.Errorf("real Hosted bridge closed unexpectedly: %v: %s", b.out.Err(), b.diagnostics.String())
}
func (b *repairBridge) call(action, path, token string, body any) repairReply {
	b.t.Helper()
	reply, err := b.callResult(action, path, token, body)
	if err != nil {
		b.t.Fatal(err)
	}
	return reply
}
func (b *repairBridge) callResult(action, path, token string, body any) (repairReply, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, err := json.Marshal(map[string]any{"Action": action, "Path": path, "Token": token, "Operation": path, "Body": body, "Method": func() string {
		if path == "/v1/servers" {
			return "GET"
		}
		return "POST"
	}()})
	if err != nil {
		return repairReply{}, err
	}
	if _, err = b.in.Write(append(raw, '\n')); err != nil {
		return repairReply{}, err
	}
	return b.readResult()
}
func (b *repairBridge) post(path, token string, body any, out any) error {
	r, err := b.callResult("http", path, token, body)
	if err != nil {
		return err
	}
	if r.Status < 200 || r.Status >= 300 {
		return fmt.Errorf("actual producer %s status %d: %w", path, r.Status, networking.ErrUnavailable)
	}
	if out != nil {
		return json.Unmarshal(r.Body, out)
	}
	return nil
}

// This is a test channel adapter only: all bytes below are accepted or rejected
// by actual Hosted HTTP handlers/PostgreSQL consumers in the other Go module.
type repairTransport struct {
	b        *repairBridge
	store    *networking.SQLiteStore
	before   func()
	loseOnce bool
}

func proofBody(p networking.SignedProof) any {
	return map[string]string{"payload": base64.RawURLEncoding.EncodeToString(p.Payload), "signature": base64.RawURLEncoding.EncodeToString(p.Signature)}
}
func (x *repairTransport) Challenge(ctx context.Context, v networking.Intent, purpose networking.Purpose, p networking.SignedProof) (networking.Challenge, error) {
	var r struct {
		RequestDigest, NonceID, Nonce string
		IssuedAt, ExpiresAt           time.Time
	}
	e := x.b.post("/v1/server-claims/nonce", "", proofBody(p), &r)
	n, _ := base64.RawURLEncoding.DecodeString(r.Nonce)
	return networking.Challenge{RequestDigest: r.RequestDigest, NonceID: r.NonceID, Nonce: n, IssuedAt: r.IssuedAt, ExpiresAt: r.ExpiresAt}, e
}
func (x *repairTransport) Finalize(ctx context.Context, v networking.Intent, p networking.SignedProof) (networking.Commit, error) {
	if x.before != nil {
		x.before()
	}
	var r networking.Commit
	e := x.b.post("/v1/server-claims/finalize", "", proofBody(p), &r)
	if e == nil && x.loseOnce {
		x.loseOnce = false
		return networking.Commit{}, networking.ErrUnavailable
	}
	return r, e
}
func (x *repairTransport) Retrieve(ctx context.Context, v networking.Intent, p networking.SignedProof) (networking.Result, error) {
	var r struct{ OperationID, ServerID, ClaimGeneration, CredentialGeneration, ServerCredential string }
	e := x.b.post("/v1/server-claims/result", "", proofBody(p), &r)
	if e != nil {
		return networking.Result{}, e
	}
	secret, e := networking.NewSecret([]byte(r.ServerCredential))
	return networking.Result{Commit: networking.Commit{OperationID: r.OperationID, ServerID: r.ServerID, ClaimGeneration: r.ClaimGeneration, CredentialGeneration: r.CredentialGeneration}, Credential: secret}, e
}
func (x *repairTransport) Acknowledge(ctx context.Context, v networking.Intent) error {
	secret, e := x.store.InstalledCredential(ctx, v)
	if e != nil {
		return e
	}
	defer secret.Clear()
	return secret.Use(func(raw []byte) error {
		return x.b.post("/v1/server-claims/ack", string(raw), struct {
			OperationID          string `json:"operationId"`
			ServerID             string `json:"serverId"`
			ClaimGeneration      string `json:"claimGeneration"`
			CredentialGeneration string `json:"credentialGeneration"`
		}{v.OperationID, v.ServerID, v.ClaimGeneration, v.CredentialGeneration}, nil)
	})
}
func (x *repairTransport) Cancel(context.Context, networking.Cancellation) error {
	return errors.New("unexpected post-finalization cancellation in this journey")
}
func ownerRepairHandler(t *testing.T, f *claimFixture, b *repairBridge) (*networking.ClaimHandler, *repairTransport) {
	t.Helper()
	f.service.key = ed25519.PublicKey(b.root)
	f.service.keyID = trust.KeyID(b.root)
	verifier, e := networking.NewApprovalVerifier("https://hosted.example", trust.KeyID(b.root), ed25519.PublicKey(b.root))
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := networking.OpenClaimCipher(context.Background(), f.db, f.state)
	if e != nil {
		t.Fatal(e)
	}
	store, e := networking.NewSQLiteStore(f.db, cipher, networking.GuardLocalOwner, networking.GuardInstalledClaim, verifier, "https://hosted.example")
	if e != nil {
		t.Fatal(e)
	}
	f.store = store
	transport := &repairTransport{b: b, store: store}
	authorize := func(r *http.Request) (networking.ClaimRequestOwner, error) {
		p, e := f.id.Authenticate(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if e != nil || p.Authority != "local" || p.Role != "owner" {
			return networking.ClaimRequestOwner{}, identity.ErrUnauthorized
		}
		return networking.ClaimRequestOwner{Owner: networking.LocalOwner{AccountID: p.AccountID, ProfileID: p.ProfileID, Epoch: int64(p.Epoch)}, Guard: func(ctx context.Context, tx *sql.Tx) error { _, e := f.id.SessionFamilyTx(ctx, tx, p); return e }}, nil
	}
	h, e := networking.NewClaimHandler(store, f.runner, transport, f.keys, authorize, f.id.Name)
	if e != nil {
		t.Fatal(e)
	}
	return h, transport
}
func localClaimCall(t *testing.T, h *networking.ClaimHandler, token, path string, q any, want int) networking.ClaimStatus {
	t.Helper()
	out, err := localClaimResult(context.Background(), h, token, path, q, want)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Worker requests return their failures to the test goroutine. Fatal/Goexit
// inside a helper must never strand the caller waiting for a result.
func localClaimResult(ctx context.Context, h *networking.ClaimHandler, token, path string, q any, want int) (networking.ClaimStatus, error) {
	raw, err := json.Marshal(q)
	if err != nil {
		return networking.ClaimStatus{}, err
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:32500/v1/networking/claim/"+path, bytes.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		return networking.ClaimStatus{}, fmt.Errorf("local claim %s status %d want %d: %s", path, w.Code, want, w.Body.String())
	}
	var out networking.ClaimStatus
	if want == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			return out, fmt.Errorf("claim JSON: %w", err)
		}
	}
	return out, nil
}
func TestOwnerChangeRepairActualProducerConsumer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, scenario := range []string{"linked-collision", "name-collision", "concurrent-ambiguity", "success", "writer-failure", "success-member", "success-remove", "cancel", "decline", "expiry", "switch", "switch-existing-state"} {
		t.Run(scenario, func(t *testing.T) {
			f := newClaimFixture(t)
			ctx := context.Background()
			if _, e := f.db.Exec(`UPDATE account_portico_links SET hosted_account_id='strict-owner' WHERE account_id=?`, f.owner.Viewer.AccountID); e != nil {
				t.Fatal(e)
			}
			if _, e := f.db.Exec(`UPDATE networking_claim_intents SET account_id='strict-owner' WHERE operation_id=?`, f.installed.OperationID); e != nil {
				t.Fatal(e)
			}
			f.installed.AccountID = "strict-owner"
			if !strings.HasPrefix(scenario, "switch") {
				if _, e := f.db.Exec(`UPDATE onboarding_state_v1 SET auth_mode='hosted'`); e != nil {
					t.Fatal(e)
				}
			}
			f.id.SetupClaimReadyTx = networking.SetupClaimInstalledTx
			b := startOwnerBridge(t, f)
			h, tr := ownerRepairHandler(t, f, b)
			old := b.call("snapshot", "", "", nil)
			ownerID := f.owner.Viewer.AccountID
			profileID := f.owner.Viewer.ProfileID
			if scenario == "linked-collision" || scenario == "name-collision" {
				username := "unrelated"
				if scenario == "name-collision" {
					username = "strict-other"
				}
				// Existing Portico memberships carry no local password. Seed a
				// legitimate separate account; never use the retired mixed path.
				link := "another-portico-account"
				if scenario == "linked-collision" {
					link = "strict-other"
				}
				if _, e := f.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('collision-member',?,X'','collision-profile')`, username); e != nil {
					t.Fatal(e)
				}
				if _, e := f.db.Exec(`INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('collision-member',?)`, link); e != nil {
					t.Fatal(e)
				}
			}
			purpose, previous, target, token := "change-owner", "admin", "strict-other", b.targetToken
			if strings.HasPrefix(scenario, "switch") {
				purpose, previous, target, token = "switch", "", "strict-owner", "claim-owner-access-token-fixture"
			}
			if scenario == "success-member" {
				previous = "member"
			}
			if scenario == "success-remove" {
				previous = "remove"
			}
			q := networking.ClaimPrepareRequest{AccountID: target, Purpose: purpose, PreviousOwnerRole: previous, Expected: networking.ClaimIdentityExpected{ServerID: f.id.ID()}}
			if scenario == "linked-collision" || scenario == "success" {
				q.AccountID = ""
			}
			if scenario == "success" {
				// Actual strict prepare route negatives, before any pending mutation.
				bad := q
				bad.Purpose = "unbounded"
				localClaimCall(t, h, f.owner.AccessToken, "prepare", bad, 409)
				bad = q
				bad.PreviousOwnerRole = "owner"
				localClaimCall(t, h, f.owner.AccessToken, "prepare", bad, 400)
				bad = q
				bad.ReturnRegistrationID = "https://evil.example"
				localClaimCall(t, h, f.owner.AccessToken, "prepare", bad, 400)
				bad = q
				bad.Expected.LocalGeneration = 99
				localClaimCall(t, h, f.owner.AccessToken, "prepare", bad, 409)
				// Current owner is derived from the authenticated linked owner; not an input.
				localClaimCall(t, h, f.owner.AccessToken, "prepare", map[string]any{"currentOwnerAccountId": "invented", "expected": q.Expected, "purpose": q.Purpose, "previousOwnerRole": q.PreviousOwnerRole}, 400)
			}
			status := localClaimCall(t, h, f.owner.AccessToken, "prepare", q, 200)
			// P212-007: real installed access and guard remain live immediately after PREPARE.
			if e := f.runner.Do(ctx, func(ctx context.Context) error {
				v, e := f.store.InstalledIntent(ctx)
				if e != nil {
					return e
				}
				if v.OperationID != f.installed.OperationID {
					return errors.New("old pointer lost")
				}
				secret, e := f.store.InstalledCredential(ctx, v)
				if secret != nil {
					secret.Clear()
				}
				return e
			}); e != nil {
				t.Fatal("old installation lost at prepare", e)
			}
			v, e := f.store.Load(ctx, status.Operation.OperationID)
			if e != nil {
				t.Fatal(e)
			}
			now := time.Now().UTC()
			webAccount := v.AccountID
			if webAccount == networking.UnboundAccount {
				webAccount = ""
			}
			web := claimweb.Request{Kind: "portico.claim.web.v2", Audience: "https://hosted.example", OperationID: v.OperationID, ServerID: v.ServerID, AccountID: webAccount, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), Name: "Server", Address: "https://server.example:32500", Purpose: purpose, CurrentOwnerAccountID: "strict-owner", PreviousOwnerRole: previous, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
			raw, _ := json.Marshal(web)
			var signature []byte
			if e = f.runner.Do(ctx, func(ctx context.Context) error {
				var e error
				signature, e = f.keys.Sign(ctx, v.Binding, raw)
				return e
			}); e != nil {
				t.Fatal(e)
			}
			signed := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(signature)}
			var pending claimweb.Pending
			if e = b.post("/v1/server-claims/pending", "", signed, &pending); e != nil {
				t.Fatal("actual pending", e)
			}
			if scenario == "cancel" || scenario == "decline" {
				if scenario == "decline" {
					if e = b.post("/v1/server-claims/deny", token, map[string]string{"code": pending.Code}, nil); e != nil {
						t.Fatal(e)
					}
				}
				status = localClaimCall(t, h, f.owner.AccessToken, "cancel", map[string]any{"expected": status.Operation}, 200)
				after := b.call("snapshot", "", "", nil)
				if !bytes.Equal(old.Body, after.Body) || after.Finalizes != 0 {
					t.Fatal("decline/cancel changed old Hosted authority")
				}
				if e = f.runner.Do(ctx, func(ctx context.Context) error { _, e := f.store.InstalledCredential(ctx, f.installed); return e }); e != nil {
					t.Fatal("decline/cancel lost installed control plane", e)
				}
				retry := localClaimCall(t, h, f.owner.AccessToken, "prepare", q, 200)
				localClaimCall(t, h, f.owner.AccessToken, "cancel", map[string]any{"expected": retry.Operation}, 200)
				return
			}
			if e = b.post("/v1/server-claims/approve-code", token, map[string]string{"code": pending.Code}, nil); e != nil {
				t.Fatal("actual approval", e)
			}
			var events struct {
				Status           string
				ApprovalEnvelope json.RawMessage
			}
			if e = b.post("/v1/server-claims/events", "", signed, &events); e != nil || events.Status != "approved" {
				t.Fatal("actual events", e)
			}
			approve := struct {
				Expected         networking.ClaimExpected `json:"expected"`
				ApprovalEnvelope string                   `json:"approvalEnvelope,omitempty"`
			}{*status.Operation, string(events.ApprovalEnvelope)}
			if strings.HasSuffix(scenario, "collision") {
				localClaimCall(t, h, f.owner.AccessToken, "approve", approve, 409)
				after := b.call("snapshot", "", "", nil)
				if !bytes.Equal(old.Body, after.Body) || after.Finalizes != 0 {
					t.Fatal("Hosted authority mutated for refused collision")
				}
				var role, link string
				f.db.QueryRow(`SELECT role FROM direct_memberships WHERE account_id=?`, ownerID).Scan(&role)
				f.db.QueryRow(`SELECT hosted_account_id FROM account_portico_links WHERE account_id=?`, ownerID).Scan(&link)
				if role != "owner" || link != "strict-owner" {
					t.Fatal("local owner changed on refusal")
				}
				if e = f.runner.Do(ctx, func(ctx context.Context) error { _, e := f.store.InstalledIntent(ctx); return e }); e != nil {
					t.Fatal("old install lost on refusal", e)
				}
				return
			}
			status = localClaimCall(t, h, f.owner.AccessToken, "approve", approve, 200)
			if scenario == "expiry" {
				if _, e = f.db.Exec(`UPDATE networking_claim_intents SET approval_expires_at=? WHERE operation_id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), status.Operation.OperationID); e != nil {
					t.Fatal(e)
				}
				localClaimCall(t, h, f.owner.AccessToken, "continue", map[string]any{"expected": status.Operation}, 409)
				localClaimCall(t, h, f.owner.AccessToken, "cancel", map[string]any{"expected": status.Operation}, 200)
				after := b.call("snapshot", "", "", nil)
				if !bytes.Equal(old.Body, after.Body) || after.Finalizes != 0 {
					t.Fatal("expiry changed old Hosted authority")
				}
				if e = f.runner.Do(ctx, func(ctx context.Context) error { _, e := f.store.InstalledCredential(ctx, f.installed); return e }); e != nil {
					t.Fatal("expiry lost old control plane", e)
				}
				retry := localClaimCall(t, h, f.owner.AccessToken, "prepare", q, 200)
				localClaimCall(t, h, f.owner.AccessToken, "cancel", map[string]any{"expected": retry.Operation}, 200)
				return
			}

			if scenario == "concurrent-ambiguity" {
				entered, release := make(chan struct{}), make(chan struct{})
				var released sync.Once
				unblock := func() { released.Do(func() { close(release) }) }
				defer unblock()
				request, stop := context.WithTimeout(ctx, 15*time.Second)
				defer stop()
				tr.loseOnce = true
				tr.before = func() {
					close(entered)
					select {
					case <-release:
					case <-request.Done():
					}
				}
				type claimResult struct {
					status networking.ClaimStatus
					err    error
				}
				done := make(chan claimResult, 1)
				go func() {
					result, err := localClaimResult(request, h, f.owner.AccessToken, "continue", map[string]any{"expected": status.Operation}, 503)
					done <- claimResult{result, err}
				}()
				select {
				case <-entered:
				case result := <-done:
					t.Fatalf("claim returned before finalization barrier: %v", result.err)
				case <-request.Done():
					t.Fatal("claim never reached finalization barrier", request.Err())
				}
				// These independent SQL writers race the real network finalization; none
				// relies on an API preflight or an in-memory lock.
				other, e := sql.Open("sqlite", storagepaths.Path(f.state, "server.sqlite"))
				if e != nil {
					t.Fatal(e)
				}
				defer other.Close()
				for _, query := range []string{`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('race','STRICT-OTHER',X'','race-profile')`, `UPDATE accounts SET username='strict-other' WHERE id='` + ownerID + `'`, `INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('race','strict-other')`} {
					if _, e = other.Exec(query); e == nil || !strings.Contains(e.Error(), "owner_change_identity_reserved") {
						t.Fatalf("competing identity writer bypassed durable collision fence: %s: %v", query, e)
					}
				}
				unblock()
				select {
				case result := <-done:
					if result.err != nil {
						t.Fatal(result.err)
					}
				case <-request.Done():
					t.Fatal("claim did not finish after finalization was released", request.Err())
				}
				tr.before = nil
				var reserved bool
				f.db.QueryRow(`SELECT owner_change_reserved FROM networking_claim_intents WHERE operation_id=?`, status.Operation.OperationID).Scan(&reserved)
				if !reserved {
					t.Fatal("ambiguous response released reservation")
				}
				loaded, e := f.store.Load(ctx, status.Operation.OperationID)
				if e != nil || loaded.Stage != networking.Finalizing {
					t.Fatal("ambiguous state lost")
				}
				status.Operation = &networking.ClaimExpected{OperationID: loaded.OperationID, LocalGeneration: loaded.LocalGeneration, Revision: loaded.Revision}
			}
			for range 3 {
				status = localClaimCall(t, h, f.owner.AccessToken, "continue", map[string]any{"expected": status.Operation}, 200)
			}
			if !status.InstallationAcknowledged {
				t.Fatal("replacement not acknowledged")
			}
			b.call("route", status.Operation.OperationID, "", nil)
			var lookup struct{ ReturnRegistration *struct{ ID, Origin string } }
			if e = b.post("/v1/server-claims/lookup", token, map[string]string{"code": pending.Code}, &lookup); e != nil || lookup.ReturnRegistration == nil {
				t.Fatal("post-install actual lookup stranded", e)
			}
			f.service.client = &http.Client{Transport: returnRoundTripper(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				reply := b.call("http", "/v1/server-returns/exchange", "", json.RawMessage(raw))
				return &http.Response{StatusCode: reply.Status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(reply.Body))}, nil
			})}
			consumeReturn := func() identity.PorticoIdentity {
				install := strings.Repeat("i", 43)
				challenge, _, e := f.service.IssueChallenge(ctx, install)
				if e != nil {
					t.Fatal(e)
				}
				verifier := strings.Repeat("v", 43)
				sum := sha256.Sum256([]byte(verifier))
				start := map[string]string{"serverId": f.id.ID(), "purpose": purpose, "claimOperationId": status.Operation.OperationID, "registrationId": lookup.ReturnRegistration.ID, "state": strings.Repeat("s", 43), "codeChallenge": base64.RawURLEncoding.EncodeToString(sum[:]), "serverChallenge": challenge, "installationId": install}
				var started struct{ TransactionID string }
				if e = b.post("/v1/server-returns/start", token, start, &started); e != nil {
					t.Fatal("actual start", e)
				}
				var completion struct{ Location string }
				if e = b.post("/v1/server-returns/"+started.TransactionID+"/complete", token, struct{}{}, &completion); e != nil {
					t.Fatal("actual complete", e)
				}
				u, _ := url.Parse(completion.Location)
				values, _ := url.ParseQuery(u.Fragment)
				exchange := RegisteredReturnExchange{Code: values.Get("code"), TransactionID: started.TransactionID, Verifier: verifier, RegistrationID: lookup.ReturnRegistration.ID, ServerChallenge: challenge, InstallationID: install, Purpose: purpose}

				who, e := f.service.ExchangeRegisteredReturn(ctx, exchange)
				if e != nil {
					t.Fatal("actual signed exchange consumer", e)
				}
				return who
			}
			who := consumeReturn()

			var revision, epoch int64
			f.db.QueryRow(`SELECT revision FROM direct_memberships WHERE account_id=?`, ownerID).Scan(&revision)
			f.db.QueryRow(`SELECT epoch FROM accounts WHERE id=?`, ownerID).Scan(&epoch)
			if scenario == "switch-existing-state" {
				if _, e = f.db.Exec(`INSERT INTO identity_credential_state(account_id,must_change) VALUES(?,0) ON CONFLICT(account_id) DO UPDATE SET must_change=0`, ownerID); e != nil {
					t.Fatal(e)
				}
			}
			if scenario == "writer-failure" {
				if _, e = f.db.Exec(`CREATE TRIGGER fixture_owner_write_failure BEFORE INSERT ON accounts WHEN NEW.username='strict-other' BEGIN SELECT RAISE(ABORT,'fixture_owner_writer_failure'); END`); e != nil {
					t.Fatal(e)
				}
				if _, e = f.id.CompletePorticoOwnerClaim(ctx, f.owner.AccessToken, revision, who); e == nil {
					t.Fatal("injected writer failure published ownership")
				}
				var reserved bool
				var role string
				f.db.QueryRow(`SELECT owner_change_reserved FROM networking_claim_intents WHERE operation_id=?`, who.Return.OperationID).Scan(&reserved)
				f.db.QueryRow(`SELECT role FROM direct_memberships WHERE account_id=?`, ownerID).Scan(&role)
				if !reserved || role != "owner" {
					t.Fatal("writer rollback lost collision fence or old membership")
				}
				if _, e = f.db.Exec(`DROP TRIGGER fixture_owner_write_failure`); e != nil {
					t.Fatal(e)
				}
				who = consumeReturn() // a fresh actual one-use code and challenge, never replay
			}
			out, e := f.id.CompletePorticoOwnerClaim(ctx, f.owner.AccessToken, revision, who)
			if e != nil || out.Session == nil {
				t.Fatal("actual local return writer", e)
			}
			if purpose == "switch" {
				var changed bool
				var newEpoch int64
				var hash []byte
				f.db.QueryRow(`SELECT must_change FROM identity_credential_state WHERE account_id=?`, ownerID).Scan(&changed)
				f.db.QueryRow(`SELECT epoch,password_hash FROM accounts WHERE id=?`, ownerID).Scan(&newEpoch, &hash)
				if changed || newEpoch != epoch+1 || len(hash) != 0 || out.Account.ID != ownerID {
					t.Fatal("canonical switch facts")
				}
				if _, e = f.id.Authenticate(f.owner.AccessToken); e == nil {
					t.Fatal("old switch credential survived")
				}
			} else {
				if out.Account.HostedAccountID != "strict-other" || out.Account.Role != "owner" {
					t.Fatal("wrong local target")
				}
			}
			caps, e := f.id.Capabilities(ctx)
			if e != nil {
				t.Fatal(e)
			}
			hasHosted := false
			for _, method := range caps.Methods {
				if method == "password" {
					t.Fatal("Portico server advertised password")
				}
				hasHosted = hasHosted || method == "hosted"
			}
			if !hasHosted {
				t.Fatal("genuine installed Portico authority not advertised")
			}
			// Actual authenticated directory reflects each genuine ownership outcome.
			listed := b.call("http", "/v1/servers", token, nil)
			if listed.Status != 200 {
				t.Fatal("directory after transfer")
			}
			var directory struct {
				Items []struct {
					Role      string
					OwnedByMe bool
				}
			}
			if json.Unmarshal(listed.Body, &directory) != nil || len(directory.Items) != 1 || directory.Items[0].Role != "owner" || !directory.Items[0].OwnedByMe {
				t.Fatal("target directory role")
			}
			if purpose == "change-owner" {
				listed = b.call("http", "/v1/servers", "claim-owner-access-token-fixture", nil)
				if listed.Status != 200 {
					t.Fatal("previous owner list")
				}
				if json.Unmarshal(listed.Body, &directory) != nil {
					t.Fatal("directory")
				}
				if previous == "remove" {
					if len(directory.Items) != 0 {
						t.Fatal("removed previous owner listed")
					}
				} else if len(directory.Items) != 1 || directory.Items[0].Role != previous || directory.Items[0].OwnedByMe {
					t.Fatal("previous-owner role guessed")
				}
			}
			if scenario == "success" || scenario == "switch" {
				proof := exec.Command("go", "test", "./internal/httpapi", "-run", "^TestRepairCapabilitiesExistingClaimHTTP$", "-count=1")
				proof.Dir = "../.."
				proof.Env = append(os.Environ(), "PORTICO_REPAIR_COMPLETED_STATE="+f.state, "PORTICO_REPAIR_PUBLIC_ROOT="+base64.RawURLEncoding.EncodeToString(b.root))
				if raw, e := proof.CombinedOutput(); e != nil {
					t.Fatalf("actual capability HTTP producer: %v %s", e, raw)
				}
			}
			var primary string
			f.db.QueryRow(`SELECT profile_id FROM accounts WHERE id=?`, ownerID).Scan(&primary)
			if primary != profileID {
				t.Fatal("previous profile identity changed")
			}
			if e = f.runner.Do(ctx, func(ctx context.Context) error { _, e := f.store.InstalledCredential(ctx, f.installed); return e }); e == nil {
				t.Fatal("old installed credential replay survived replacement")
			}
			if _, e = f.id.CompletePorticoOwnerClaim(ctx, f.owner.AccessToken, revision, who); e == nil {
				t.Fatal("owner return replay accepted")
			}
		})
	}
}
