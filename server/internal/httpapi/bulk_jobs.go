package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func (d Dependencies) bulkAccess(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (string, []any, error) {
	if _, err := d.Identity.SessionFamilyTx(ctx, tx, p); err != nil {
		return "", nil, err
	}
	return contentaccess.VisibleItemsSQL(ctx, tx, p, item, func(library string) error { return d.allowedLibraryTx(ctx, p, library, tx) })
}
func bulkError(err error) error {
	var invalid *catalog.BrowseValidationError
	switch {
	case errors.As(err, &invalid), errors.Is(err, catalog.ErrBrowsePivot):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	case errors.Is(err, catalog.ErrOperationConflict):
		return &apierror.Error{Code: "idempotency_key_reused", Cause: err}
	case errors.Is(err, catalog.ErrOperationExpired):
		return &apierror.Error{Code: "operation_expired", Cause: err}
	case errors.Is(err, catalog.ErrPersonalConflict), errors.Is(err, catalog.ErrStaleContinuation):
		return &apierror.Error{Code: "revision_mismatch", Cause: err}
	case errors.Is(err, catalog.ErrPersonalCapacity), errors.Is(err, operations.ErrCapacity):
		return &apierror.Error{Code: "rate_limited", Cause: err}
	case errors.Is(err, catalog.ErrJobRequest):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	case errors.Is(err, catalog.ErrCursor):
		return &apierror.Error{Code: "invalid_cursor", Cause: err}
	}
	return err
}
func registerBulkJobs(r *apikit.Registry, d Dependencies) {
	errorsList := []string{"invalid_request", "unauthorized", "not_found", "idempotency_key_reused", "revision_mismatch", "rate_limited", "internal"}
	rate := &personalLimiter{}
	scope := func(q *http.Request) (identity.Principal, catalog.Viewer, error) {
		p, libraries, fence, e := d.homeScope(q)
		if e != nil {
			return p, catalog.Viewer{}, e
		}
		v, e := d.catalogViewer(q, p, libraries, fence)
		return p, v, e
	}
	v1Route(r, apikit.Metadata{Audience: apikit.Integrations, Admission: laneDefault, ID: "create_job", Method: "POST", Path: "/v1/jobs", Summary: "Capture a selection and enqueue a durable bulk job", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.SelectionAsync, BodyLimit: 65536, Status: 202, Errors: errorsList},
		func(ctx context.Context, q *http.Request, in catalog.JobRequest) (catalog.BulkJob, error) {
			p, v, e := scope(q)
			if e != nil {
				return catalog.BulkJob{}, e
			}
			if in.Command == "metadata-edit" || in.Command == "refresh" {
				if e = registryScopeGate(q, apikit.ScopeLibrary); e != nil {
					return catalog.BulkJob{}, e
				}
			}
			if !rate.allow(viewerScope(p)) {
				return catalog.BulkJob{}, &apierror.Error{Code: "rate_limited"}
			}
			out, e := d.Catalog.CreateBulkJob(ctx, p, v, in, d.Scheduler, d.bulkAccess)
			return out, bulkError(e)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Integrations, Admission: laneDefault, ID: "get_job", Method: "GET", Path: "/v1/jobs/{id}", Summary: "Read this profile's job progress", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: errorsList},
		func(ctx context.Context, q *http.Request, _ noBody) (catalog.BulkJob, error) {
			p, _, e := scope(q)
			if e != nil {
				return catalog.BulkJob{}, e
			}
			out, e := d.Catalog.WithContext(ctx).BulkJob(identity.PersonalKey(p.Viewer), q.PathValue("id"))
			return out, bulkError(e)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Integrations, Admission: laneDefault, ID: "get_job_failures", Method: "GET", Path: "/v1/jobs/{id}/failures", Summary: "Read visible failures from this profile's job", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Query: []string{"cursor"}, Errors: append(errorsList, "invalid_cursor")},
		func(ctx context.Context, q *http.Request, _ noBody) (catalog.JobFailures, error) {
			p, v, e := scope(q)
			if e != nil {
				return catalog.JobFailures{}, e
			}
			out, e := d.Catalog.WithContext(ctx).BulkFailures(identity.PersonalKey(p.Viewer), q.PathValue("id"), q.URL.Query().Get("cursor"), v)
			return out, bulkError(e)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_container_personal_state", Method: "GET", Path: "/v1/containers/{kind}/{id}/personal-state", Summary: "Read a container watermark, revision, saved flags and visible-member counts", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, Errors: errorsList},
		func(ctx context.Context, q *http.Request, _ noBody) (catalog.ContainerPersonalView, error) {
			_, v, e := scope(q)
			if e != nil {
				return catalog.ContainerPersonalView{}, e
			}
			out, e := d.Catalog.WithContext(ctx).ReadContainerPersonal(v, q.PathValue("kind"), q.PathValue("id"))
			return out, bulkError(e)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "set_container_personal_state", Method: "PUT", Path: "/v1/containers/{kind}/{id}/personal-state", Summary: "Set a container watched watermark, or its Watchlist or Favorite flag, for this profile", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: 2048, Status: 200, Errors: errorsList},
		func(ctx context.Context, q *http.Request, in catalog.ContainerPersonalMutation) (catalog.ContainerPersonal, error) {
			p, v, e := scope(q)
			if e != nil {
				return catalog.ContainerPersonal{}, e
			}
			if !rate.allow(viewerScope(p)) {
				return catalog.ContainerPersonal{}, &apierror.Error{Code: "rate_limited"}
			}
			if in.Watched == nil {
				out, e := d.Catalog.SetContainerSaved(ctx, p, v, q.PathValue("kind"), q.PathValue("id"), in)
				return out, bulkError(e)
			}
			out, e := d.Catalog.SetContainerPersonal(ctx, p, q.PathValue("kind"), q.PathValue("id"), in, d.Scheduler, d.bulkAccess)
			return out, bulkError(e)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "undo_container_personal_state", Method: "POST", Path: "/v1/containers/{kind}/{id}/personal-state/undo", Summary: "Restore watched and resume state before a recent container mark", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: 2048, Status: 200, Errors: append(errorsList, "operation_expired")},
		func(ctx context.Context, q *http.Request, in catalog.ContainerUndoMutation) (catalog.ContainerPersonal, error) {
			p, _, e := scope(q)
			if e != nil {
				return catalog.ContainerPersonal{}, e
			}
			if !rate.allow(viewerScope(p)) {
				return catalog.ContainerPersonal{}, &apierror.Error{Code: "rate_limited"}
			}
			out, e := d.Catalog.UndoContainerPersonal(ctx, p, q.PathValue("kind"), q.PathValue("id"), in, d.bulkAccess)
			return out, bulkError(e)
		})

}
