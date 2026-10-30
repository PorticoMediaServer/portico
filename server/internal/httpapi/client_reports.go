package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
)

func clientReportError(err error) error {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return &apierror.Error{Code: "not_found"}
	case errors.Is(err, servicelog.ErrClientUpload):
		return &apierror.Error{Code: "invalid_request"}
	case errors.Is(err, servicelog.ErrClientUploadDevice):
		return &apierror.Error{Code: "device_session_required"}
	case errors.Is(err, servicelog.ErrClientLogBudget):
		return &apierror.Error{Code: "client_log_budget"}
	default:
		return &apierror.Error{Code: "internal", Cause: err}
	}
}

func registerClientReports(registry *apikit.Registry, d Dependencies) error {
	store := d.Access.ClientLogs
	errs := []string{"unauthorized", "invalid_request", "device_session_required", "client_log_budget", "internal"}
	err := apikit.Register(registry, apikit.Route[servicelog.ClientUploadRequest, servicelog.ClientUpload]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneUpload, ID: "post_client_log", Method: "POST", Path: "/v1/diagnostics/client-logs", Summary: "Send a client report to this server", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: (servicelog.MaxClientUploadBytes * 6) + 4096, Status: 201, Errors: errs}, Handler: func(ctx context.Context, r *http.Request, body servicelog.ClientUploadRequest) (servicelog.ClientUpload, error) {
		if store == nil {
			return servicelog.ClientUpload{}, &apierror.Error{Code: "internal"}
		}
		principal, err := d.bearerPrincipal(r)
		if err != nil {
			return servicelog.ClientUpload{}, foundationAuthError(err)
		}
		out, err := store.Store(ctx, principal.AccountID, principal.ProfileID, principal.Hash, body)
		if err != nil {
			return out, clientReportError(err)
		}
		return out, nil
	}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, servicelog.ClientUploadPage]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdminHeavy, ID: "get_client_logs", Method: "GET", Path: "/v1/admin/diagnostics/client-logs", Summary: "List client reports", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Query: []string{"cursor", "limit"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 25, Maximum: 100, DefaultBelowMinimum: true, EmptyIsDefault: true}}, Status: 200, Errors: []string{"unauthorized", "invalid_request", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (servicelog.ClientUploadPage, error) {
		if store == nil {
			return servicelog.ClientUploadPage{}, &apierror.Error{Code: "internal"}
		}
		limit := 25
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return servicelog.ClientUploadPage{}, clientReportError(servicelog.ErrClientUpload)
			}
			limit = n
		}
		out, err := store.List(ctx, r.URL.Query().Get("cursor"), limit)
		if err != nil {
			return out, clientReportError(err)
		}
		return out, nil
	}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, servicelog.ClientUpload]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_client_log", Method: "GET", Path: "/v1/admin/diagnostics/client-logs/{id}", Summary: "Read a client report", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_found", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (servicelog.ClientUpload, error) {
		if store == nil {
			return servicelog.ClientUpload{}, &apierror.Error{Code: "internal"}
		}
		out, err := store.Read(ctx, r.PathValue("id"))
		if err != nil {
			return out, clientReportError(err)
		}
		return out, nil
	}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "delete_client_log", Method: "DELETE", Path: "/v1/admin/diagnostics/client-logs/{id}", Summary: "Delete a client report", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_found", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (struct{}, error) {
		if store == nil {
			return struct{}{}, &apierror.Error{Code: "internal"}
		}
		if err := store.Delete(ctx, r.PathValue("id")); err != nil {
			return struct{}{}, clientReportError(err)
		}
		return struct{}{}, nil
	}})
	if err != nil {
		return err
	}
	return apikit.RegisterRaw(registry, apikit.RawRoute[struct{}, servicelog.ClientUpload]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "download_client_log", Method: "GET", Path: "/v1/admin/diagnostics/client-logs/{id}/download", Summary: "Download a client report", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_found", "internal"}}, Serve: func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			registry.Errors().Write(w, "", &apierror.Error{Code: "internal"})
			return
		}
		out, err := store.Read(r.Context(), r.PathValue("id"))
		if err != nil {
			registry.Errors().Write(w, "", clientReportError(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="portico-client-report.json"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = json.NewEncoder(w).Encode(out)
	}})
}
