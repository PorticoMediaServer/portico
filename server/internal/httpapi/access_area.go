package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/connectivity"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
)

// AccessArea carries the people, diagnostics and connectivity seams. It is one
// field on Dependencies so the area adds one line to the shared struct.
type AccessArea struct {
	Access     *access.Store
	Logs       *servicelog.Recorder
	ClientLogs *servicelog.ClientLogStore
	Advertiser *connectivity.Advertiser
	// DLNA starts and stops the SSDP advertisement with the owner's switch;
	// the /dlna/ routes read the switch themselves on every request.
	DLNA *connectivity.Advertiser
}

// administrationRoutes registers every route in the people, diagnostics and
// connectivity area.
func (d Dependencies) accessAreaRoutes(mux *http.ServeMux) {
	d.accessRoutes(mux)
	d.diagnosticsRoutes(mux)
	d.recordingRootRoutes(mux)
}

// tierAuthorityTx is the administration counterpart of ownerAuthorityTx. It
// asks identity.Grants, the single tier predicate, of both the session's own
// tier and the account's live membership row: an account demoted a moment ago
// cannot use a token minted while it was an administrator, and a session
// issued narrower than its account never borrows the account's tier.
func (d Dependencies) tierAuthorityTx(ctx context.Context, tx *sql.Tx, p identity.Principal, needed string) (identity.Principal, error) {
	if d.Identity == nil || tx == nil {
		return p, identity.ErrUnauthorized
	}
	if _, err := d.Identity.SessionFamilyTx(ctx, tx, p); err != nil {
		return p, err
	}
	if err := d.Identity.RequireMainProfileTx(ctx, tx, p); err != nil {
		return p, err
	}
	switch p.Authority {
	case "local":
		role, err := access.Role(tx, p.AccountID)
		if err != nil {
			return p, err
		}
		// Both must hold the tier: the account, now, and the session itself.
		// A session can be narrower than its account on purpose (a second
		// profile of the owner's account, a viewing-only device, a Cast
		// target are all members), and the account's tier must not be lent to
		// it. A session can also be stale the other way, which the live
		// membership row catches.
		if !identity.Grants(p.Role, needed) {
			return p, identity.ErrForbidden
		}
		p.Role = role
	default:
		return p, identity.ErrUnauthorized
	}
	if !identity.Grants(p.Role, needed) {
		return p, identity.ErrForbidden
	}
	return p, nil
}

// administrator authenticates a request and requires at least the admin tier.
// Owner-only routes keep using d.owner, which requires the owner tier exactly.
func (d Dependencies) administrator(r *http.Request) (identity.Principal, error) {
	p, err := d.bearerPrincipal(r)
	if err != nil {
		return p, err
	}
	gated, err := dbwork.BeginSnapshot(r.Context(), d.DB)
	if err != nil {
		return p, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	return d.tierAuthorityTx(r.Context(), tx, p, identity.TierAdmin)
}

// bearerPrincipal resolves the caller's session. An API key or personal access
// token arrives here like any session, already held to its scopes by the
// credential gate, and gets the same recovery and live-session checks.
func (d Dependencies) bearerPrincipal(r *http.Request) (identity.Principal, error) {
	headers := r.Header.Values("Authorization")
	if d.Identity == nil || d.DB == nil || len(headers) != 1 || !strings.HasPrefix(headers[0], "Bearer ") {
		return identity.Principal{}, identity.ErrUnauthorized
	}
	secret := strings.TrimPrefix(headers[0], "Bearer ")
	p, err := d.Identity.AuthenticateContext(r.Context(), secret)
	if err != nil {
		return p, err
	}
	d.admission.rememberCredential(secret, p)
	return p, err
}

// accessAuthority adapts a checked principal into the store's in-transaction
// authority, so the store re-checks the tier inside the same transaction that
// performs the write.
func (d Dependencies) accessAuthority(p identity.Principal, needed string) access.Authorize {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := d.tierAuthorityTx(ctx, tx, p, needed)
		return err
	}
}

// accessFailure maps this area's errors onto the wire. It mirrors consoleError's
// shape, including the currentRevision and fields detail a client needs in order
// to recover from a conflict or a rejected value without a second read.
func accessFailure(w http.ResponseWriter, e error) {
	if writerRefused(w, e) {
		return
	}
	if revisionFailure(w, e) {
		return
	}
	if errors.Is(e, identity.ErrSignInKindLocal) || errors.Is(e, identity.ErrSignInKindPortico) || errors.Is(e, identity.ErrFactorSetupRequired) || errors.Is(e, identity.ErrAvatarMissing) || errors.Is(e, identity.ErrProfilePINReentry) || errors.Is(e, identity.ErrProfilePINManage) {
		failure(w, e)
		return
	}
	status, code, message := 500, "administration_error", "The request could not be completed."
	switch {
	// CD-51: a refused but valid session is 403 (or a hidden 404), never 401.
	case errors.Is(e, identity.ErrNotVisible):
		status, code, message = 404, "not_found", publicErrorMessage("not_found")
	case errors.Is(e, identity.ErrForbidden):
		status, code, message = 403, "forbidden", identity.ErrForbidden.Error()
	case errors.Is(e, identity.ErrUnauthorized):
		status, code, message = 401, "unauthorized", "Authentication is required."
	case errors.Is(e, identity.ErrContentRestricted), errors.Is(e, access.ErrChannelDenied):
		status, code, message = 404, "not_found", publicErrorMessage("not_found")
	case errors.Is(e, errFeatureRestricted):
		status, code, message = 403, "feature_restricted", publicErrorMessage("feature_restricted")
	case requestinput.Is(e), errors.Is(e, identity.ErrDirectInput), errors.Is(e, identity.ErrRestrictionInput), errors.Is(e, access.ErrInvalid), errors.Is(e, servicelog.ErrClientUpload):
		status, code, message = 400, "invalid_request", "Check the submitted fields and their limits."
	case errors.Is(e, access.ErrIdempotencyKeyReused):
		status, code, message = 409, "idempotency_key_reused", "This request conflicts with an earlier request."
	case errors.Is(e, identity.ErrProfileChanged), errors.Is(e, identity.ErrRestrictionChanged):
		status, code, message = 409, "revision_mismatch", publicErrorMessage("administration_conflict")
	case errors.Is(e, access.ErrConflict):
		status, code, message = 409, "administration_conflict", publicErrorMessage("administration_conflict")
	case errors.Is(e, access.ErrExpired):
		status, code, message = 410, "invitation_expired", publicErrorMessage("invitation_expired")
	case errors.Is(e, access.ErrInvitationRecipient):
		status, code, message = 403, "invitation_for_another_account", publicErrorMessage("invitation_for_another_account")
	case errors.Is(e, access.ErrCapacity):
		status, code, message = 429, "administration_capacity", publicErrorMessage("administration_capacity")
	case errors.Is(e, servicelog.ErrClientLogBudget):
		status, code, message = 429, "client_log_budget", "The daily client log limit has been reached."
	case errors.Is(e, servicelog.ErrClientUploadDevice):
		status, code, message = 403, "device_session_required", "Sign in on this device to upload logs."
	case errors.Is(e, access.ErrSchedule):
		status, code, message = 403, "outside_allowed_hours", publicErrorMessage("outside_allowed_hours")
	case errors.Is(e, access.ErrStreamLimit):
		status, code, message = 403, "access_limit", publicErrorMessage("access_limit")
	case errors.Is(e, ErrRemoteSignIn):
		status, code, message = 403, "remote_sign_in_not_allowed", publicErrorMessage("remote_sign_in_not_allowed")
	case errors.Is(e, operations.ErrInvalid):
		status, code, message = 400, "invalid_request", "Check the submitted fields and their limits."
	case errors.Is(e, operations.ErrIdempotencyKeyReused):
		status, code, message = 409, "idempotency_key_reused", "This request conflicts with an earlier request."
	case errors.Is(e, operations.ErrConflict):
		status, code, message = 409, "administration_conflict", publicErrorMessage("administration_conflict")
	case errors.Is(e, sql.ErrNoRows):
		status, code, message = 404, "not_found", "This record is not available."
	case errors.Is(e, context.DeadlineExceeded):
		status, code, message = 503, "timeout", "The operation timed out. Retry shortly."
	}
	detail := map[string]any{"code": code, "message": message, "retryable": status == 503 || status == 429}
	var conflict *access.ConflictError
	if errors.As(e, &conflict) {
		detail["currentRevision"] = conflict.CurrentRevision
	}
	var settingsConflict *operations.ConflictError
	if errors.As(e, &settingsConflict) {
		detail["currentRevision"] = settingsConflict.CurrentRevision
	}
	var fields *access.ValidationError
	if errors.As(e, &fields) {
		detail["fields"] = fields.Fields
	}
	var settingsFields *operations.ValidationError
	if errors.As(e, &settingsFields) {
		detail["fields"] = settingsFields.Fields
	}
	if status == 429 {
		w.Header().Set("Retry-After", "60")
	}
	write(w, status, map[string]any{"error": detail})
}

// settingsDocument reads the effective settings with the server's own authority.
// Connectivity and diagnostics policy are rows in that one document.
func (d Dependencies) settingsDocument(ctx context.Context) (operations.SettingsDocument, error) {
	if d.Console == nil {
		return operations.SettingsDocument{Effective: operations.DefaultSettings(), Requested: operations.DefaultSettings()}, nil
	}
	return d.Console.Settings(ctx, operations.AllowServerScope)
}

// enforcer builds the admission enforcer over the member limits store.
func (d Dependencies) enforcer() *access.Enforcer {
	return &access.Enforcer{Store: d.Access.Access}
}
