package networking

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// SetupApprovalRequest is POST /v1/setup/approval: the caller's own setup start (its
// PKCE challenge, random state and installation), as the claim web-approval takes it.
type SetupApprovalRequest struct {
	SetupStart ClaimSetupStartRequest `json:"setupStart"`
}

// ServeSetupApproval is the server's half of "Continue to Portico Account"
// (setupflow S6, S7) in one call: with no claim active it prepares one (unbound, so
// whoever approves it becomes the owner), then signs this caller's setup start and
// registers it with Portico Services. A live start replays its stored signed pair; an
// installed setup whose admission lapsed renews its start without a second approval.
// It answers the claim with its approval link and signed continuation, under the same
// authority, guard and setup-start capture as the claim routes, and the server then
// waits for the decision itself. The caller's phase is checked before this is called.
func (h *ClaimHandler) ServeSetupApproval(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, e := h.authorize(r)
	if e != nil || actor.Guard == nil || actor.SetupStart == nil || actor.WebOrigin == "" {
		writeClaimFailure(w, http.StatusUnauthorized, "owner_required", "Start setup again on this server.", false)
		return
	}
	var body SetupApprovalRequest
	if e = readClaimBody(w, r, &body); e != nil {
		h.failure(w, r, e)
		return
	}
	if len(r.Header.Values("X-Portico-Installation-Id")) != 1 || body.SetupStart.InstallationID != r.Header.Get("X-Portico-Installation-Id") {
		h.failure(w, r, ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var result ClaimStatus
	e = h.runner.Do(ctx, func(ctx context.Context) error {
		ctx, e := guardedClaimRequest(ctx, h.store, actor.Guard)
		if e != nil {
			return e
		}
		if e = checkClaimRequest(ctx); e != nil {
			return e
		}
		status, e := h.snapshot(ctx)
		if e != nil {
			return e
		}
		if status.Operation == nil {
			if status, e = h.prepareClaim(ctx, actor, ClaimPrepareRequest{Purpose: "setup", Expected: status.Identity}); e != nil {
				return e
			}
			if status.Operation == nil {
				return ErrStale
			}
		}
		expected, e := h.expected(ctx, *status.Operation)
		if e != nil {
			return stepFailure("expected operation", e)
		}
		if expected.Purpose != "setup" {
			return ErrInvalid
		}
		pending, e := h.setupWebClaim(ctx, r, actor, expected, body.SetupStart)
		if e != nil {
			return e
		}
		if result, e = h.webClaimStatus(ctx, pending); e != nil {
			return e
		}
		return checkClaimRequest(ctx)
	})
	if e != nil {
		h.wakeClaimRecovery()
		h.failure(w, r, e)
		return
	}
	if result.Operation != nil && result.State != string(Installed) {
		if _, e = h.watchWebClaim(r.Context(), *result.Operation, actor.Guard, actor.Resume); e != nil {
			log.Printf("Waiting for the Portico Account approval could not start: %v", e)
		}
	}
	if h.certificates != nil {
		h.certificates.Wake()
		if h.remote != nil {
			h.remote.Invalidate()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
