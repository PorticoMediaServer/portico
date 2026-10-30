package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"time"
)

// Never return transport errors: even url.Error may contain a private target.
func remoteSourceFailure(w http.ResponseWriter, err error) {
	if revisionFailure(w, err) {
		return
	}
	if errors.Is(err, storage.ErrBusy) || errors.Is(err, remotemedia.ErrBusy) {
		// Local source capacity is not an asynchronous provider throttle report.
		// Use the existing busy response without a provider Retry-After time.
		failure(w, remotemedia.ErrBusy)
		return
	}
	if errors.Is(err, catalog.ErrVisibilityBuilding) {
		// A catalogue change is still publishing: the shared retryable code.
		failure(w, err)
		return
	}
	if errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, mounts.ErrCommandConflict) || errors.Is(err, mounts.ErrCommandExpired) || errors.Is(err, mounts.ErrInvalid) || errors.Is(err, mounts.ErrCommandCapacity) {
		storageFailure(w, err)
		return
	}
	code := remotesources.ErrorCode(err)
	if errors.Is(err, storage.ErrRemoteRange) {
		// Unknown qualification uses the existing interactive limitation, while
		// the producer keeps its distinct diagnostic/source fact unchanged.
		code = "range_unsupported"
	}
	status := 503
	if code == "invalid_configuration" {
		status = 400
	}
	if code == "source_changed" || code == "cursor_expired" {
		status = 409
	}
	if code == "range_unsupported" {
		status = 422
	}
	if code == "rate_limited" {
		status = 429
	}
	write(w, status, map[string]any{"error": map[string]any{"code": "remote_" + code, "message": "Remote source operation could not complete (" + code + "). Check the owner source settings.", "retryable": code == "offline" || code == "rate_limited", "serverTime": time.Now().UTC().Format(time.RFC3339Nano)}})
}
func (d Dependencies) remoteSourceRoutes(mux *http.ServeMux) {
	if d.RemoteSources == nil {
		return
	}
	d.httpHandle(mux, "GET /v1/storage/sources", func(w http.ResponseWriter, r *http.Request) {
		_, scope, err := d.storageOwner(r)
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		if r.URL.RawQuery != "" {
			remoteSourceFailure(w, mounts.ErrInvalid)
			return
		}
		rows, err := d.RemoteSources.List(r.Context())
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		if _, _, err = d.storageOwner(r); err != nil {
			remoteSourceFailure(w, err)
			return
		}
		write(w, 200, map[string]any{"scope": scope, "sources": rows, "mountSupported": mounts.MountSupported()})
	})
	d.httpHandle(mux, "GET /v1/storage/source-operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		actor, _ := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID})
		receipt, err := d.RemoteSources.Receipt(r.Context(), string(actor), r.PathValue("id"))
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		if _, _, err = d.storageOwner(r); err != nil {
			remoteSourceFailure(w, err)
			return
		}
		write(w, 200, map[string]any{"scope": scope, "receipt": receipt})
	})
	for _, route := range []string{"POST /v1/storage/sources/webdav", "PUT /v1/storage/sources/webdav/{id}", "DELETE /v1/storage/sources/webdav/{id}"} {
		d.httpHandle(mux, route, func(w http.ResponseWriter, r *http.Request) {
			p, scope, err := d.storageOwner(r)
			if err != nil {
				remoteSourceFailure(w, err)
				return
			}
			if r.URL.RawQuery != "" {
				remoteSourceFailure(w, mounts.ErrInvalid)
				return
			}
			actor, _ := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID})
			var receipt remotesources.Receipt
			authorize := func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") }
			if r.Method == http.MethodDelete {
				var body struct {
					OperationID        string `json:"operationId"`
					ExpectedGeneration int64  `json:"expectedGeneration"`
				}
				if err = decodeLegacyRevision(w, r, &body); err == nil {
					receipt, err = d.RemoteSources.RemoveDAV(r.Context(), string(actor), r.PathValue("id"), body.OperationID, body.ExpectedGeneration, authorize)
				}
			} else {
				var body remotesources.DAVInput
				if err = decodeLegacyRevision(w, r, &body); err == nil {
					receipt, err = d.RemoteSources.ConfigureDAV(r.Context(), string(actor), r.PathValue("id"), body, authorize)
				}
			}
			if err != nil {
				remoteSourceFailure(w, err)
				return
			}
			if _, _, err = d.storageOwner(r); err != nil {
				remoteSourceFailure(w, err)
				return
			}
			write(w, 200, map[string]any{"scope": scope, "receipt": receipt})
		})
	}
	d.httpHandle(mux, "GET /v1/libraries/{id}/network-roots", func(w http.ResponseWriter, r *http.Request) {
		_, scope, err := d.storageOwner(r)
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		var library string
		if err = d.DB.QueryRowContext(r.Context(), `SELECT id FROM libraries WHERE id=?`, r.PathValue("id")).Scan(&library); err != nil {
			remoteSourceFailure(w, err)
			return
		}
		var revision int64
		raw := "[]"
		err = d.DB.QueryRowContext(r.Context(), `SELECT revision,approvals_json FROM library_network_policy WHERE library_id=?`, library).Scan(&revision, &raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			remoteSourceFailure(w, err)
			return
		}
		var approvals []struct{ Root string }
		if json.Unmarshal([]byte(raw), &approvals) != nil {
			remoteSourceFailure(w, errors.New("invalid network policy"))
			return
		}
		origins := []string{}
		seen := map[string]bool{}
		for _, approval := range approvals {
			u, e := url.Parse(approval.Root)
			if e != nil {
				remoteSourceFailure(w, e)
				return
			}
			origin := u.Scheme + "://" + u.Host
			if !seen[origin] {
				origins = append(origins, origin)
				seen[origin] = true
			}
		}
		if _, _, err = d.storageOwner(r); err != nil {
			remoteSourceFailure(w, err)
			return
		}
		write(w, 200, map[string]any{"scope": scope, "policy": map[string]any{"libraryId": library, "revision": revision, "origins": origins}})
	})
	d.httpHandle(mux, "GET /v1/libraries/{id}/strm-analysis-policy", func(w http.ResponseWriter, r *http.Request) {
		_, scope, err := d.storageOwner(r)
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		policy, err := d.RemoteSources.AnalysisPolicy(r.Context(), r.PathValue("id"))
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		if _, _, err = d.storageOwner(r); err != nil {
			remoteSourceFailure(w, err)
			return
		}
		write(w, 200, map[string]any{"scope": scope, "policy": policy})
	})
	d.httpHandle(mux, "PUT /v1/libraries/{id}/strm-analysis-policy", func(w http.ResponseWriter, r *http.Request) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		var body struct {
			Enabled          bool  `json:"enabled"`
			ExpectedRevision int64 `json:"expectedRevision"`
		}
		if err = decodeLegacyRevision(w, r, &body); err != nil {
			remoteSourceFailure(w, err)
			return
		}
		policy, err := d.RemoteSources.SetAnalysisPolicy(r.Context(), r.PathValue("id"), body.Enabled, body.ExpectedRevision, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, r.PathValue("id")) })
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		if _, _, err = d.storageOwner(r); err != nil {
			remoteSourceFailure(w, err)
			return
		}
		write(w, 200, map[string]any{"scope": scope, "policy": policy})
	})
	d.httpHandle(mux, "GET /v1/items/{id}/source-availability", func(w http.ResponseWriter, r *http.Request) {
		p, err := d.principal(r)
		if err != nil {
			failure(w, err)
			return
		}
		if err = d.itemAccess(r.Context(), p, r.PathValue("id")); err != nil {
			failure(w, err)
			return
		}
		state, err := d.sourceAvailability(r.Context(), r.PathValue("id"))
		if err != nil {
			remoteSourceFailure(w, err)
			return
		}
		p, err = d.principal(r)
		if err == nil {
			err = d.itemAccess(r.Context(), p, r.PathValue("id"))
		}
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]any{"itemId": r.PathValue("id"), "state": state})
	})
}
