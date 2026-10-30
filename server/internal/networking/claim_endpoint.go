package networking

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type endpointChallenge struct {
	Kind                 string    `json:"kind"`
	ServerID             string    `json:"serverId"`
	AccountID            string    `json:"accountId"`
	OperationID          string    `json:"operationId"`
	ClaimGeneration      string    `json:"claimGeneration"`
	CredentialGeneration string    `json:"credentialGeneration"`
	BaseURL              string    `json:"baseUrl"`
	RegistrationID       string    `json:"registrationId"`
	Nonce                string    `json:"nonce"`
	ExpiresAt            time.Time `json:"expiresAt"`
}
type endpointPending struct {
	intent Intent
	origin string
	ctx    context.Context
}
type endpointRegistrations struct {
	sync.Mutex
	pending map[string]endpointPending
}
type EndpointReceipt struct {
	BaseURL    string    `json:"baseUrl"`
	VerifiedAt time.Time `json:"verifiedAt"`
}
type endpointCaller interface {
	CallServer(context.Context, Intent, ServerOperation, any, any) error
}

// endpointOrigin is preliminary local validation. Hosted independently enforces
// public DNS/IP-pinned HTTPS: this value can never select the control-plane host.
func validEndpointPort(u *url.URL) bool {
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if u.Port() == "" {
		return true
	}
	n, e := strconv.Atoi(u.Port())
	return e == nil && n > 0 && n <= 65535 && strconv.Itoa(n) == u.Port()
}
func endpointOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 300 && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == "" && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && u.Opaque == "" && validEndpointPort(u) && strings.ToLower(raw) == raw
}
func (h *ClaimHandler) endpointRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, err := h.authorize(r)
	if err != nil || !validOwner(actor.Owner) || actor.Guard == nil {
		h.failure(w, r, ErrClaimOwnerRequired)
		return
	}
	var q struct {
		Expected ClaimExpected `json:"expected"`
		BaseURL  string        `json:"baseUrl"`
	}
	if err = readClaimBody(w, r, &q); err != nil || !endpointOrigin(q.BaseURL) {
		h.failure(w, r, ErrInvalid)
		return
	}
	caller, ok := h.transport.(endpointCaller)
	if !ok {
		h.failure(w, r, stepFailure("endpoint transport", ErrUnavailable))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var receipt EndpointReceipt
	err = h.runner.Do(ctx, func(ctx context.Context) error {
		ctx, err = guardedClaimRequest(ctx, h.store, actor.Guard)
		if err != nil {
			return err
		}
		v, e := h.expected(ctx, q.Expected)
		if e != nil {
			return e
		}
		current, e := h.store.InstalledIntent(ctx)
		if e != nil {
			return e
		}
		if current.OperationID != v.OperationID || current.Revision != v.Revision || !v.InstallationAcknowledged {
			return ErrStale
		}
		random := make([]byte, 32)
		if _, e = rand.Read(random); e != nil {
			return e
		}
		id := base64.RawURLEncoding.EncodeToString(random)
		h.endpoints.Lock()
		if len(h.endpoints.pending) >= 1 {
			h.endpoints.Unlock()
			return ErrUnavailable
		}
		h.endpoints.pending[id] = endpointPending{v, q.BaseURL, ctx}
		h.endpoints.Unlock()
		defer func() { h.endpoints.Lock(); delete(h.endpoints.pending, id); h.endpoints.Unlock() }()
		request := struct {
			BaseURL        string `json:"baseUrl"`
			RegistrationID string `json:"registrationId"`
		}{q.BaseURL, id}
		if e = caller.CallServer(ctx, v, RegisterEndpoint, request, &receipt); e != nil {
			return e
		}
		if receipt.BaseURL != q.BaseURL || receipt.VerifiedAt.IsZero() {
			return ErrInvalid
		}
		return checkClaimRequest(ctx)
	})
	if err != nil {
		h.failure(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(receipt)
}
func (h *ClaimHandler) endpointProof(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var q endpointChallenge
	if err := readClaimBody(w, r, &q); err != nil {
		h.failure(w, r, err)
		return
	}
	h.endpoints.Lock()
	pending, ok := h.endpoints.pending[q.RegistrationID]
	h.endpoints.Unlock()
	now := time.Now()
	if !ok || pending.ctx.Err() != nil || q.Kind != "portico.server.endpoint.v1" || q.BaseURL != pending.origin || q.ServerID != pending.intent.ServerID || q.AccountID != pending.intent.AccountID || q.OperationID != pending.intent.OperationID || q.ClaimGeneration != pending.intent.ClaimGeneration || q.CredentialGeneration != pending.intent.CredentialGeneration || !q.ExpiresAt.After(now) || q.ExpiresAt.After(now.Add(35*time.Second)) || !validID(q.Nonce) {
		h.failure(w, r, ErrStale)
		return
	}
	// Use the original owner request context, retaining its exact family guard and
	// restore lease. Cancellation of either HTTP exchange cancels this signer.
	ctx, cancel := context.WithCancel(pending.ctx)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	err := h.runner.Do(ctx, func(ctx context.Context) error {
		// A separately counted lease keeps this HTTP callback in the drain set
		// even if the initiating registration times out while proof is in flight.
		raw, err := json.Marshal(q)
		if err != nil {
			return err
		}
		if err = h.store.Current(ctx, pending.intent); err != nil {
			return err
		}
		signature, err := h.signer.Sign(ctx, pending.intent.Binding, raw)
		if err != nil {
			return err
		}
		if err = h.store.Current(ctx, pending.intent); err != nil {
			return err
		}
		out := struct {
			Payload   string `json:"payload"`
			Signature string `json:"signature"`
		}{base64.RawURLEncoding.EncodeToString(raw), base64.RawURLEncoding.EncodeToString(signature)}
		encoded, _ := json.Marshal(out)
		return commitClaimTx(ctx, func() error {
			if e := checkClaimRequest(ctx); e != nil {
				return e
			}
			w.Header().Set("Content-Type", "application/json")
			_, e := w.Write(encoded)
			return e
		})
	})
	if err != nil {
		h.failure(w, r, err)
	}

}

// validateEndpointSigningPayload prevents an endpoint-specific signer allowance
// from being used for a different account, operation or unrelated byte payload.
func validateEndpointSigningPayload(raw []byte, b Binding) bool {
	var q endpointChallenge
	if canonicalApprovalJSON(raw, &q) != nil {
		return false
	}
	canonical, _ := json.Marshal(q)
	return bytes.Equal(canonical, raw) && q.Kind == "portico.server.endpoint.v1" && q.ServerID == b.ServerID && q.AccountID == b.AccountID && q.OperationID == b.OperationID && endpointOrigin(q.BaseURL) && q.ExpiresAt.After(time.Now()) && q.ExpiresAt.Before(time.Now().Add(35*time.Second)) && validID(q.Nonce) && validID(q.RegistrationID)
}
