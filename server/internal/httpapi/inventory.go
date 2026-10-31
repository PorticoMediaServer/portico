package httpapi

import (
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"net/http"
)

func (d Dependencies) inventoryRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/libraries/{id}/inventory-status", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		library := r.PathValue("id")
		if e = d.Hosted.Allowed(p, library); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.InventoryStatus(r.Context(), library)
		if e != nil {
			failure(w, e)
			return
		}
		p, e = d.principal(r)
		if e == nil {
			e = d.Hosted.Allowed(p, library)
		}
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, map[string]any{"serverId": d.Identity.ID(), "inventory": out})
	})
	d.httpHandle(mux, "GET /v1/admin/libraries/{id}/inventory-config", func(w http.ResponseWriter, r *http.Request) {
		q, e := d.adminRequest(r, "resource")
		if e != nil {
			failure(w, e)
			return
		}
		l, e := d.Catalog.AdminLibrary(r.Context(), q, false)
		if e != nil {
			failure(w, e)
			return
		}
		sources, e := d.Catalog.LibrarySources(r.Context(), q.LibraryID)
		if e != nil {
			failure(w, e)
			return
		}
		policy, e := d.Catalog.ScanPolicy(r.Context(), q.LibraryID)
		if e != nil {
			failure(w, e)
			return
		}
		// Detect a configuration mutation between the component reads.
		after, e := d.Catalog.AdminLibrary(r.Context(), q, false)
		if e == nil && after.Revision != l.Revision {
			e = catalog.ErrStaleContinuation
		}
		if e != nil {
			failure(w, e)
			return
		}
		if _, e = d.libraryAdministrationRead(r, r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		write(w, 200, map[string]any{"scope": catalog.AdminScope{ServerID: q.ServerID, ViewerFence: q.ViewerFence}, "libraryId": l.ID, "revision": l.Revision, "sources": sources, "policy": policy})
	})

	for _, pattern := range []string{"POST /v1/admin/libraries/{id}/sources", "PATCH /v1/admin/libraries/{id}/sources/{source}"} {
		d.httpHandle(mux, pattern, func(w http.ResponseWriter, r *http.Request) {
			p, e := d.libraryAdministrationRead(r, r.PathValue("id"))
			if e != nil {
				failure(w, e)
				return
			}
			var body catalog.SourceSettings
			if e = decodeLegacyRevision(w, r, &body); e != nil {
				failure(w, e)
				return
			}
			// Attaching a folder scans it only when the caller asks; editing an
			// existing folder never scans here.
			var after func(*sql.Tx, string) error
			if r.Method == http.MethodPost && d.Ingestion != nil && body.ScanAfter != nil && *body.ScanAfter {
				after = func(tx *sql.Tx, source string) error {
					_, err := d.Ingestion.QueueSourceTx(r.Context(), tx, r.PathValue("id"), source)
					return err
				}
			}
			out, e := d.Catalog.SaveSourceThen(r.Context(), r.PathValue("id"), r.PathValue("source"), body, func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, r.PathValue("id")) }, after)
			if e != nil {
				failure(w, e)
				return
			}
			write(w, 200, out)
		})
	}
	d.httpHandle(mux, "POST /v1/admin/libraries/{id}/sources/{source}/check", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.libraryAdministrationRead(r, r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.CheckSource(r.Context(), r.PathValue("id"), r.PathValue("source"), func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, r.PathValue("id")) })
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "DELETE /v1/admin/libraries/{id}/sources/{source}", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.libraryAdministrationRead(r, r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			ExpectedRevision int64 `json:"expectedRevision"`
		}
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		if e = d.Catalog.RemoveSource(r.Context(), r.PathValue("id"), r.PathValue("source"), body.ExpectedRevision, func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, r.PathValue("id")) }, d.removalEnqueue(p)); e != nil {
			failure(w, e)
			return
		}
		d.Ingestion.InterruptSource(r.Context(), r.PathValue("source"))
		w.WriteHeader(204)
	})
	d.httpHandle(mux, "POST /v1/admin/libraries/{id}/sources/{source}/scans", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.libraryAdministrationRead(r, r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Ingestion.QueueMode(r.Context(), r.PathValue("id"), r.PathValue("source"), r.URL.Query().Get("mode"), func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, r.PathValue("id")) })
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 202, out)
	})
	d.httpHandle(mux, "PUT /v1/admin/libraries/{id}/scan-policy", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.libraryAdministrationRead(r, r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			Tier             string   `json:"tier"`
			Operations       []string `json:"operations"`
			ExpectedRevision int64    `json:"expectedRevision"`
		}
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.UpdateScanPolicy(r.Context(), r.PathValue("id"), catalog.ScanPolicy{Tier: body.Tier, Operations: body.Operations}, body.ExpectedRevision, func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, r.PathValue("id")) })
		if e != nil {
			failure(w, e)
			return
		}
		if e = d.Ingestion.PolicyChanged(r.Context(), r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/admin/ingestion/jobs/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			failure(w, e)
			return
		}
		authorize := func(tx *sql.Tx) error {
			return d.libraryAdministrationJobAuthorize(r.Context(), tx, p, r.PathValue("id"))
		}
		action := r.PathValue("action")
		if action == "retry" {
			out, e := d.Ingestion.Retry(r.Context(), r.PathValue("id"), authorize)
			if e != nil {
				failure(w, e)
				return
			}
			write(w, 202, out)
			return
		}
		if action != "pause" && action != "resume" && action != "cancel" {
			failure(w, catalog.ErrAdminQuery)
			return
		}
		if e = d.Ingestion.Control(r.Context(), r.PathValue("id"), action, authorize); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	// separate-folder: this file's folder is an album or book of its own. It has
	// its own contract, so its answers (including in-progress and revision
	// conflicts) are declared and its client path is generated.
	d.httpHandle(mux, "POST /v1/admin/libraries/{id}/inventory/{object}/separate-folder", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.libraryAdministrationRead(r, r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			Revision string `json:"revision"`
		}
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		authorize := func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, r.PathValue("id")) }
		container, e := d.Catalog.SeparateFolder(catalog.WithSeparateFolderActor(r.Context(), p), r.PathValue("id"), r.PathValue("object"), body.Revision, authorize)
		if e == nil {
			write(w, 200, map[string]any{"itemId": container})
			return
		}
		// The bounded command is still admitted and progressing, so this call
		// reached no finalize and has no id to report. That is honest
		// backpressure, not a fault: answer with the existing
		// request_in_progress policy and its retry delay. Genuine faults,
		// authorization refusals and revision conflicts keep their own
		// statuses.
		if errors.Is(e, catalog.ErrSeparateFolderPending) {
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "request_in_progress", RetryAfterSeconds: 2, Cause: e})
			return
		}
		// A stale folder binding is the same revision conflict every other
		// revision-fenced change reports, not a fault.
		if errors.Is(e, compactcatalog.ErrMutationStale) {
			policyError(w, "revision_mismatch")
			return
		}
		failure(w, e)
	})
	d.httpHandle(mux, "POST /v1/admin/libraries/{id}/inventory/{object}/{action}", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.libraryAdministrationRead(r, r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			Revision string `json:"revision"`
			ItemID   string `json:"itemId"`
		}
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		authorize := func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, r.PathValue("id")) }
		// associate: this file is another version of that title.
		// separate:  this file is not a version of the title it was grouped with.
		switch r.PathValue("action") {
		case "associate":
			e = d.Catalog.AssociateInventoryVersion(r.Context(), r.PathValue("id"), r.PathValue("object"), body.Revision, body.ItemID, authorize)
		case "separate":
			var item string
			if item, e = d.Catalog.SeparateInventoryVersion(r.Context(), r.PathValue("id"), r.PathValue("object"), body.Revision, authorize); e == nil {
				write(w, 200, map[string]any{"itemId": item})
				return
			}
		default:
			e = catalog.ErrAdminQuery
		}
		if e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
}
