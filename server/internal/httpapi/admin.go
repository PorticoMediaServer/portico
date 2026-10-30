package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strconv"
	"time"
)

func (d Dependencies) adminRequest(r *http.Request, kind string) (catalog.AdminRequest, error) {
	p, e := d.libraryAdministrationRead(r, r.PathValue("id"))
	if e != nil {
		return catalog.AdminRequest{}, e
	}
	out := catalog.AdminRequest{ServerID: d.Identity.ID(), Profile: identity.PersonalKey(p.Viewer), ViewerFence: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch)))), Limit: 40, LibraryID: r.PathValue("id")}
	out.Authorize = func(ctx context.Context, tx *sql.Tx, library string) error {
		return d.libraryAdministrationAuthorize(ctx, tx, p, library)
	}
	out.LibraryScope = d.libraryAdministrationScope(p)
	restrictions, fence, err := d.viewerRestrictions(r, p)
	if err != nil {
		return out, err
	}
	out.Restrictions = restrictions
	out.ViewerFence += "/" + fence
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			return out, catalog.ErrAdminQuery
		}
		switch key {
		case "limit":
			if kind == "resource" {
				return out, catalog.ErrAdminQuery
			}
			out.Limit, e = strconv.Atoi(values[0])
			if e != nil || out.Limit < 1 || out.Limit > 40 {
				return out, catalog.ErrAdminQuery
			}
		case "cursor":
			if kind == "resource" {
				return out, catalog.ErrAdminQuery
			}
			out.Cursor = values[0]
		case "libraryId":
			if kind != "jobs" {
				return out, catalog.ErrAdminQuery
			}
			out.LibraryID = values[0]
		case "status":
			if kind != "jobs" {
				return out, catalog.ErrAdminQuery
			}
			out.Status = values[0]
		default:
			return out, catalog.ErrAdminQuery
		}
	}
	return out, nil
}
func (d Dependencies) adminRoutes(mux *http.ServeMux) {
	for _, kind := range []string{"libraries", "jobs", "resource"} {
		route := "GET /v1/admin/" + kind
		if kind == "resource" {
			route = "GET /v1/admin/libraries/{id}"
		}
		d.httpHandle(mux, route, func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			q, e := d.adminRequest(r, kind)
			if e != nil {
				failure(w, e)
				return
			}
			var out any
			switch kind {
			case "libraries":
				out, e = d.Catalog.AdminLibraries(ctx, q)
			case "jobs":
				out, e = d.Catalog.AdminJobs(ctx, q)
			case "resource":
				var l catalog.AdminLibrary
				l, e = d.Catalog.AdminLibrary(ctx, q, true)
				out = map[string]any{"scope": catalog.AdminScope{ServerID: q.ServerID, ViewerFence: q.ViewerFence}, "library": l}
			}
			if e != nil {
				failure(w, e)
				return
			}
			if _, e = d.libraryAdministrationRead(r, q.LibraryID); e != nil {
				failure(w, e)
				return
			}
			write(w, 200, out)
		})
	}
	// The owner's order of the libraries, as every client lists them (GET /v1/libraries).
	d.httpHandle(mux, "PUT /v1/admin/libraries/order", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			LibraryIDs []string `json:"libraryIds"`
		}
		if e = decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		if e = d.Catalog.OrderLibraries(r.Context(), body.LibraryIDs, func(tx *sql.Tx) error {
			return d.libraryAdministrationAuthority(p, body.LibraryIDs...)(r.Context(), tx)
		}); e != nil {
			failure(w, e)
			return
		}
		ids := body.LibraryIDs
		if _, _, protocol := usermutation.RequestFrom(r.Context()); protocol {
			libraries, err := d.Catalog.WithContext(r.Context()).Libraries()
			if err != nil {
				failure(w, err)
				return
			}
			ids = make([]string, 0, len(libraries))
			for _, library := range libraries {
				ids = append(ids, library.ID)
			}
		}
		write(w, 200, map[string]any{"libraryIds": ids})
	})
	// How one show's episodes are presented (Spec — Title Pages §4): the owner's, from the show's page.
	d.httpHandle(mux, "PUT /v1/admin/libraries/{id}/shows/{show}/settings", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			failure(w, e)
			return
		}
		var body catalog.ShowSettings
		if e = decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.SetShowSettings(r.Context(), r.PathValue("id"), r.PathValue("show"), body, func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, r.PathValue("id")) })
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PATCH /v1/admin/libraries/{id}", func(w http.ResponseWriter, r *http.Request) {
		q, e := d.adminRequest(r, "resource")
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			Name             string `json:"name"`
			ExpectedRevision int64  `json:"expectedRevision"`
		}
		if e = decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		p, e := d.administrator(r)
		if e != nil {
			failure(w, e)
			return
		}
		if e = d.Catalog.RenameLibrary(r.Context(), q.LibraryID, body.Name, body.ExpectedRevision, func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, q.LibraryID) }); e != nil {
			if errors.Is(e, catalog.ErrLibraryConfigurationConflict) {
				write(w, 409, map[string]any{"error": map[string]any{"code": "library_configuration_conflict", "message": publicErrorMessage("library_configuration_conflict"), "retryable": false}})
				return
			}
			failure(w, e)
			return
		}
		l, e := d.Catalog.AdminLibrary(r.Context(), q, false)
		if e != nil {
			failure(w, e)
			return
		}
		if _, e = d.libraryAdministrationRead(r, q.LibraryID); e != nil {
			failure(w, e)
			return
		}
		write(w, 200, map[string]any{"scope": catalog.AdminScope{ServerID: q.ServerID, ViewerFence: q.ViewerFence}, "library": l})
	})
}
