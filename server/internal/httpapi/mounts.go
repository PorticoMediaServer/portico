package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"time"
)

func (d Dependencies) storageOwner(r *http.Request) (identity.Principal, catalog.AdminScope, error) {
	p, e := d.owner(r)
	scope := catalog.AdminScope{ServerID: d.Identity.ID(), ViewerFence: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch))))}
	if e == nil && d.Mounts == nil {
		e = mounts.ErrUnavailable
	}
	return p, scope, e
}
func storageFailure(w http.ResponseWriter, e error) {
	if writerRefused(w, e) {
		return
	}
	if errors.Is(e, storage.ErrBusy) || errors.Is(e, storage.ErrRemoteConfig) || errors.Is(e, storage.ErrRemoteChanged) || errors.Is(e, storage.ErrRemoteOffline) || errors.Is(e, storage.ErrRemoteCredentials) || errors.Is(e, storage.ErrRemoteRange) || errors.Is(e, storage.ErrRemoteCursor) || errors.Is(e, storage.ErrRemoteLimit) || errors.Is(e, storage.ErrRemoteBinary) {
		remoteSourceFailure(w, e)
		return
	}
	status, code := 0, ""
	switch {
	case errors.Is(e, mounts.ErrAllocationCapacity):
		status, code = 409, "storage_allocation_recovery_required"
	case errors.Is(e, mounts.ErrConfigInvalid):
		status, code = 400, "storage_config_invalid"
	case errors.Is(e, mounts.ErrExecutableInvalid), errors.Is(e, mounts.ErrExecutableTooOld):
		status, code = 400, "storage_executable_invalid"
	case errors.Is(e, mounts.ErrCommandConflict):
		status, code = 409, "storage_command_conflict"
	case errors.Is(e, mounts.ErrCommandExpired):
		status, code = 410, "storage_operation_expired"
	case errors.Is(e, mounts.ErrCommandCapacity):
		status, code = 429, "storage_command_capacity"
	}
	if status == 0 {
		failure(w, e)
		return
	}
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": publicErrorMessage(code), "retryable": false, "serverTime": time.Now().UTC().Format(time.RFC3339Nano)}})
}
func (d Dependencies) mountRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/storage/mounts", func(w http.ResponseWriter, r *http.Request) {
		_, scope, e := d.storageOwner(r)
		if e != nil {
			storageFailure(w, e)
			return
		}
		if len(r.URL.Query()) != 0 {
			storageFailure(w, mounts.ErrInvalid)
			return
		}
		before, e := d.Mounts.Revision()
		if e != nil {
			storageFailure(w, e)
			return
		}
		rows, e := d.Mounts.List()
		if e != nil {
			storageFailure(w, e)
			return
		}
		after, e := d.Mounts.Revision()
		if e != nil {
			storageFailure(w, e)
			return
		}
		if before != after {
			storageFailure(w, catalog.ErrStaleContinuation)
			return
		}
		if _, _, e = d.storageOwner(r); e != nil {
			storageFailure(w, e)
			return
		}
		write(w, 200, map[string]any{"scope": scope, "revision": after, "limits": map[string]int{"definitions": 8, "active": 4}, "mounts": rows, "mountSupported": mounts.MountSupported()})
	})
	d.httpHandle(mux, "GET /v1/storage/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, scope, e := d.storageOwner(r)
		if e != nil {
			storageFailure(w, e)
			return
		}
		if len(r.URL.Query()) != 0 {
			storageFailure(w, mounts.ErrInvalid)
			return
		}
		actor, _ := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID})
		receipt, e := d.Mounts.Receipt(string(actor), r.PathValue("id"))
		if e != nil {
			storageFailure(w, e)
			return
		}
		if _, _, e = d.storageOwner(r); e != nil {
			storageFailure(w, e)
			return
		}
		write(w, 200, struct {
			Scope catalog.AdminScope `json:"scope"`
			mounts.Receipt
		}{scope, receipt})
	})
	for _, route := range []struct{ path, action string }{{"POST /v1/storage/mounts", "create"}, {"POST /v1/storage/mounts/{id}/actions", ""}, {"DELETE /v1/storage/mounts/{id}", "delete"}} {
		d.httpHandle(mux, route.path, func(w http.ResponseWriter, r *http.Request) {
			p, scope, e := d.storageOwner(r)
			if e != nil {
				storageFailure(w, e)
				return
			}
			if len(r.URL.Query()) != 0 {
				storageFailure(w, mounts.ErrInvalid)
				return
			}
			var body mounts.Command
			if e = decode(w, r, &body); e != nil {
				storageFailure(w, e)
				return
			}
			if route.action != "" {
				if body.Action != "" && body.Action != route.action {
					storageFailure(w, mounts.ErrInvalid)
					return
				}
				body.Action = route.action
			}
			actor, _ := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID})
			receipt, e := d.Mounts.Command(r.Context(), string(actor), r.PathValue("id"), body, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
			if e != nil {
				storageFailure(w, e)
				return
			}
			if _, _, e = d.storageOwner(r); e != nil {
				storageFailure(w, e)
				return
			}
			if d.RemoteSources != nil {
				if e = d.RemoteSources.RefreshMounts(r.Context()); e != nil {
					remoteSourceFailure(w, e)
					return
				}
			}
			status := 202
			if route.action == "create" {
				status = 201
			}
			write(w, status, struct {
				Scope catalog.AdminScope `json:"scope"`
				mounts.Receipt
			}{scope, receipt})
		})
	}
}
