package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

type ReadingSavedGroupRequest ReadingSavedRequest

func (ReadingSavedGroupRequest) JSONSchema() map[string]any {
	return (ReadingSavedRequest{}).JSONSchema()
}

type ReadingSavedGroupResponse struct {
	ServerID    string `json:"serverId"`
	ViewerFence string `json:"viewerFence"`
	catalog.ReadingSavedGroupState
}

func (ReadingSavedGroupResponse) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"serverId", "viewerFence", "libraryId", "groupKind", "seriesId", "listed", "revision"}, "properties": map[string]any{
		"serverId": annotationString(1, 128), "viewerFence": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$", "minLength": 64, "maxLength": 64},
		"libraryId": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]{1,128}$"}, "groupKind": (catalog.ReadingSavedGroupKind("")).JSONSchema(), "seriesId": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{43}$", "minLength": 43, "maxLength": 43},
		"listed": map[string]any{"type": "boolean"}, "revision": annotationInteger(9007199254740991), "referenceId": annotationString(1, 128), "operationId": annotationString(1, 128)}}
}

type ReadingSavedGroupRemovalRequest struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

func (ReadingSavedGroupRemovalRequest) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operationId", "expectedRevision"}, "properties": map[string]any{"operationId": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]{1,128}$"}, "expectedRevision": map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991}}}
}

type ReadingSavedGroupRemovalResponse struct {
	ServerID string `json:"serverId"`
	catalog.ReadingSavedGroupRemoval
}

func (ReadingSavedGroupRemovalResponse) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"serverId", "referenceId", "revision", "removed", "operationId"}, "properties": map[string]any{"serverId": annotationString(1, 128), "referenceId": annotationString(1, 128), "revision": map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991}, "removed": map[string]any{"type": "boolean", "enum": []bool{true}}, "operationId": annotationString(1, 128)}}
}

func (d Dependencies) readingSavedGroupPrincipalTx(ctx context.Context, tx *sql.Tx, p identity.Principal) (identity.Principal, error) {
	if d.Identity == nil {
		return identity.Principal{}, identity.ErrUnauthorized
	}
	if !identity.CanUsePersonalState(p) || p.ServerID != d.Identity.ID() {
		return identity.Principal{}, identity.ErrForbidden
	}
	live, err := d.Identity.PrincipalByDigestTx(ctx, tx, p.Hash)
	if err != nil {
		return live, err
	}
	if live.Viewer != p.Viewer {
		return live, identity.ErrUnauthorized
	}
	if !identity.CanUsePersonalState(live) || live.ServerID != d.Identity.ID() {
		return live, identity.ErrForbidden
	}
	if err = live.PermitsRequest(ctx); err != nil {
		return live, err
	}
	return live, nil
}

func (d Dependencies) readingSavedGroupResolve(ctx context.Context, tx *sql.Tx, p identity.Principal, target catalog.ReadingSavedGroupTarget) (catalog.ReadingSavedGroupProof, error) {
	live, err := d.readingSavedGroupPrincipalTx(ctx, tx, p)
	if err != nil {
		return catalog.ReadingSavedGroupProof{}, err
	}
	scope, err := d.readingSeriesScopeTx(ctx, tx, live, target.LibraryID)
	if err != nil {
		return catalog.ReadingSavedGroupProof{}, err
	}
	restrictions, err := contentaccess.RestrictionsTx(ctx, tx, live.Viewer)
	if err != nil {
		return catalog.ReadingSavedGroupProof{}, err
	}
	viewer := catalog.Viewer{Profile: identity.PersonalKey(live.Viewer), Fence: scope.Fence, Libraries: []string{target.LibraryID}, Restrictions: restrictions}
	group, err := catalog.ResolveReadingSavedGroupTx(ctx, tx, viewer, target, d.readingService().Mobi != nil)
	return group.Proof, err
}

func readingSavedGroupTarget(r *http.Request) catalog.ReadingSavedGroupTarget {
	return catalog.ReadingSavedGroupTarget{LibraryID: r.PathValue("libraryId"), GroupKind: catalog.ReadingSavedGroupKind(r.PathValue("groupKind")), SeriesID: r.PathValue("seriesId")}
}
func (d Dependencies) readingSavedGroup(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	state, proof, err := d.Catalog.WithContext(r.Context()).ReadingSavedGroup(p, d.Identity.ID(), readingSavedGroupTarget(r), d.readingSavedGroupResolve)
	if err != nil {
		readingFailure(w, err)
		return
	}
	write(w, 200, ReadingSavedGroupResponse{ServerID: d.Identity.ID(), ViewerFence: proof.ViewerFence, ReadingSavedGroupState: state})
}
func (d Dependencies) readingSetSavedGroup(rate *personalLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := d.readingPrincipal(w, r)
		if !ok {
			return
		}
		if !identity.CanUsePersonalState(p) {
			readingFailure(w, identity.ErrForbidden)
			return
		}
		if !rate.allow(viewerScope(p)) {
			w.Header().Set("Retry-After", "60")
			policyError(w, "rate_limited")
			return
		}
		var in ReadingSavedGroupRequest
		if err := decodeSavedRevision(w, r, &in); err != nil {
			readingFailure(w, err)
			return
		}
		if in.Listed == nil {
			readingFailure(w, reading.ErrInvalid)
			return
		}
		target := readingSavedGroupTarget(r)
		state, proof, err := d.Catalog.WithContext(r.Context()).SetReadingSavedGroup(p, d.Identity.ID(), target, in.OperationID, in.ExpectedRevision, *in.Listed, d.readingSavedGroupResolve)
		if err != nil {
			failure(w, err)
			return
		}
		_, after, err := d.Catalog.WithContext(r.Context()).ReadingSavedGroup(p, d.Identity.ID(), target, d.readingSavedGroupResolve)
		_, _, protocol := usermutation.RequestFrom(r.Context())
		if protocol && (errors.Is(err, sql.ErrNoRows) || errors.Is(err, catalog.ErrReadingProjectionChanged)) {
			// The private reference was already accepted. A subsequently missing
			// target is current state, not permission to apply the old edit again.
			err = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
				live, e := d.readingSavedGroupPrincipalTx(ctx, dbwork.Snapshot(ctx), p)
				if e != nil {
					return e
				}
				scope, e := d.readingSeriesScopeTx(ctx, dbwork.Snapshot(ctx), live, target.LibraryID)
				after.ViewerFence = scope.Fence
				return e
			})
		}
		if err != nil {
			readingFailure(w, err)
			return
		}
		if (!protocol || proof.ViewerFence != "") && after.ViewerFence != proof.ViewerFence {
			failure(w, catalog.ErrStaleContinuation)
			return
		}
		write(w, 200, ReadingSavedGroupResponse{ServerID: d.Identity.ID(), ViewerFence: after.ViewerFence, ReadingSavedGroupState: state})
	}
}

func (d Dependencies) readingRemoveSavedGroup(rate *personalLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := d.readingPrincipal(w, r)
		if !ok {
			return
		}
		if !identity.CanUsePersonalState(p) {
			readingFailure(w, identity.ErrForbidden)
			return
		}
		if !rate.allow(viewerScope(p)) {
			w.Header().Set("Retry-After", "60")
			policyError(w, "rate_limited")
			return
		}
		var in ReadingSavedGroupRemovalRequest
		if err := decodeSavedRevision(w, r, &in); err != nil {
			readingFailure(w, err)
			return
		}
		applied, err := d.Catalog.WithContext(r.Context()).RemoveReadingSavedReference(p, d.Identity.ID(), r.PathValue("referenceId"), in.OperationID, in.ExpectedRevision, func(ctx context.Context, tx *sql.Tx, original identity.Principal) error {
			_, err := d.readingSavedGroupPrincipalTx(ctx, tx, original)
			return err
		})
		if err != nil {
			failure(w, err)
			return
		}
		// This postproof is deliberately reference-only too. Hidden/missing or
		// unready targets never enter a metadata or library expansion here.
		err = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
			tx := dbwork.Snapshot(ctx)
			live, err := d.readingSavedGroupPrincipalTx(ctx, tx, p)
			if err != nil {
				return err
			}
			var found int
			return tx.QueryRowContext(ctx, `SELECT 1 FROM reading_saved_groups WHERE reference_id=? AND owner_key=? AND server_id=?`, r.PathValue("referenceId"), identity.PersonalKey(live.Viewer), d.Identity.ID()).Scan(&found)
		})
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, ReadingSavedGroupRemovalResponse{ServerID: d.Identity.ID(), ReadingSavedGroupRemoval: applied})
	}
}

func registerReadingSavedGroupHTTPContracts(registry *apikit.Registry) error {
	m := apikit.Metadata{Audience: apikit.Clients, ID: "get_reading_group_my_list", Method: "GET", Path: "/v1/libraries/{libraryId}/reading/groups/{groupKind}/{seriesId}/my-list", Summary: "Read one current Reading group's dynamic My List reference", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "stale_continuation", "internal"}}
	if err := apikit.RegisterHTTP[struct{}, ReadingSavedGroupResponse](registry, m); err != nil {
		return err
	}
	m.ID = "put_reading_group_my_list"
	m.Method = "PUT"
	m.Summary = "Change one dynamic Reading group reference without saving its members"
	m.Requires = apikit.ScopePlayback
	m.Lane = apikit.Default
	m.Admission = laneDefault
	m.BodyLimit = 4096
	m.Errors = append(m.Errors, "rate_limited", "personal_state_conflict", "idempotency_key_reused", "operation_expired")
	if err := apikit.RegisterHTTP[ReadingSavedGroupRequest, ReadingSavedGroupResponse](registry, m); err != nil {
		return err
	}
	m.ID = "delete_reading_saved_group_reference"
	m.Method = "DELETE"
	m.Path = "/v1/reading/saved-groups/{referenceId}"
	m.Summary = "Remove only this profile's exact private Saved reference without resolving its target"
	return apikit.RegisterHTTP[ReadingSavedGroupRemovalRequest, ReadingSavedGroupRemovalResponse](registry, m)
}
