package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

type securityReturnPrepareRequest struct {
	Purpose            string    `json:"purpose"`
	Target             string    `json:"target"`
	Origin             string    `json:"origin"`
	OpeningID          string    `json:"openingId"`
	TaskID             string    `json:"taskId"`
	OperationID        string    `json:"operationId"`
	ProviderIdentityID string    `json:"providerIdentityId"`
	ProviderRevision   int64     `json:"providerRevision,string"`
	Nonce              string    `json:"nonce"`
	ExpiresAt          time.Time `json:"expiresAt"`
}
type securityReturnConfirmRequest struct {
	Context       serverreturn.SecuritySigned `json:"context"`
	TransactionID string                      `json:"transactionId"`
	Code          string                      `json:"code"`
}
type securityReturnCancelRequest struct {
	Context serverreturn.SecuritySigned `json:"context"`
}
type securityReturnSigner interface {
	SignSecurityReturn(context.Context, serverreturn.SecurityContext, networking.SecurityActorReader) (serverreturn.SecuritySigned, error)
	ConfirmSecurityReturn(context.Context, serverreturn.SecuritySigned, string, string, networking.SecurityActorReader) (serverreturn.SecuritySigned, error)
	CancelSecurityReturn(context.Context, serverreturn.SecuritySigned, networking.SecurityActorReader) error
	CheckSecurityReturn(context.Context, serverreturn.SecuritySigned, networking.SecurityActorReader) error
}

// This unverified parse selects no authority. The signer subsequently verifies
// every original byte; the edge also requires the actual request's origin/host.
func securityReturnRequestOrigin(r *http.Request, q serverreturn.SecuritySigned) bool {
	raw, e := base64.RawURLEncoding.Strict().DecodeString(q.Payload)
	var value struct {
		Origin string `json:"origin"`
	}
	if e != nil || len(raw) > 6144 || json.Unmarshal(raw, &value) != nil || !serverreturn.Origin(value.Origin) || value.Origin != r.Header.Get("Origin") {
		return false
	}
	u, e := url.Parse(value.Origin)
	return e == nil && networkpolicy.HostsMatch(u.Host, r.Host)
}

func securityReturnFailure(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, networking.ErrInvalid):
		failure(w, identity.ErrDirectInput)
	case errors.Is(e, networking.ErrStale), errors.Is(e, networking.ErrCancelled):
		failure(w, identity.ErrUnauthorized)
	case errors.Is(e, networking.ErrUnavailable):
		failure(w, hosted.ErrServicesUnavailable)
	default:
		failure(w, e)
	}
}

func (d Dependencies) securityReturnRoutes(mux *http.ServeMux) {
	actor := func(r *http.Request) networking.SecurityActorReader {
		bearer := directBearer(r)
		return func(ctx context.Context, tx *sql.Tx) (string, string, time.Time, error) {
			return d.Identity.SecurityReturnActorTx(ctx, tx, bearer)
		}
	}
	d.httpHandle(mux, "POST /v1/direct/security-return/prepare", func(w http.ResponseWriter, r *http.Request) {
		var q securityReturnPrepareRequest
		if e := decode(w, r, &q); e != nil {
			securityReturnFailure(w, e)
			return
		}
		u, e := url.Parse(q.Origin)
		if e != nil || !serverreturn.Origin(q.Origin) || r.Header.Get("Origin") != q.Origin || !networkpolicy.HostsMatch(u.Host, r.Host) {
			failure(w, identity.ErrDirectInput)
			return
		}
		signer, ok := d.RouteIdentity.(securityReturnSigner)
		if !ok || d.Hosted == nil {
			failure(w, identity.ErrUnauthorized)
			return
		}
		prepared, e := d.Hosted.PrepareRegisteredReturn(r.Context(), q.Origin)
		if e != nil {
			securityReturnFailure(w, e)
			return
		}
		out, e := signer.SignSecurityReturn(r.Context(), serverreturn.SecurityContext{Purpose: q.Purpose, Target: q.Target, ServerID: prepared.ServerID, RegistrationID: prepared.Registration.ID, Origin: prepared.Registration.Origin, HostedOrigin: prepared.HostedOrigin, ClaimOperationID: prepared.ClaimOperationID, OpeningID: q.OpeningID, TaskID: q.TaskID, OperationID: q.OperationID, ProviderIdentityID: q.ProviderIdentityID, ProviderRevision: q.ProviderRevision, Nonce: q.Nonce, ExpiresAt: q.ExpiresAt}, actor(r))
		if e != nil {
			securityReturnFailure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/direct/security-return/confirm", func(w http.ResponseWriter, r *http.Request) {
		var q securityReturnConfirmRequest
		if e := decode(w, r, &q); e != nil {
			securityReturnFailure(w, e)
			return
		}
		if !securityReturnRequestOrigin(r, q.Context) {
			failure(w, identity.ErrDirectInput)
			return
		}
		signer, ok := d.RouteIdentity.(securityReturnSigner)
		if !ok {
			failure(w, identity.ErrUnauthorized)
			return
		}
		out, e := signer.ConfirmSecurityReturn(r.Context(), q.Context, q.TransactionID, q.Code, actor(r))
		if e != nil {
			securityReturnFailure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})

	d.httpHandle(mux, "POST /v1/direct/security-return/check", func(w http.ResponseWriter, r *http.Request) {
		var q securityReturnCancelRequest
		if e := decode(w, r, &q); e != nil {
			securityReturnFailure(w, e)
			return
		}
		if !securityReturnRequestOrigin(r, q.Context) {
			failure(w, identity.ErrDirectInput)
			return
		}
		signer, ok := d.RouteIdentity.(securityReturnSigner)
		if !ok {
			failure(w, identity.ErrUnauthorized)
			return
		}
		if e := signer.CheckSecurityReturn(r.Context(), q.Context, actor(r)); e != nil {
			securityReturnFailure(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	d.httpHandle(mux, "POST /v1/direct/security-return/cancel", func(w http.ResponseWriter, r *http.Request) {
		var q securityReturnCancelRequest
		if e := decode(w, r, &q); e != nil {
			securityReturnFailure(w, e)
			return
		}
		if !securityReturnRequestOrigin(r, q.Context) {
			failure(w, identity.ErrDirectInput)
			return
		}
		signer, ok := d.RouteIdentity.(securityReturnSigner)
		if !ok {
			failure(w, identity.ErrUnauthorized)
			return
		}
		if e := signer.CancelSecurityReturn(r.Context(), q.Context, actor(r)); e != nil {
			securityReturnFailure(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func registerSecurityReturnHTTPContracts(registry *apikit.Registry) error {
	meta := func(id, path, summary string, status int) apikit.Metadata {
		return apikit.Metadata{Audience: apikit.Clients, ID: id, Method: "POST", Path: path, Summary: summary, Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.Constant, BodyLimit: 65536, Status: status, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "sign_in_kind_local", "portico_services_unavailable", "password_change_required", "factor_setup_required", "internal"}}
	}
	if e := apikit.RegisterHTTP[securityReturnPrepareRequest, serverreturn.SecuritySigned](registry, meta("post_direct_security_return_prepare", "/v1/direct/security-return/prepare", "Prepare only the original provider Security opening", 200)); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[securityReturnConfirmRequest, serverreturn.SecuritySigned](registry, meta("post_direct_security_return_confirm", "/v1/direct/security-return/confirm", "Confirm only the original provider Security return", 200)); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[securityReturnCancelRequest, struct{}](registry, meta("post_direct_security_return_check", "/v1/direct/security-return/check", "Check only the original Account Settings UI return scope", 204)); e != nil {
		return e
	}
	return apikit.RegisterHTTP[securityReturnCancelRequest, struct{}](registry, meta("post_direct_security_return_cancel", "/v1/direct/security-return/cancel", "Cancel only the original provider Security opening", 204))
}
