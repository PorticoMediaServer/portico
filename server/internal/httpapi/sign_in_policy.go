package httpapi

import (
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

func (d Dependencies) signInPolicyRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/sign-in/policy", func(w http.ResponseWriter, r *http.Request) {
		out, e := d.Identity.ReadSignInPolicy(r.Context(), directBearer(r))
		if e != nil {
			signInPolicyFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PATCH /v1/admin/sign-in/policy", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.owner(r); e != nil {
			signInPolicyFailure(w, e)
			return
		}
		var q identity.SignInPolicyPatch
		if e := decode(w, r, &q); e != nil {
			signInPolicyFailure(w, e)
			return
		}
		out, e := d.Identity.ChangeSignInPolicy(r.Context(), directBearer(r), q)
		if e != nil {
			signInPolicyFailure(w, e)
			return
		}
		write(w, 200, out)
	})
}
func registerSignInPolicyHTTPContracts(registry *apikit.Registry) error {
	errors := []string{"invalid_request", "unauthorized", "not_permitted", "revision_required", "revision_mismatch", "factor_setup_required", "internal"}
	if e := apikit.RegisterHTTP[struct{}, identity.SignInPolicy](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_sign_in_policy", Method: "GET", Path: "/v1/admin/sign-in/policy", Summary: "Read the server sign-in policy", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneAdmin, Cost: apikit.Constant, Status: 200, Errors: errors}); e != nil {
		return e
	}
	return apikit.RegisterHTTP[identity.SignInPolicyPatch, identity.SignInPolicy](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_sign_in_policy", Method: "PATCH", Path: "/v1/admin/sign-in/policy", Summary: "Change the server sign-in policy", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: errors})
}

func signInPolicyFailure(w http.ResponseWriter, e error) {
	if revisionFailure(w, e) {
		return
	}
	if requestinput.Is(e) || errors.Is(e, identity.ErrDirectInput) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "invalid_request"})
		return
	}
	if errors.Is(e, identity.ErrProfileChanged) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "revision_mismatch"})
		return
	}
	if errors.Is(e, identity.ErrFactorSetupRequired) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "factor_setup_required"})
		return
	}
	policyErrors.Write(w, w.Header().Get("X-Request-Id"), v1Error(e))
}
