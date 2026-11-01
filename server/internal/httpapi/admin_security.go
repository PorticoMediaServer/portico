package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
)

// The owner's remote-security summary. Brute-force protection is always on and
// is not a setting; what an owner needs from it is what happened and what is
// blocked right now, plus one action: let a mistaken block go.
//
// Both routes need an administrator in a fresh authority snapshot (the typed
// registry's Access: Admin does the same through v1Authorize). The GET proves it
// again after the read, so a revoked or demoted session receives nothing.
func registerAdminSecurity(registry *apikit.Registry, d Dependencies) error {
	if err := apikit.Register(registry, apikit.Route[struct{}, identity.SecuritySummary]{Metadata: apikit.Metadata{
		Audience: apikit.Clients, Admission: laneAdmin, ID: "get_admin_security_summary", Method: "GET", Path: "/v1/admin/security/summary",
		Summary: "Read refused connections, failed sign-ins, blocked sources and new remote devices for the last day and week", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "security",
		Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (identity.SecuritySummary, error) {
		var out identity.SecuritySummary
		caller, err := d.administrator(r)
		if err != nil {
			return out, v1AuthError(err)
		}
		if d.Identity == nil {
			return out, &apierror.Error{Code: "internal"}
		}
		out, err = d.Identity.SecuritySummary(ctx)
		if err != nil {
			return out, &apierror.Error{Code: "internal", Cause: err}
		}
		var refusals *remotegate.Refusals
		if d.RemoteGate != nil {
			refusals = d.RemoteGate.Refusals
		}
		out.Counts.Last24Hours.RefusedConnections, out.Counts.Last24Hours.RefusedByReason = refusals.Totals(24)
		out.Counts.Last7Days.RefusedConnections, out.Counts.Last7Days.RefusedByReason = refusals.Totals(7 * 24)
		again, err := d.administrator(r)
		if err != nil {
			return out, v1AuthError(err)
		}
		if again.AccountID != caller.AccountID || again.ProfileID != caller.ProfileID || again.Authority != caller.Authority ||
			again.Role != caller.Role || again.Epoch != caller.Epoch || again.Hash != caller.Hash {
			return out, &apierror.Error{Code: "not_permitted"}
		}
		return out, nil
	}}); err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: apikit.Metadata{
		Audience: apikit.Clients, Admission: laneSecurityFence, ID: "post_admin_security_unblock", Method: "POST", Path: "/v1/admin/security/blocks/{id}/unblock",
		Summary: "Clear one blocked source and its failure count", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "security",
		Lane: apikit.Security, Cost: apikit.Constant, Status: 204, NoResponseBody: true, Errors: []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (struct{}, error) {
		if _, err := d.administrator(r); err != nil {
			return struct{}{}, v1AuthError(err)
		}
		if d.Identity == nil {
			return struct{}{}, &apierror.Error{Code: "internal"}
		}
		switch err := d.Identity.UnblockSource(ctx, r.PathValue("id")); {
		case err == nil:
			return struct{}{}, nil
		case errors.Is(err, sql.ErrNoRows):
			return struct{}{}, &apierror.Error{Code: "not_found"}
		case errors.Is(err, identity.ErrDirectInput):
			return struct{}{}, &apierror.Error{Code: "invalid_request"}
		default:
			return struct{}{}, &apierror.Error{Code: "internal", Cause: err}
		}
	}})
}
