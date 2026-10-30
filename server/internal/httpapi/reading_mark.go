package httpapi

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

type ReadingMarkRequest struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Read             *bool  `json:"read"`
}
type ReadingMarkResponse struct {
	ServerID    string `json:"serverId"`
	ViewerFence string `json:"viewerFence"`
	ItemID      string `json:"itemId"`
	Read        bool   `json:"read"`
	Revision    int64  `json:"revision"`
	OperationID string `json:"operationId,omitempty"`
}

func (ReadingMarkRequest) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operationId", "expectedRevision", "read"}, "properties": map[string]any{
		"operationId":      map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]{1,128}$"},
		"expectedRevision": annotationInteger(9007199254740991), "read": map[string]any{"type": "boolean"}}}
}
func (ReadingMarkResponse) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"serverId", "viewerFence", "itemId", "read", "revision"}, "properties": map[string]any{
		"serverId": annotationString(1, 128), "viewerFence": annotationString(1, 128), "itemId": annotationIDSchema(), "read": map[string]any{"type": "boolean"},
		"revision": annotationInteger(9007199254740991), "operationId": annotationString(1, 128)}}
}

func (d Dependencies) readingMarkAuthorize(ctx context.Context, p identity.Principal, item string, fence *string) func(*sql.Tx) error {
	return func(tx *sql.Tx) error {
		if !identity.CanUsePersonalState(p) {
			return identity.ErrForbidden
		}
		if d.Identity == nil {
			return identity.ErrUnauthorized
		}
		live, err := d.Identity.PrincipalByDigestTx(ctx, tx, p.Hash)
		if err != nil {
			return err
		}
		if live.Viewer != p.Viewer {
			return identity.ErrUnauthorized
		}
		if !identity.CanUsePersonalState(live) {
			return identity.ErrForbidden
		}
		if err = live.PermitsRequest(ctx); err != nil {
			return err
		}
		// Reject malformed public IDs before the strict SQL adapter sees them.
		// This is the same concealed absence as a well-formed missing target;
		// original-principal eligibility/readmission still precedes this check.
		if _, valid := entityid.Decode(item); !valid {
			return sql.ErrNoRows
		}
		var library string
		if err := tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?) AND e.retired=0 AND cl.retired=0`, item).Scan(&library); err != nil {
			return err
		}
		scope, err := d.readingSeriesScopeTx(ctx, tx, p, library)
		if err != nil {
			return err
		}
		service := d.readingService()
		if service.Authorize == nil {
			return identity.ErrForbidden
		}
		if err = service.Authorize(ctx, tx, p, library, item); err != nil {
			return err
		}
		*fence = scope.Fence
		return nil
	}
}

func (d Dependencies) readingMarkView(ctx context.Context, p identity.Principal, item string) (ReadingMarkResponse, error) {
	if !identity.CanUsePersonalState(p) {
		return ReadingMarkResponse{}, identity.ErrForbidden
	}
	var fence string
	state, err := d.Catalog.WithContext(ctx).ReadingMark(identity.PersonalKey(p.Viewer), item, d.readingMarkAuthorize(ctx, p, item, &fence))
	if err != nil {
		return ReadingMarkResponse{}, err
	}
	return ReadingMarkResponse{ServerID: d.Identity.ID(), ViewerFence: fence, ItemID: state.ItemID, Read: state.Read, Revision: state.Revision}, nil
}
func (d Dependencies) readingMark(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	out, err := d.readingMarkView(r.Context(), p, r.PathValue("itemId"))
	if err != nil {
		readingFailure(w, err)
		return
	}
	write(w, 200, out)
}
func (d Dependencies) readingSetMark(rate *personalLimiter) http.HandlerFunc {
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
		var in ReadingMarkRequest
		if err := decodeSavedRevision(w, r, &in); err != nil {
			readingFailure(w, err)
			return
		}
		if in.Read == nil {
			readingFailure(w, reading.ErrInvalid)
			return
		}
		var fence string
		applied, err := d.Catalog.WithContext(r.Context()).SetReadingMark(p.Authority+":"+p.AccountID, identity.PersonalKey(p.Viewer), r.PathValue("itemId"), in.OperationID, in.ExpectedRevision, *in.Read, d.readingMarkAuthorize(r.Context(), p, r.PathValue("itemId"), &fence))
		if err != nil {
			failure(w, err)
			return
		}
		out, err := d.readingMarkView(r.Context(), p, r.PathValue("itemId"))
		if err != nil {
			readingFailure(w, err)
			return
		}
		if out.ViewerFence != fence {
			failure(w, catalog.ErrStaleContinuation)
			return
		}
		// Fresh state proves admission only; the old operation retains its stored result.
		out.ItemID, out.Read, out.Revision = applied.ItemID, applied.Read, applied.Revision
		out.OperationID = in.OperationID
		write(w, 200, out)
	}
}
func registerReadingMarkHTTPContracts(registry *apikit.Registry) error {
	m := apikit.Metadata{Audience: apikit.Clients, ID: "get_items_itemId_reading_mark", Method: "GET", Path: "/v1/items/{itemId}/reading/mark", Summary: "Read this profile's explicit Reading completion mark", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}
	if err := apikit.RegisterHTTP[struct{}, ReadingMarkResponse](registry, m); err != nil {
		return err
	}
	m.ID = "put_items_itemId_reading_mark"
	m.Method = "PUT"
	m.Summary = "Set an explicit Reading completion mark without changing its location"
	m.Requires = apikit.ScopePlayback
	m.Lane = apikit.Default
	m.Admission = laneDefault
	m.BodyLimit = 4096
	m.Errors = append(m.Errors, "rate_limited", "personal_state_conflict", "personal_needs_resolution", "personal_needs_review", "operation_expired", "idempotency_key_reused", "stale_continuation")
	if err := apikit.RegisterHTTP[ReadingMarkRequest, ReadingMarkResponse](registry, m); err != nil {
		return err
	}
	return registerReadingMarkAllHTTPContracts(registry)
}
