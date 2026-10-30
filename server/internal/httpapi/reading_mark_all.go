package httpapi

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

type ReadingMarkAllRequest catalog.ReadingMarkAllCommand
type ReadingMarkAllProjection catalog.ReadingMarkAllScope
type ReadingMarkAllResponse catalog.ReadingMarkAllReceipt

func readingMarkAllPairSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"catalog", "viewer"}, "properties": map[string]any{"catalog": annotationInteger(9007199254740991), "viewer": annotationInteger(9007199254740991)}}
}
func (ReadingMarkAllRequest) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operationId", "viewerFence", "expectedRevision"}, "properties": map[string]any{
		"operationId": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$", "minLength": 1, "maxLength": 128},
		"viewerFence": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$", "minLength": 64, "maxLength": 64}, "expectedRevision": readingMarkAllPairSchema()}}
}
func (ReadingMarkAllProjection) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"serverId", "libraryId", "viewerFence", "revision", "count"}, "properties": map[string]any{
		"serverId": annotationString(1, 128), "libraryId": annotationString(1, 128), "viewerFence": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$", "minLength": 64, "maxLength": 64}, "revision": readingMarkAllPairSchema(), "count": annotationInteger(9007199254740991)}}
}
func (ReadingMarkAllResponse) JSONSchema() map[string]any {
	out := (ReadingMarkAllProjection{}).JSONSchema()
	out["required"] = append(out["required"].([]string), "operationId", "expectedRevision", "changed")
	properties := out["properties"].(map[string]any)
	properties["operationId"] = annotationString(1, 128)
	properties["expectedRevision"] = readingMarkAllPairSchema()
	properties["changed"] = annotationInteger(9007199254740991)
	return out
}

func (d Dependencies) readingMarkAllAuthorize(p identity.Principal, library string) catalog.ReadingMarkAllAuthorizer {
	return func(ctx context.Context, tx *sql.Tx) (catalog.Viewer, error) {
		live, err := d.readingSavedGroupPrincipalTx(ctx, tx, p)
		if err != nil {
			return catalog.Viewer{}, err
		}
		scope, err := d.readingSeriesScopeTx(ctx, tx, live, library)
		if err != nil {
			return catalog.Viewer{}, err
		}
		restrictions, err := contentaccess.RestrictionsTx(ctx, tx, live.Viewer)
		if err != nil {
			return catalog.Viewer{}, err
		}
		return catalog.Viewer{Profile: identity.PersonalKey(live.Viewer), Fence: scope.Fence, Libraries: []string{library}, Restrictions: restrictions}, nil
	}
}
func (d Dependencies) readingMarkAllProjection(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	library := r.PathValue("libraryId")
	out, err := d.Catalog.WithContext(r.Context()).ReadingMarkAllProjection(d.Identity.ID(), library, d.readingMarkAllAuthorize(p, library))
	if err != nil {
		readingFailure(w, err)
		return
	}
	write(w, 200, out)
}

// This endpoint has a pair CAS, not the scalar personal-item revision used by
// decodeSavedRevision. Every member must be explicitly present (including zero).
func decodeReadingMarkAll(w http.ResponseWriter, r *http.Request) (ReadingMarkAllRequest, error) {
	var wire struct {
		OperationID      string `json:"operationId"`
		ViewerFence      string `json:"viewerFence"`
		ExpectedRevision *struct {
			Catalog *int64 `json:"catalog"`
			Viewer  *int64 `json:"viewer"`
		} `json:"expectedRevision"`
	}
	if err := decode(w, r, &wire); err != nil {
		return ReadingMarkAllRequest{}, err
	}
	if wire.ExpectedRevision == nil || wire.ExpectedRevision.Catalog == nil || wire.ExpectedRevision.Viewer == nil {
		return ReadingMarkAllRequest{}, requestinput.New("explicit catalog and viewer revisions are required")
	}
	return ReadingMarkAllRequest{OperationID: wire.OperationID, ViewerFence: wire.ViewerFence, ExpectedRevision: catalog.ContentRevision{Catalog: *wire.ExpectedRevision.Catalog, Viewer: *wire.ExpectedRevision.Viewer}}, nil
}
func (d Dependencies) readingSetMarkAll(rate *personalLimiter) http.HandlerFunc {
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
		in, err := decodeReadingMarkAll(w, r)
		if err != nil {
			readingFailure(w, err)
			return
		}
		library := r.PathValue("libraryId")
		out, err := d.Catalog.WithContext(r.Context()).SetReadingMarkAll(d.Identity.ID(), library, catalog.ReadingMarkAllCommand(in), d.readingMarkAllAuthorize(p, library))
		if err != nil {
			readingFailure(w, err)
			return
		}
		write(w, 200, out)
	}
}
func registerReadingMarkAllHTTPContracts(registry *apikit.Registry) error {
	m := apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_libraryId_reading_mark_all", Method: "GET", Path: "/v1/libraries/{libraryId}/reading/mark-all", Summary: "Read this profile's ready visible Reading mark-all target", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "internal"}}
	if err := apikit.RegisterHTTP[struct{}, ReadingMarkAllProjection](registry, m); err != nil {
		return err
	}
	m.ID = "post_libraries_libraryId_reading_mark_all"
	m.Method = "POST"
	m.Summary = "Atomically mark every ready visible Reading title read for the original profile"
	m.Lane = apikit.Default
	m.Admission = laneDefault
	m.BodyLimit = 4096
	m.Errors = append(m.Errors, "rate_limited", "personal_state_conflict", "personal_needs_resolution", "operation_expired", "idempotency_key_reused")
	return apikit.RegisterHTTP[ReadingMarkAllRequest, ReadingMarkAllResponse](registry, m)
}
