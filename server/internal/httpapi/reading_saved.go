package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

type ReadingSavedRequest struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Listed           *bool  `json:"listed"`
}
type ReadingSavedResponse struct {
	ServerID    string `json:"serverId"`
	ViewerFence string `json:"viewerFence"`
	ItemID      string `json:"itemId"`
	Listed      bool   `json:"listed"`
	Revision    int64  `json:"revision"`
	OperationID string `json:"operationId,omitempty"`
}

func (ReadingSavedRequest) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operationId", "expectedRevision", "listed"}, "properties": map[string]any{
		"operationId":      map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]{1,128}$"},
		"expectedRevision": annotationInteger(9007199254740991), "listed": map[string]any{"type": "boolean"}}}
}
func (ReadingSavedResponse) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"serverId", "viewerFence", "itemId", "listed", "revision"}, "properties": map[string]any{
		"serverId": annotationString(1, 128), "viewerFence": annotationString(1, 128), "itemId": annotationIDSchema(), "listed": map[string]any{"type": "boolean"},
		"revision": annotationInteger(9007199254740991), "operationId": annotationString(1, 128)}}
}

func (d Dependencies) readingSavedView(ctx context.Context, p identity.Principal, item string) (ReadingSavedResponse, error) {
	if !identity.CanUsePersonalState(p) {
		return ReadingSavedResponse{}, identity.ErrForbidden
	}
	var fence string
	state, err := d.Catalog.WithContext(ctx).ReadingSaved(identity.PersonalKey(p.Viewer), item, d.readingMarkAuthorize(ctx, p, item, &fence))
	if err != nil {
		return ReadingSavedResponse{}, err
	}
	return ReadingSavedResponse{ServerID: d.Identity.ID(), ViewerFence: fence, ItemID: state.ItemID, Listed: state.Listed, Revision: state.Revision}, nil
}
func (d Dependencies) readingSaved(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	out, err := d.readingSavedView(r.Context(), p, r.PathValue("itemId"))
	if err != nil {
		readingFailure(w, err)
		return
	}
	write(w, 200, out)
}
func (d Dependencies) readingSetSaved(rate *personalLimiter) http.HandlerFunc {
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
		var in ReadingSavedRequest
		if err := decodeSavedRevision(w, r, &in); err != nil {
			readingFailure(w, err)
			return
		}
		if in.Listed == nil {
			readingFailure(w, reading.ErrInvalid)
			return
		}
		var fence string
		applied, err := d.Catalog.WithContext(r.Context()).SetReadingSaved(p.Authority+":"+p.AccountID, identity.PersonalKey(p.Viewer), r.PathValue("itemId"), in.OperationID, in.ExpectedRevision, *in.Listed, d.readingMarkAuthorize(r.Context(), p, r.PathValue("itemId"), &fence))
		if err != nil {
			failure(w, err)
			return
		}
		out, err := d.readingSavedView(r.Context(), p, r.PathValue("itemId"))
		if err != nil {
			readingFailure(w, err)
			return
		}
		if out.ViewerFence != fence {
			failure(w, catalog.ErrStaleContinuation)
			return
		}
		// Fresh state proves admission only; the old operation retains its stored result.
		out.ItemID, out.Listed, out.Revision = applied.ItemID, applied.Listed, applied.Revision
		out.OperationID = in.OperationID
		write(w, 200, out)
	}
}
func registerReadingSavedHTTPContracts(registry *apikit.Registry) error {
	m := apikit.Metadata{Audience: apikit.Clients, ID: "get_items_itemId_reading_my_list", Method: "GET", Path: "/v1/items/{itemId}/reading/my-list", Summary: "Read this profile's Reading My List membership", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}
	if err := apikit.RegisterHTTP[struct{}, ReadingSavedResponse](registry, m); err != nil {
		return err
	}
	m.ID = "put_items_itemId_reading_my_list"
	m.Method = "PUT"
	m.Summary = "Set Reading My List membership without changing progress"
	m.Requires = apikit.ScopePlayback
	m.Lane = apikit.Default
	m.Admission = laneDefault
	m.BodyLimit = 4096
	m.Errors = append(m.Errors, "rate_limited", "personal_state_conflict", "personal_needs_resolution", "personal_needs_review", "operation_expired", "idempotency_key_reused", "stale_continuation")
	return apikit.RegisterHTTP[ReadingSavedRequest, ReadingSavedResponse](registry, m)
}
