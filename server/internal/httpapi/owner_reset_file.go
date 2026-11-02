package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"reflect"
)

type completeAccountSetupRequest struct {
	RequestID       string         `json:"requestId"`
	SetupToken      string         `json:"setupToken"`
	PorticoIdentity *hosted.Signed `json:"porticoIdentity"`
}

func (d Dependencies) ownerResetFileRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "POST /v1/setup/account/complete", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !d.setupRequestAllowed(r) {
			failure(w, identity.ErrUnauthorized)
			return
		}
		var q completeAccountSetupRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if d.Hosted == nil || q.PorticoIdentity == nil {
			failure(w, identity.ErrUnauthorized)
			return
		}
		if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "complete-account-setup", 20); e != nil {
			setupFailure(w, e)
			return
		}
		pending, _, e := d.Identity.SetupClaimAuthority(r.Context(), q.SetupToken)
		if e != nil || pending.RequestID != q.RequestID {
			failure(w, identity.ErrUnauthorized)
			return
		}
		who, e := d.Hosted.VerifyIdentity(r.Context(), *q.PorticoIdentity, r.Header.Get("X-Portico-Installation-Id"))
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.CompleteAccountSetup(r.Context(), q.RequestID, q.SetupToken, who)
		if e != nil {
			failure(w, e)
			return
		}
		// Set up: the remote gate applies from now on.
		d.reloadConnectionPolicy(r.Context())
		// The admitted owner is journalled; push them to Portico Services now.
		d.Hosted.MembershipChanged()
		writeAuth(w, r, 200, out)
	})
	d.httpHandle(mux, "GET /v1/auth/services-reachability", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, d.Hosted.ServicesReachability(r.Context()))
	})
	d.httpHandle(mux, "POST /v1/auth/owner-reset/request", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var q identity.OwnerResetRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "owner-reset-request", 30); e != nil {
			setupFailure(w, e)
			return
		}
		out, e := d.Identity.RequestOwnerReset(r.Context(), q)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/auth/owner-reset/verify", func(w http.ResponseWriter, r *http.Request) {
		var q identity.OwnerResetVerify
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.VerifyOwnerReset(r.Context(), q)
		if e != nil {
			setupFailure(w, e)
			return
		}
		kind := "password-change"
		if out.ResetFileOwnerSession {
			kind = "owner-session"
		}
		writeAuth(w, r, 200, taggedBody{"variant", kind, out})
	})
	d.httpHandle(mux, "POST /v1/direct/members/{id}/reset-code", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var q identity.MemberResetRequest
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.MakeMemberResetCode(r.Context(), directBearer(r), r.PathValue("id"), q)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/direct/password/reset", func(w http.ResponseWriter, r *http.Request) {
		var q identity.MemberResetVerify
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.VerifyMemberReset(r.Context(), q)
		if e != nil {
			setupFailure(w, e)
			return
		}
		writeAuth(w, r, 200, out)
	})
}
func registerResetFileHTTPContracts(registry *apikit.Registry) error {
	if e := registerAccountMoveContracts(registry); e != nil {
		return e
	}
	if e := registerServerReturnHTTPContracts(registry); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[completeAccountSetupRequest, identity.DirectSignIn](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_setup_account_complete", Method: "POST", Path: "/v1/setup/account/complete", Summary: "Complete account setup from its validated claim", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 16384, Status: 200, Headers: []string{"X-Portico-Installation-Id"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "rate_limited", "internal"}}); e != nil {
		return e
	}
	if e := apikit.RegisterHTTP[struct{}, hosted.ServicesReachability](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_auth_services_reachability", Method: "GET", Path: "/v1/auth/services-reachability", Summary: "Check whether Portico Services answers", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, Status: 200, Errors: []string{"internal"}}); e != nil {
		return e
	}
	meta := func(id, path string, access apikit.Access) apikit.Metadata {
		return apikit.Metadata{Audience: apikit.Clients, ID: id, Method: "POST", Path: path, Summary: "Reset account access", Access: access, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal", "rate_limited"}}
	}
	ownerRequest := meta("post_auth_owner_reset_request", "/v1/auth/owner-reset/request", apikit.Public)
	ownerRequest.Errors = append(ownerRequest.Errors, "owner_reset_file_unavailable")
	if e := apikit.RegisterHTTP[identity.OwnerResetRequest, identity.OwnerResetLocation](registry, ownerRequest); e != nil {
		return e
	}
	ownerVerify := meta("post_auth_owner_reset_verify", "/v1/auth/owner-reset/verify", apikit.Public)
	ownerVerify.Errors = append(ownerVerify.Errors, "owner_reset_expired")
	if e := apikit.RegisterHTTP[identity.OwnerResetVerify, ownerResetResponse](registry, ownerVerify); e != nil {
		return e
	}
	memberReset := meta("post_direct_members_id_reset_code", "/v1/direct/members/{id}/reset-code", apikit.Device)
	memberReset.Errors = append(memberReset.Errors, "profile_changed")
	if e := apikit.RegisterHTTP[identity.MemberResetRequest, identity.MemberResetCode](registry, memberReset); e != nil {
		return e
	}
	return apikit.RegisterHTTP[identity.MemberResetVerify, passwordResetResponse](registry, meta("post_direct_password_reset", "/v1/direct/password/reset", apikit.Public))
}

// Reset responses have only two successful authorities. Neither a second-factor
// challenge nor an unapproved device is a successful consumption of this proof.
type ownerResetResponse identity.DirectSignIn
type passwordResetResponse identity.DirectSignIn

func resetSignInSchema(owner bool) map[string]any {
	shape, e := apikit.Schema(reflect.TypeFor[identity.DirectSignIn]())
	if e != nil {
		panic(e)
	}
	props := shape["properties"].(map[string]any)
	delete(props, "challenge")
	required := shape["required"].([]string)
	required = append(required, "session")
	shape["required"] = required
	session := props["session"].(map[string]any)
	sessionProps := session["properties"].(map[string]any)
	sessionProps["refreshToken"].(map[string]any)["maxLength"] = 0
	viewer := sessionProps["viewer"].(map[string]any)["properties"].(map[string]any)
	if owner {
		props["resetFileOwnerSession"] = map[string]any{"type": "boolean", "const": true}
		props["passwordChangeRequired"] = map[string]any{"type": "boolean", "const": false}
		shape["required"] = append(required, "resetFileOwnerSession")
		viewer["role"].(map[string]any)["enum"] = []string{"owner"}
	} else {
		props["passwordChangeRequired"] = map[string]any{"type": "boolean", "const": true}
		props["resetFileOwnerSession"] = map[string]any{"type": "boolean", "const": false}
		shape["required"] = append(required, "passwordChangeRequired")
		viewer["role"].(map[string]any)["enum"] = []string{"account"}
	}
	return shape
}
func (ownerResetResponse) JSONSchema() map[string]any {
	return map[string]any{"oneOf": []any{withTag(resetSignInSchema(false), "variant", "password-change"), withTag(resetSignInSchema(true), "variant", "owner-session")}}
}
func (passwordResetResponse) JSONSchema() map[string]any { return resetSignInSchema(false) }
