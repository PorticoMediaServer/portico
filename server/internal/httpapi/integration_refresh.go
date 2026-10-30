package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
)

// IntegrationRefreshRequest is the whole native refresh vocabulary. Omitting
// libraryId queues the one coalesced refresh of every enabled, non-private
// library.
type IntegrationRefreshRequest struct {
	LibraryID string `json:"libraryId,omitempty"`
}

// registerIntegrationRefreshRoute is the native entry point integrations use
// when a tool reports "refresh this library" without naming any changed path.
// It is a real scan request: the answer is 202 with the jobs that were actually
// queued, or deferred when an existing (possibly paused) source job owns the
// source slot. It never invents a job and never reports "nothing changed".
func registerIntegrationRefreshRoute(registry *apikit.Registry, d Dependencies) error {
	return apikit.Register(registry, apikit.Route[IntegrationRefreshRequest, ingestion.PathReport]{Metadata: apikit.Metadata{
		Audience: apikit.Integrations, Admission: laneDefault,
		ID: "refresh_integrations", Method: "POST", Path: "/v1/integrations/refresh",
		Summary:  "Queue a refresh of one library, or of every enabled library",
		Access:   apikit.Owner,
		Requires: apikit.ScopeLibrary, Lane: apikit.Default, Cost: apikit.PageSized,
		BodyLimit: 1 << 20, OptionalBody: true, Status: 202,
		Errors: []string{"unauthorized", "not_permitted", "insufficient_scope", "session_required", "invalid_request", "body_too_large", "not_found", "administration_unavailable", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, body IntegrationRefreshRequest) (ingestion.PathReport, error) {
		empty := ingestion.PathReport{Paths: []ingestion.ReportedPath{}, Jobs: []ingestion.Job{}}
		if d.Ingestion == nil {
			return empty, &apierror.Error{Code: "administration_unavailable"}
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return empty, foundationAuthError(err)
		}
		out, err := d.Ingestion.RefreshLibraries(ctx, body.LibraryID, func(tx *sql.Tx, library string) error {
			return d.resourceAuthorize(tx, p, library)
		})
		var refusal *ingestion.StrictPathRefusal
		if errors.As(err, &refusal) {
			if refusal.Retryable() {
				// Global maintained-intent capacity is the only retryable
				// boundary this operation has: the same typed 503 as strict
				// admission, and nothing was committed.
				return empty, &apierror.Error{Code: "administration_unavailable", Reason: string(refusal.Reason), RetryAfterSeconds: 2, Cause: err}
			}
			return empty, &apierror.Error{Code: "invalid_request", Reason: string(refusal.Reason), Cause: err}
		}
		switch {
		case err == nil:
			if out.Paths == nil {
				out.Paths = []ingestion.ReportedPath{}
			}
			if out.Jobs == nil {
				out.Jobs = []ingestion.Job{}
			}
			return out, nil
		case errors.Is(err, sql.ErrNoRows):
			// Only an absent/private named library is reported as absent.
			// Existing libraries with no enabled sources accept without a job.
			return empty, &apierror.Error{Code: "not_found"}
		case errors.Is(err, ingestion.ErrPathReport):
			return empty, &apierror.Error{Code: "invalid_request"}
		case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrNotVisible):
			return empty, foundationAuthError(err)
		default:
			return empty, &apierror.Error{Code: "internal", Cause: err}
		}
	}})
}
