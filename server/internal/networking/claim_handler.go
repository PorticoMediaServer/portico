package networking

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"

	"github.com/PorticoMediaServer/portico/apikit/httpclientclose"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
	"strings"
	"sync"
	"time"
)

// ClaimRequestOwner is constructed from root's current local owner principal.
// Guard must recheck that exact token family/generation in the supplied tx.
// Authorize must enforce root's origin/CSRF contract before returning this value.
type ClaimRequestOwner struct {
	Owner LocalOwner
	Guard ClaimRequestGuard
	// WebOrigin is captured by the HTTP edge from the actual trusted transport,
	// never from an untrusted caller's proposed callback address.
	WebOrigin string
	// Resume rebuilds Guard after a server restart (ResumeWebClaims): an
	// opaque note of the sign-in that asked, never a credential.
	Resume string
	// OwnerStart is a captured live family capability, never client input.
	SetupStart *SetupStartAuthority
	OwnerStart *OwnerStartAuthority
	// ResolveOwnerReturn is a captured same-origin local-owner callback. It
	// obtains the actual installed HTTPS resolver fact after installation.
	ResolveOwnerReturn func(context.Context) error
	ResolveSetupReturn func(context.Context) error
	// CancelOwnerReturnTx can retire only the captured installed continuation.
	// It never authorizes cancelling the installed central claim.
	CancelOwnerReturnTx func(context.Context, *sql.Tx, string) error
}
type ClaimRequestAuthorizer func(*http.Request) (ClaimRequestOwner, error)
type ClaimExpected struct {
	OperationID     string `json:"operationId"`
	LocalGeneration int64  `json:"localGeneration,string"`
	Revision        int64  `json:"revision,string"`
}
type ClaimIdentityExpected struct {
	ServerID        string `json:"serverId"`
	LocalGeneration int64  `json:"localGeneration,string"`
}
type ClaimApprovalRequest struct {
	ClaimPurpose
	OperationID      string `json:"operationId"`
	ServerID         string `json:"serverId"`
	PublicKey        string `json:"publicKey"`
	LocalGeneration  int64  `json:"localGeneration,string"`
	Name             string `json:"name"`
	ExpectedRevision int64  `json:"expectedRevision,string"`
}
type ClaimStatus struct {
	ApprovalURL              string                `json:"approvalUrl,omitempty"`
	ClaimCode                string                `json:"claimCode,omitempty"`
	State                    string                `json:"state"`
	Identity                 ClaimIdentityExpected `json:"identity"`
	Operation                *ClaimExpected        `json:"operation,omitempty"`
	AccountID                string                `json:"accountId,omitempty"`
	InstallationAcknowledged bool                  `json:"installationAcknowledged"`
	ApprovalRequired         bool                  `json:"approvalRequired"`
	Actions                  []string              `json:"actions"`
	ApprovalRequest          *ClaimApprovalRequest `json:"approvalRequest,omitempty"`
	SetupContinuation        *SetupContinuation    `json:"setupContinuation,omitempty"`
	OwnerContinuation        *OwnerContinuation    `json:"ownerContinuation,omitempty"`
}
type ClaimHandler struct {
	recoveryWake chan struct{}
	webWatches   sync.Map
	// deliveries tracks applying an approval a setup claim was given (setup_delivery.go).
	deliveries          sync.Map
	certificates        *CertificateManager
	remote              *RemoteManager
	store               *SQLiteStore
	runner              *AuthorityRunner
	transport           Transport
	signer              Signer
	authorize           ClaimRequestAuthorizer
	managementAuthorize func(*http.Request) (ClaimRequestGuard, error)
	setupReturnResolver func(context.Context, string, string) error
	name                func() string
	endpoints           endpointRegistrations
	failures            *failureLog
}

// UseSetupReturnResolver is root composition, set before serving/recovery. Its
// opaque note is the original persisted setup capture, never request input.
func (h *ClaimHandler) UseSetupReturnResolver(resolve func(context.Context, string, string) error) {
	h.setupReturnResolver = resolve
}

type ClaimPrepareRequest struct {
	AccountID            string                `json:"accountId,omitempty"`
	Purpose              string                `json:"purpose,omitempty"`
	PreviousOwnerRole    string                `json:"previousOwnerRole,omitempty"`
	ReturnRegistrationID string                `json:"returnRegistrationId,omitempty"`
	Expected             ClaimIdentityExpected `json:"expected"`
}

func NewClaimHandler(store *SQLiteStore, runner *AuthorityRunner, transport Transport, signer Signer, authorize ClaimRequestAuthorizer, name func() string) (*ClaimHandler, error) {
	if store == nil || runner == nil || transport == nil || signer == nil || authorize == nil || name == nil {
		return nil, ErrInvalid
	}
	return &ClaimHandler{store: store, runner: runner, transport: transport, signer: signer, authorize: authorize, name: name, recoveryWake: make(chan struct{}, 1), endpoints: endpointRegistrations{pending: make(map[string]endpointPending)}, failures: sharedFailureLog}, nil
}

// HTTPRegistrar is the route binding surface shared by net/http and the API registry.
type HTTPRegistrar interface {
	Handle(string, http.Handler)
	HandleFunc(string, func(http.ResponseWriter, *http.Request))
}

func (h *ClaimHandler) Register(mux HTTPRegistrar) {
	if h == nil {
		return
	}
	mux.HandleFunc("POST /v1/networking/claim/await-approval", h.awaitWebClaim)
	h.registerCertificates(mux)
	h.registerRemote(mux)
	mux.Handle("GET /v1/networking/claim", h)
	mux.HandleFunc("POST /v1/networking/claim/endpoint", h.endpointRegister)
	mux.HandleFunc("POST /v1/networking/endpoint-proof", h.endpointProof)
	for _, action := range []string{"prepare", "approve", "continue", "cancel", "web-approval"} {
		mux.Handle("POST /v1/networking/claim/"+action, h)
	}
}
func (h *ClaimHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, e := h.authorize(r)
	if e != nil || !validOwner(actor.Owner) || actor.Guard == nil {
		writeClaimFailure(w, http.StatusUnauthorized, "owner_required", "Sign in as this server’s current owner.", false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var result ClaimStatus
	denied := false
	e = h.runner.Do(ctx, func(ctx context.Context) error {
		ctx, e = guardedClaimRequest(ctx, h.store, actor.Guard)
		if e != nil {
			return e
		}
		if e = checkClaimRequest(ctx); e != nil {
			return e
		}
		result, e = h.handle(ctx, w, r, actor, &denied)
		if e != nil {
			return e
		}
		if denied {
			// The exact denial committed under this lifecycle lease and retired
			// its initiating family. It cannot authorize a second positive read.
			return nil
		}
		return checkClaimRequest(ctx)
	})
	if e != nil {
		if r.Method == http.MethodPost {
			h.wakeClaimRecovery()
		}
		h.failure(w, r, e)
		return
	}
	// The server itself waits for the Portico Account's decision from here on, so
	// the claim installs even when the page that asked is closed.
	// A renewed start of an already installed setup has no decision left to wait for.
	if r.URL.Path == "/v1/networking/claim/web-approval" && result.Operation != nil && result.State != string(Installed) {
		if _, e = h.watchWebClaim(r.Context(), *result.Operation, actor.Guard, actor.Resume); e != nil {
			log.Printf("Waiting for the Portico Account approval could not start: %v", e)
		}
	}
	if h.certificates != nil && r.Method == http.MethodPost {
		h.certificates.Wake()
		if h.remote != nil {
			h.remote.Invalidate()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
func readClaimBody(w http.ResponseWriter, r *http.Request, out any) error {
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return ErrInvalid
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 20<<10))
	if e != nil {
		return ErrInvalid
	}
	defer clear(raw)
	// Canonical wrapper rejects duplicate fields and numeric aliases. The signed
	// envelope string remains byte-for-byte unchanged after JSON string decoding.
	return canonicalApprovalJSON(raw, out)
}
func (h *ClaimHandler) handle(ctx context.Context, w http.ResponseWriter, r *http.Request, actor ClaimRequestOwner, denied *bool) (ClaimStatus, error) {
	if r.Method == http.MethodGet && r.URL.Path == "/v1/networking/claim" {
		return h.snapshot(ctx)
	}
	if r.Method != http.MethodPost {
		return ClaimStatus{}, ErrInvalid
	}
	if r.URL.Path == "/v1/networking/claim/prepare" {
		// accountId is optional (omitempty): the canonical decoder compares the
		// request with its own re-encoding, so a body without one, which is what
		// the web console's Plex-style Connect sends, must re-encode without one.
		var body ClaimPrepareRequest
		if e := readClaimBody(w, r, &body); e != nil {
			return ClaimStatus{}, e
		}
		return h.prepareClaim(ctx, actor, body)
	}
	var body ClaimActionRequest
	if e := readClaimBody(w, r, &body); e != nil {
		return ClaimStatus{}, e
	}
	if !validID(body.Expected.OperationID) || body.Expected.LocalGeneration < 0 || body.Expected.Revision < 1 {
		return ClaimStatus{}, ErrInvalid
	}
	expected, e := h.expected(ctx, body.Expected)
	if e != nil {
		return ClaimStatus{}, stepFailure("expected operation", e)
	}
	if r.URL.Path == "/v1/networking/claim/web-approval" {
		address := returnOrigin(r)
		if expected.Purpose == "switch" {
			if actor.WebOrigin == "" {
				return ClaimStatus{}, ErrClaimOwnerRequired
			}
			address = actor.WebOrigin
		}
		var pending localWebClaim
		if body.SetupStart != nil {
			if expected.Purpose != "setup" || actor.SetupStart == nil || actor.WebOrigin == "" || body.OwnerStart != nil || body.ApprovalEnvelope != "" || len(r.Header.Values("X-Portico-Installation-Id")) != 1 || body.SetupStart.InstallationID != r.Header.Get("X-Portico-Installation-Id") {
				return ClaimStatus{}, ErrInvalid
			}
			pending, e = h.setupWebClaim(ctx, r, actor, expected, *body.SetupStart)
		} else if expected.Purpose == "switch" && expected.CurrentOwnerAccountID == "" {
			if body.OwnerStart == nil || actor.OwnerStart == nil || actor.WebOrigin == "" || body.ApprovalEnvelope != "" || len(r.Header.Values("X-Portico-Installation-Id")) != 1 || body.OwnerStart.InstallationID != r.Header.Get("X-Portico-Installation-Id") {
				return ClaimStatus{}, ErrInvalid
			}
			pending, e = h.beginOwnerWebClaim(ctx, expected, address, body.OwnerStart.State, body.OwnerStart.CodeChallenge, body.OwnerStart.InstallationID, actor.OwnerStart)
		} else {
			if body.OwnerStart != nil || body.SetupStart != nil {
				return ClaimStatus{}, ErrInvalid
			}
			pending, e = h.beginWebClaim(ctx, expected, address)
		}
		if e != nil {
			return ClaimStatus{}, e
		}
		return h.webClaimStatus(ctx, pending)
	}
	if body.OwnerStart != nil || body.SetupStart != nil {
		return ClaimStatus{}, ErrInvalid
	}
	if r.URL.Path == "/v1/networking/claim/approve" {
		if len(body.ApprovalEnvelope) < 1 || len(body.ApprovalEnvelope) > 16<<10 {
			return ClaimStatus{}, ErrInvalid
		}
		if _, e = h.store.Approve(ctx, expected, []byte(body.ApprovalEnvelope)); e != nil {
			if errors.Is(e, ErrOwnerCollision) {
				_, _, _ = h.store.WithdrawReplacement(ctx, expected)
			}
			return ClaimStatus{}, e
		}
		return h.snapshot(ctx)
	}
	if body.ApprovalEnvelope != "" {
		return ClaimStatus{}, ErrInvalid
	}
	if r.URL.Path != "/v1/networking/claim/continue" && r.URL.Path != "/v1/networking/claim/cancel" {
		return ClaimStatus{}, ErrInvalid
	}
	if r.URL.Path == "/v1/networking/claim/cancel" && expected.Stage == Installed {
		ended, err := h.cancelOwnerReturn(ctx, expected, actor)
		if err != nil {
			return ClaimStatus{}, err
		}
		if ended {
			*denied = true
			return h.snapshotAfterOwnerDenial(ctx)
		}
	}
	coordinator, e := NewCoordinator(expected.OperationID, h.store.audience, claimExpectedStore{Store: h.store, expected: expected}, h.transport, h.signer)
	if e != nil {
		return ClaimStatus{}, e
	}
	if r.URL.Path == "/v1/networking/claim/cancel" {
		if expected.Purpose == "switch" && expected.CurrentOwnerAccountID == "" && (expected.Stage == Finalizing || expected.Stage == Retrieving) {
			// FINALIZE may already have committed remotely. Use only the
			// original-key proof armed before it, never a fresh cancellation.
			coordinator, e = NewCoordinator(expected.OperationID, h.store.audience, h.store, h.transport, h.signer)
			if e == nil {
				_, e = coordinator.ReconcilePreinstallCancellation(ctx)
			}
		} else {
			_, e = coordinator.Cancel(ctx)
		}
	} else {
		var next Intent
		next, e = coordinator.Step(ctx)
		if e == nil && next.Purpose == "setup" && next.Stage == Installed && next.InstallationAcknowledged && actor.ResolveSetupReturn != nil {
			var captured bool
			if e = h.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_setup_completions WHERE operation_id=? AND phase IN('prepared','installed'))`, next.OperationID).Scan(&captured); e == nil && captured {
				e = actor.ResolveSetupReturn(ctx)
				if errors.Is(e, ErrUnavailable) {
					// Not ready yet is not a refusal, but it must never be silent.
					h.failures.report(routeLabel(r, "networking claim route"), 200, ctx, e)
					e = nil
				}
			}
		}
		if e == nil && next.Stage == Installed && next.InstallationAcknowledged && actor.ResolveOwnerReturn != nil {
			var captured bool
			if e = h.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_owner_completions WHERE operation_id=? AND phase='installed')`, next.OperationID).Scan(&captured); e == nil && captured {
				e = actor.ResolveOwnerReturn(ctx)
				// TLS/route provisioning can exceed this bounded attempt. The
				// installed progress remains true; no deadline is extended.
				if errors.Is(e, ErrUnavailable) {
					e = nil
				}
			}
		}
	}
	if e != nil {
		return ClaimStatus{}, e
	}
	if r.URL.Path == "/v1/networking/claim/cancel" {
		h.deliveries.Delete(expected.OperationID) // cancelled: nothing more to deliver
		// A never-installed captured switch is withdrawn locally or fenced by
		// its original-key outbox. Only that completed denial skips the now
		// retired family's final positive check.
		var ended bool
		if e = h.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_owner_completions c JOIN networking_claim_intents i ON i.operation_id=c.operation_id WHERE c.operation_id=? AND c.phase='cancelled' AND i.stage='cancelled' AND i.server_id=? AND i.local_generation=?)`, expected.OperationID, expected.ServerID, expected.LocalGeneration).Scan(&ended); e != nil {
			return ClaimStatus{}, e
		}
		*denied = ended
	}
	if *denied {
		return h.snapshotAfterOwnerDenial(ctx)
	}
	return h.snapshot(ctx)
}

// snapshotAfterOwnerDenial is reachable only after the exact captured denial
// committed and retired the initiating family. It exposes bounded progress,
// never new actions or signed proof. The process lifecycle remains required.
func (h *ClaimHandler) snapshotAfterOwnerDenial(ctx context.Context) (ClaimStatus, error) {
	status, err := h.snapshot(context.WithValue(ctx, claimRequestKey{}, nil))
	status.Actions = []string{}
	return status, err
}
func (h *ClaimHandler) expected(ctx context.Context, want ClaimExpected) (Intent, error) {
	gated2, e := h.store.tx(ctx)
	if e != nil {
		return Intent{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return Intent{}, e
	}
	if (!id.active.Valid || id.active.String != want.OperationID) && (!id.pending.Valid || id.pending.String != want.OperationID) {
		return Intent{}, ErrStale
	}
	v, e := loadIntentTx(ctx, tx, want.OperationID)
	if e != nil {
		return Intent{}, e
	}
	if v.LocalGeneration != want.LocalGeneration || v.Revision != want.Revision {
		return Intent{}, ErrStale
	}
	return v.Intent, nil
}
func (h *ClaimHandler) snapshot(ctx context.Context) (ClaimStatus, error) {
	// Name is display metadata and may read the same single-connection database.
	// Resolve it before borrowing the transactional claim connection.
	name := h.name()
	gated3, e := h.store.tx(ctx)
	if e != nil {
		return ClaimStatus{}, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	id, active, _, e := readCurrentIdentityTx(ctx, tx)
	if e != nil {
		return ClaimStatus{}, e
	}
	result := ClaimStatus{State: "unconfigured", Identity: ClaimIdentityExpected{id.ServerID, id.ResetGeneration}, Actions: []string{"prepare"}}
	var pending sql.NullString
	if e = tx.QueryRowContext(ctx, `SELECT pending_operation_id FROM networking_claim_identity WHERE singleton=1`).Scan(&pending); e != nil {
		return ClaimStatus{}, e
	}
	if pending.Valid {
		active = pending
	}
	// The first Local-to-Portico switch has only a pending pointer. It must be
	// visible without inventing an active or installed central claim.
	if !active.Valid {
		return result, nil
	}
	stored, e := loadIntentTx(ctx, tx, active.String)
	if e != nil {
		return ClaimStatus{}, e
	}
	v := stored.Intent
	if pending.Valid && (v.ServerID != id.ServerID || v.LocalGeneration != id.ResetGeneration || !bytes.Equal(v.PublicKey, id.PublicKey)) {
		return ClaimStatus{}, ErrStale
	}
	result.State = string(v.Stage)
	result.Operation = &ClaimExpected{v.OperationID, v.LocalGeneration, v.Revision}
	result.AccountID = v.AccountID
	result.InstallationAcknowledged = v.InstallationAcknowledged
	result.Actions = []string{}
	// A first-run setup's progress is read from GET /v1/setup/state; only the approval answer carries its signed pair.
	var continuation OwnerContinuation
	var expires int64
	var registration, origin string
	ce := tx.QueryRowContext(ctx, `SELECT transaction_id,phase,expires_ms,registration_id,registration_origin FROM identity_owner_completions WHERE operation_id=?`, v.OperationID).Scan(&continuation.TransactionID, &continuation.Phase, &expires, &registration, &origin)
	if ce != nil && !errors.Is(ce, sql.ErrNoRows) {
		return ClaimStatus{}, ce
	}
	if ce == nil {
		continuation.ExpiresAt = time.UnixMilli(expires).UTC().Format(time.RFC3339Nano)
		if registration != "" && origin != "" {
			continuation.ReturnRegistration = &serverreturn.Registration{ID: registration, Origin: origin}
		}
		result.OwnerContinuation = &continuation
		if continuation.Phase != "prepared" && continuation.Phase != "installed" {
			return result, nil
		}
	}
	if v.Stage == CancelPending {
		result.Actions = []string{"continue"}
		return result, nil
	}
	result.Actions = append(result.Actions, "cancel")
	if v.Stage == Installed {
		var state string
		e = tx.QueryRowContext(ctx, `SELECT state FROM networking_claim_authority WHERE operation_id=?`, v.OperationID).Scan(&state)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return ClaimStatus{}, e
		}
		if terminalClaimState(state) {
			result.State = "disconnected"
			return result, nil
		}
		if result.OwnerContinuation != nil && result.OwnerContinuation.Phase == "installed" {
			result.Actions = []string{"continue", "cancel"}
			return result, nil
		}
		if !v.InstallationAcknowledged {
			result.Actions = append(result.Actions, "continue")
		}
		return result, nil
	}
	result.ApprovalRequired = v.Stage == Prepared || !h.store.now().Before(v.ApprovalExpiresAt)
	if result.ApprovalRequired {
		if name == "" || len(name) > 120 {
			return ClaimStatus{}, ErrInvalid
		}
		result.Actions = append(result.Actions, "approve")
		result.ApprovalRequest = &ClaimApprovalRequest{v.ClaimPurpose, v.OperationID, v.ServerID, base64.RawURLEncoding.EncodeToString(v.PublicKey), v.LocalGeneration, name, v.ApprovalRevision}
	} else {
		result.Actions = append(result.Actions, "continue")
	}
	return result, nil
}

func (s claimExpectedStore) WithdrawReplacement(ctx context.Context, v Intent) (Intent, bool, error) {
	if store, ok := s.Store.(replacementWithdrawer); ok {
		return store.WithdrawReplacement(ctx, v)
	}
	return Intent{}, false, nil
}

type claimExpectedStore struct {
	Store
	expected Intent
}

func (s claimExpectedStore) Load(ctx context.Context, id string) (Intent, error) {
	v, e := s.Store.Load(ctx, id)
	if e != nil {
		return Intent{}, e
	}
	if !sameBinding(v.Binding, s.expected.Binding) || v.Revision != s.expected.Revision || v.Stage != s.expected.Stage || v.ApprovalRevision != s.expected.ApprovalRevision || v.ClaimGeneration != s.expected.ClaimGeneration || v.CredentialGeneration != s.expected.CredentialGeneration {
		return Intent{}, ErrStale
	}
	return v, nil
}
func writeClaimFailure(w http.ResponseWriter, status int, code, message string, retry bool) {
	if httpclientclose.Writer(w) {
		w.WriteHeader(httpclientclose.Status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retry}})
}
func (h *ClaimHandler) failure(w http.ResponseWriter, r *http.Request, e error) {
	var request context.Context
	if r != nil {
		request = r.Context()
	}
	// A refusal is answered plainly, but the server log names its step and class (never a
	// value), so a refused setup or claim explains itself without a reproduction.
	if errors.Is(e, ErrStale) || errors.Is(e, ErrApprovalRequired) || errors.Is(e, ErrCancelled) {
		h.failures.report(routeLabel(r, "networking claim route"), 409, request, e)
	}
	switch {
	case errors.Is(e, ErrClaimOwnerRequired):
		writeClaimFailure(w, 401, "owner_required", "Sign in as this server’s current owner.", false)
	case errors.Is(e, ErrInvalid):
		writeClaimFailure(w, 400, "invalid_claim_request", "Check the claim request and try again.", false)
	case errors.Is(e, ErrOwnerCollision):
		writeClaimFailure(w, 409, "owner_target_exists", "This account is already represented on this server.", false)
	case errors.Is(e, ErrClaimMoveRequired):
		writeClaimFailure(w, 409, "claim_move_required", "In Settings, change sign-in from Local to Portico Account and confirm the move before connecting this server.", false)
	case errors.Is(e, ErrStale):
		writeClaimFailure(w, 409, "claim_stale", "Server setup changed. Refresh its status before continuing.", false)
	case errors.Is(e, ErrApprovalRequired):
		writeClaimFailure(w, 409, "approval_required", "Approve this server with your Portico Account to continue.", false)
	case errors.Is(e, ErrCancelled):
		writeClaimFailure(w, 409, "claim_cancelled", "This server connection was cancelled or revoked.", false)
	default:
		h.failures.report(routeLabel(r, "networking claim route"), 503, request, e)
		writeClaimFailure(w, 503, "claim_unavailable", "Server setup could not finish. Refresh its status and retry when the connection is available.", true)
	}
}

// SetManagementAuthorizer wires ordinary server-owner administration without
// changing the separate local-owner bootstrap/claim authority. Call before Register.
func (h *ClaimHandler) SetManagementAuthorizer(authorize func(*http.Request) (ClaimRequestGuard, error)) {
	h.managementAuthorize = authorize
}
func (h *ClaimHandler) authorizeManagement(r *http.Request) (ClaimRequestGuard, error) {
	if h.managementAuthorize != nil {
		return h.managementAuthorize(r)
	}
	actor, err := h.authorize(r)
	if err != nil {
		return nil, err
	}
	if !validOwner(actor.Owner) || actor.Guard == nil {
		return nil, ErrClaimOwnerRequired
	}
	return actor.Guard, nil
}

// returnOrigin is where the web approval page sends the person back: the
// origin their browser used for this request (A60). A same-origin Origin header
// is exact, including the scheme behind a TLS-terminating proxy; otherwise the
// request's own scheme and host.
func returnOrigin(r *http.Request) string {
	if origin := r.Header.Get("Origin"); origin != "" {
		if u, e := url.Parse(origin); e == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host) && u.Path == "" && u.User == nil {
			return u.Scheme + "://" + u.Host
		}
	}
	if r.TLS != nil {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

// SetRequestAuthorizer wires the router's single connection policy before serving.
func (h *ClaimHandler) SetRequestAuthorizer(authorize ClaimRequestAuthorizer) {
	if authorize != nil {
		h.authorize = authorize
	}
}

func (ClaimPrepareRequest) JSONSchema() map[string]any {
	id := map[string]any{"type": "string", "maxLength": 128, "pattern": "^[A-Za-z0-9_-]*$"}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"expected"}, "properties": map[string]any{
		"accountId": id, "purpose": map[string]any{"type": "string", "enum": []string{"setup", "switch", "change-owner"}},
		"previousOwnerRole": map[string]any{"type": "string", "enum": []string{"admin", "member", "remove"}}, "returnRegistrationId": id,
		"expected": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"serverId", "localGeneration"}, "properties": map[string]any{"serverId": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"}, "localGeneration": map[string]any{"type": "string", "pattern": "^(0|[1-9][0-9]*)$"}}}}}
}

// setupWebClaim signs and registers a setup claim's start with Portico Services for the
// setup's own caller. A renewed start of an installed setup binds its HTTPS return now,
// as the continue step would have if the original start were still live.
func (h *ClaimHandler) setupWebClaim(ctx context.Context, r *http.Request, actor ClaimRequestOwner, expected Intent, start ClaimSetupStartRequest) (localWebClaim, error) {
	pending, e := h.beginSetupWebClaim(ctx, expected, actor.WebOrigin, start.State, start.CodeChallenge, start.InstallationID, actor.SetupStart)
	if e == nil && expected.Stage == Installed && expected.InstallationAcknowledged && actor.ResolveSetupReturn != nil {
		if err := actor.ResolveSetupReturn(ctx); err != nil && !errors.Is(err, ErrUnavailable) {
			e = stepFailure("setup return registration", err)
		} else if err != nil {
			// Not ready yet is not a refusal, but it must never be silent.
			h.failures.report(routeLabel(r, "networking claim route"), 200, ctx, err)
		}
	}
	return pending, e
}

// webClaimStatus is the claim as it now is, with the approval link and any signed
// continuation the caller binds before it opens that link.
func (h *ClaimHandler) webClaimStatus(ctx context.Context, pending localWebClaim) (ClaimStatus, error) {
	status, e := h.snapshot(ctx)
	status.ApprovalURL = pending.URL
	status.ClaimCode = pending.Code
	if pending.SetupStart != nil {
		status.SetupContinuation = &SetupContinuation{TransactionID: pending.SetupTransactionID, Phase: "prepared", ExpiresAt: pending.ExpiresAt.UTC().Format(time.RFC3339Nano), Claim: &pending.Signed, Start: pending.SetupStart}
	}
	if pending.OwnerStart != nil {
		status.OwnerContinuation = &OwnerContinuation{TransactionID: pending.OwnerTransactionID, Phase: "prepared", ExpiresAt: pending.ExpiresAt.UTC().Format(time.RFC3339Nano), Claim: &pending.Signed, Start: pending.OwnerStart}
	}
	return status, e
}

// prepareClaim prepares a claim operation for the caller: unbound to any account, so
// whoever approves it on Portico Services becomes the owner (Plex-style).
func (h *ClaimHandler) prepareClaim(ctx context.Context, actor ClaimRequestOwner, body ClaimPrepareRequest) (ClaimStatus, error) {
	owner := actor.Owner
	if body.Purpose == "switch" && actor.WebOrigin == "" {
		return ClaimStatus{}, ErrClaimOwnerRequired
	}
	// Web approval (Plex-style): no account is needed to prepare. The person
	// who approves the code on web.getportico.tv becomes the owner.
	if body.AccountID == "" {
		body.AccountID = UnboundAccount
	}
	if !validID(body.AccountID) || !validID(body.Expected.ServerID) || body.Expected.LocalGeneration < 0 {
		return ClaimStatus{}, ErrInvalid
	}
	gated, e := h.store.tx(ctx)
	if e != nil {
		return ClaimStatus{}, e
	}
	tx := gated.Tx()
	identity, active, installed, e := readCurrentIdentityTx(ctx, tx)
	gated.Rollback()
	if e != nil {
		return ClaimStatus{}, e
	}
	if (active.Valid || installed.Valid) && body.Purpose != "change-owner" && body.Purpose != "switch" || identity.ServerID != body.Expected.ServerID || identity.ResetGeneration != body.Expected.LocalGeneration {
		return ClaimStatus{}, ErrStale
	}
	nonce := make([]byte, 24)
	if _, e = rand.Read(nonce); e != nil {
		return ClaimStatus{}, e
	}
	binding := Binding{OperationID: base64.RawURLEncoding.EncodeToString(nonce), ServerID: identity.ServerID, AccountID: body.AccountID, PublicKey: identity.PublicKey, LocalGeneration: identity.ResetGeneration}
	purpose := ClaimPurpose{Purpose: body.Purpose, PreviousOwnerRole: body.PreviousOwnerRole, ReturnRegistrationID: body.ReturnRegistrationID}
	if purpose.Purpose == "" {
		purpose.Purpose = "setup"
	}
	if purpose.Purpose != "setup" {
		if purpose.Purpose == "change-owner" && purpose.PreviousOwnerRole == "" {
			purpose.PreviousOwnerRole = "admin"
		}
		if e = h.store.db.QueryRowContext(ctx, `SELECT hosted_account_id FROM account_portico_links WHERE account_id=?`, owner.AccountID).Scan(&purpose.CurrentOwnerAccountID); e != nil && !(purpose.Purpose == "switch" && errors.Is(e, sql.ErrNoRows)) {
			return ClaimStatus{}, ErrClaimOwnerRequired
		}
	}
	if _, e = h.store.PreparePurpose(ctx, binding, owner, purpose); e != nil {
		return ClaimStatus{}, e
	}
	return h.snapshot(ctx)
}
