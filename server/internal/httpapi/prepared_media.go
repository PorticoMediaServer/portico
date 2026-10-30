package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
)

func (d Dependencies) AuthorizePreparedMedia(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
	return d.playbackAuthorityTx(ctx, tx, p, item)
}
func (d Dependencies) ContinuePreparedMedia(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
	return d.playbackFamilyAuthorityTx(ctx, tx, p, item)
}
func preparedFailure(w http.ResponseWriter, e error) {
	if writerRefused(w, e) {
		return
	}
	if errors.Is(e, catalog.ErrVisibilityBuilding) {
		// A catalogue change is still publishing: the shared retryable code.
		failure(w, e)
		return
	}
	status, code, message := 503, "optimization_unavailable", "Prepared media is unavailable. Refresh and try again."
	switch {
	// CD-51: a refused but valid session is 403 (or a hidden 404), never 401.
	case errors.Is(e, identity.ErrNotVisible):
		status, code, message = 404, "not_found", publicErrorMessage("not_found")
	case errors.Is(e, identity.ErrForbidden):
		status, code, message = 403, "forbidden", identity.ErrForbidden.Error()
	case errors.Is(e, identity.ErrUnauthorized):
		status, code, message = 401, "unauthorized", "Authentication is required."
	case errors.Is(e, preparedmedia.ErrOwner):
		status, code, message = 403, "forbidden", "Only the server owner can manage prepared versions."
	case errors.Is(e, sql.ErrNoRows):
		status, code, message = 404, "optimization_not_found", "This optimization or version no longer exists."
	case errors.Is(e, preparedmedia.ErrInput):
		status, code, message = 400, "optimization_invalid_input", "Choose a current source, profile and target."
	case errors.Is(e, preparedmedia.ErrSourceChanged), errors.Is(e, preparedmedia.ErrConflict):
		status, code, message = 409, "optimization_conflict", "The source or prepared version changed. Refresh before trying again."
	case errors.Is(e, operations.ErrConflict):
		status, code, message = 409, "operation_conflict", "This operation identifier belongs to another request."
	case errors.Is(e, operations.ErrCapacity):
		status, code, message = 429, "optimization_capacity", "The optimization queue or prepared-version limit has been reached."
	case errors.Is(e, preparedmedia.ErrConfiguration):
		status, code, message = 422, "optimization_configuration", "Configure FFmpeg, FFprobe and a supported confined decoder before optimizing."
	case errors.Is(e, preparedmedia.ErrUnsupported):
		status, code, message = 422, "optimization_unsupported", "This source, profile or owner conversion policy does not support optimization."
	case errors.Is(e, preparedmedia.ErrUnavailable):
		status, code, message = 409, "optimization_source_unavailable", "The selected source is unavailable."
	case errors.Is(e, livechannels.ErrPhysicalBusy):
		status, code, message = 503, "prepared_media_busy", "Prepared media is retiring an earlier reader or producer. Retry shortly."
	}
	if status == 503 {
		w.Header().Set("Retry-After", "2")
	}
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": status == 503 || status == 409}})
}
func (d Dependencies) preparedMediaRoutes(mux *http.ServeMux) {
	if d.Prepared == nil {
		return
	}
	type handle func(http.ResponseWriter, *http.Request, identity.Principal) (any, error)
	register := func(pattern string, fn handle) {
		d.httpHandle(mux, pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			if r.URL.RawQuery != "" {
				preparedFailure(w, preparedmedia.ErrInput)
				return
			}
			p, e := d.principal(r)
			if e != nil {
				preparedFailure(w, e)
				return
			}
			out, e := fn(w, r, p)
			if e != nil {
				preparedFailure(w, e)
				return
			}
			write(w, 200, out)
		})
	}
	register("GET /v1/items/{id}/prepared-versions", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		return d.Prepared.View(r.Context(), p, r.PathValue("id"))
	})
	register("POST /v1/items/{id}/optimizations", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var request preparedmedia.Request
		if e := decode(w, r, &request); e != nil {
			return nil, preparedmedia.ErrInput
		}
		if request.ItemID != r.PathValue("id") {
			return nil, preparedmedia.ErrInput
		}
		return d.Prepared.Submit(r.Context(), p, request)
	})
	for _, action := range []string{"cancel", "retry"} {
		register("POST /v1/optimization-jobs/{id}/"+action, func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
			var request preparedmedia.Command
			if e := decode(w, r, &request); e != nil {
				return nil, preparedmedia.ErrInput
			}
			return d.Prepared.Command(r.Context(), p, r.PathValue("id"), action, request)
		})
	}
	register("POST /v1/prepared-versions/{id}/delete", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var request preparedmedia.Command
		if e := decode(w, r, &request); e != nil {
			return nil, preparedmedia.ErrInput
		}
		return d.Prepared.Delete(r.Context(), p, r.PathValue("id"), request)
	})
}
