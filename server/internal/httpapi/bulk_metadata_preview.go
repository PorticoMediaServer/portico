package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"unsafe"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type bulkMetadataPreviewPreparing struct {
	Kind         string `json:"kind"`
	State        string `json:"state"`
	QueryID      string `json:"queryId"`
	RetryAfterMs int    `json:"retryAfterMs"`
}

func (bulkMetadataPreviewPreparing) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind", "state", "queryId", "retryAfterMs"}, "properties": map[string]any{
		"kind":         map[string]any{"type": "string", "enum": []string{"metadata-preview-preparing"}},
		"state":        map[string]any{"type": "string", "enum": []string{"preparing"}},
		"queryId":      map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"retryAfterMs": map[string]any{"type": "integer", "enum": []int{1000}},
	}}
}

type bulkMetadataPreviewResponse struct {
	Ready     *metadata.BulkMetadataPreview
	Preparing *bulkMetadataPreviewPreparing
}

func (v bulkMetadataPreviewResponse) MarshalJSON() ([]byte, error) {
	if (v.Ready == nil) == (v.Preparing == nil) {
		return nil, readprep.ErrInvalid
	}
	if v.Ready != nil {
		return json.Marshal(v.Ready)
	}
	return json.Marshal(v.Preparing)
}
func (bulkMetadataPreviewResponse) JSONSchema() map[string]any {
	return map[string]any{"oneOf": []any{bulkMetadataPreviewReadySchema(), (bulkMetadataPreviewPreparing{}).JSONSchema()}}
}
func (bulkMetadataPreviewResponse) JSONSchemaForStatus(status int) map[string]any {
	if status == 200 {
		return bulkMetadataPreviewReadySchema()
	}
	if status == 202 {
		return (bulkMetadataPreviewPreparing{}).JSONSchema()
	}
	return nil
}
func bulkMetadataPreviewReadySchema() map[string]any {
	shape, err := apikit.Schema(reflect.TypeFor[metadata.BulkMetadataPreview]())
	if err != nil {
		panic(err)
	}
	return shape
}

const bulkMetadataPreviewQueryPath = "/v1/metadata/bulk/preview-queries/{queryId}"

// Every old preview input uses the same memory-only preparation store. The
// domain response remains exact up to K and explicitly lower-bounded above K.
func registerBulkMetadataPreview(registry *apikit.Registry, d Dependencies) {
	failures := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "stale_continuation", "rate_limited", "internal", "timeout", "unsupported_media_type", "body_too_large"}
	headers := []apikit.ResponseHeader{
		{Status: 200, Name: "Cache-Control", Description: "Metadata previews are private and never cached by HTTP.", Required: true},
		{Status: 202, Name: "Cache-Control", Description: "Private preparing handles are never cached.", Required: true},
		{Status: 202, Name: "Retry-After", Description: "Minimum delay before polling again.", Required: true},
		{Status: 202, Name: "Location", Description: "This caller's metadata preview preparation.", Required: true},
	}

	post := apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "post_metadata_bulk_preview", Method: "POST", Path: "/v1/metadata/bulk/preview", Summary: "Prepare shared and mixed metadata for an authorized selection", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Cost: apikit.PageSized, BodyLimit: 524288, Status: 200, SuccessStatuses: []int{202}, ResponseHeaders: headers, Errors: failures}
	v1Route(registry, post, func(ctx context.Context, r *http.Request, in catalog.BulkMetadataPreviewRequest) (bulkMetadataPreviewResponse, error) {
		apikit.SetHeader(ctx, "Cache-Control", "no-store")
		out, err := d.startBulkMetadataPreview(ctx, r, in, func(release func()) bool { return apikit.AfterResponse(ctx, release) })
		if out.Preparing != nil {
			apikit.SetStatus(ctx, 202)
			apikit.SetHeader(ctx, "Retry-After", "1")
			apikit.SetHeader(ctx, "Location", strings.Replace(bulkMetadataPreviewQueryPath, "{queryId}", out.Preparing.QueryID, 1))
		}
		return out, err
	})

	get := apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_metadata_bulk_preview_query", Method: "GET", Path: bulkMetadataPreviewQueryPath, Summary: "Read a metadata preview preparation", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Cost: apikit.Constant, Status: 200, SuccessStatuses: []int{202}, ResponseHeaders: headers, Errors: failures}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, bulkMetadataPreviewResponse]{Metadata: get, Serve: d.bulkMetadataPreviewHandler}); err != nil {
		panic(err)
	}
	cancel := apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "delete_metadata_bulk_preview_query", Method: "DELETE", Path: bulkMetadataPreviewQueryPath, Summary: "Release this caller's metadata preview demand", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Cost: apikit.Constant, Status: 204, NoResponseBody: true, Errors: failures}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, noBody]{Metadata: cancel, Serve: d.bulkMetadataPreviewHandler}); err != nil {
		panic(err)
	}
}

func (d Dependencies) startBulkMetadataPreview(ctx context.Context, r *http.Request, in catalog.BulkMetadataPreviewRequest, hold func(func()) bool) (out bulkMetadataPreviewResponse, err error) {
	defer func() { err = bulkMetadataPreviewError(err) }()
	r = r.WithContext(ctx)

	if r.URL.RawQuery != "" {
		return out, metadata.ErrRepairInput
	}
	if err = in.ValidateSelection(); err != nil {
		return out, err
	}
	if d.Metadata == nil || d.Catalog == nil || d.Identity == nil {
		return out, errMetadataUnavailable()
	}
	p, caller, fence, err := d.bulkMetadataPreviewScope(r)
	if err != nil {
		return out, err
	}
	if small, handled, e := d.smallBulkMetadataPreview(ctx, r, in, p, fence, hold); handled {
		return small, e
	}
	store := d.Catalog.ReadPreparations()
	if store == nil {
		return out, readprep.ErrClosed
	}
	raw, err := json.Marshal(bulkMetadataPreviewInput{Request: in, Fence: fence})
	if err != nil {
		return out, err
	}
	// Deserialize an owned descriptor for the background closure; do not capture
	// the request's body, HTTP request, cancellation context or decoded aliases.
	var input bulkMetadataPreviewInput
	if err = json.Unmarshal(raw, &input); err != nil {
		return out, err
	}
	access := d.bulkMetadataPreviewAccess(p, input, len(raw))
	key, plan, err := d.Metadata.PlanBulkPreview(ctx, string(raw), access)
	if err != nil {
		return out, err
	}
	handle, err := store.Submit(ctx, caller, key, plan)
	if err != nil {
		return out, err
	}
	out, err = d.bulkMetadataPreviewDeliver(ctx, r, p, caller, fence, string(handle), hold)
	if err != nil {
		_ = store.Cancel(context.WithoutCancel(ctx), caller, handle)
	}
	return out, err
}

type bulkMetadataPreviewInput struct {
	Request catalog.BulkMetadataPreviewRequest `json:"request"`
	Fence   string                             `json:"fence"`
}

func (d Dependencies) bulkMetadataPreviewScope(r *http.Request) (identity.Principal, readprep.Caller, string, error) {
	p, err := d.manualMetadataAdministrator(r.Context(), r)
	if err != nil {
		return p, "", "", err
	}
	if d.Metadata == nil || d.Catalog == nil || d.Identity == nil {
		return p, "", "", errMetadataUnavailable()
	}
	_, fence, err := d.homeScopeFor(r, p)
	if err != nil {
		return p, "", "", err
	}
	raw, err := json.Marshal([]any{"metadata-preview", p.ServerID, p.Hash, p.Authority, p.AccountID, p.ProfileID, p.Role, p.Epoch, p.Grant, fence})
	return p, readprep.Caller(raw), fence, err
}

func (d Dependencies) bulkMetadataPreviewAccess(p identity.Principal, in bulkMetadataPreviewInput, descriptorBytes int) metadata.BulkPreviewPreparationAccess {
	expected := strings.Clone(in.Fence)
	authorize := func(ctx context.Context, tx *sql.Tx) error { return d.manualMetadataAuthorize(ctx, tx, p) }
	local := false
	if in.Request.Selector != nil && in.Request.Selector.Query != nil {
		var node *catalog.BrowseNode
		if json.Unmarshal(in.Request.Selector.Query.Filter, &node) == nil {
			local = catalog.BrowseNeedsLocalSearch(node)
		}
	}
	scope := func(ctx context.Context) (catalog.Viewer, error) {
		// Scope is reconstructed with a bare request because no HTTP headers or
		// request cancellation are authority for retained background demand.
		r := (&http.Request{}).WithContext(ctx)
		libraries, fence, err := d.homeScopeFor(r, p)
		if err != nil {
			return catalog.Viewer{}, err
		}
		if fence != expected {
			return catalog.Viewer{}, readprep.ErrStale
		}
		return d.catalogViewer(r, p, libraries, fence)
	}
	source := func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		viewer, err := scope(ctx)
		if err != nil {
			return catalog.BulkMetadataSelectionPage{}, err
		}
		if local {
			return d.Catalog.BulkMetadataLocalSelectionBasisTx(ctx, tx, viewer, in.Request)
		}
		return d.Catalog.BulkMetadataSelectionUnitTx(ctx, tx, viewer, in.Request, after)
	}
	access := metadata.BulkPreviewPreparationAccess{Source: source, Authorize: authorize, CapturedBytes: 8*int64(descriptorBytes) + int64(unsafe.Sizeof(d)+unsafe.Sizeof(p)) + 16384}
	if local {
		access.NewWalk = func(ctx context.Context, tx *sql.Tx) (*catalog.BulkMetadataSelectionWalk, error) {
			viewer, err := scope(ctx)
			if err != nil {
				return nil, err
			}
			return d.Catalog.NewBulkMetadataSelectionWalkTx(ctx, tx, viewer, in.Request)
		}
	}
	return access
}

func (d Dependencies) bulkMetadataPreviewDeliver(ctx context.Context, r *http.Request, p identity.Principal, caller readprep.Caller, fence, queryID string, hold func(func()) bool) (out bulkMetadataPreviewResponse, err error) {
	store := d.Catalog.ReadPreparations()
	if store == nil {
		return out, readprep.ErrClosed
	}
	result, err := store.Poll(ctx, caller, readprep.Handle(queryID))
	if err != nil {
		return out, err
	}
	// The raw handler owns the lifetime through serialization/transport.
	// Hold every successful Poll, including preparing, through actual Write.
	if !hold(result.Release) {
		result.Release()
		return out, readprep.ErrStale
	}
	if result.Key.Namespace != metadata.BulkPreviewPreparationNamespace {
		return out, readprep.ErrStale
	}
	if err = result.ResizeDelivery(8*int64(len(result.Payload)+len(result.Key.Canonical)) + 65536); err != nil {
		return out, err
	}
	var input bulkMetadataPreviewInput
	if err = json.Unmarshal([]byte(result.Key.Canonical), &input); err != nil {
		return out, err
	}
	if input.Fence != fence {
		return out, readprep.ErrStale
	}
	access := d.bulkMetadataPreviewAccess(p, input, len(result.Key.Canonical))
	if err = d.Metadata.CheckBulkPreviewBasis(ctx, result.Basis, access); err != nil {
		return out, err
	}
	if result.State == readprep.Preparing {
		out.Preparing = &bulkMetadataPreviewPreparing{Kind: "metadata-preview-preparing", State: "preparing", QueryID: queryID, RetryAfterMs: 1000}
	} else {
		var value metadata.BulkMetadataPreview
		if err = json.Unmarshal(result.Payload, &value); err != nil {
			return out, err
		}
		value.ServerID, value.ViewerFence = d.Identity.ID(), fence
		out.Ready = &value
	}
	// Recheck cached and freshly completed responses immediately before returning
	// ownership to the serializer. No payload, handle or old principal is a grant.
	if err = d.Metadata.CheckBulkPreviewBasis(ctx, result.Basis, access); err != nil {
		return bulkMetadataPreviewResponse{}, err
	}
	return out, nil
}

func bulkMetadataPreviewError(err error, _ ...bool) error {
	switch {
	case errors.Is(err, readprep.ErrStale), errors.Is(err, catalog.ErrStaleContinuation), errors.Is(err, catalog.ErrSelectionBatchStale), errors.Is(err, catalog.ErrReadingProjectionChanged):
		return &apierror.Error{Code: "stale_continuation", Cause: err}
	case errors.Is(err, readprep.ErrCapacity), errors.Is(err, readprep.ErrClosed), errors.Is(err, readprep.ErrUncached), errors.Is(err, catalog.ErrSelectionBatchCapacity):
		return &apierror.Error{Code: "rate_limited", RetryAfterSeconds: 1, Cause: err}
	case errors.Is(err, metadata.ErrRepairInput), errors.Is(err, readprep.ErrInvalid), errors.Is(err, catalog.ErrSelectionBatchUnsupported):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	default:
		return bulkError(metadataAttentionError(err))
	}
}

// A raw handler retains leases through its own serialization and actual Write.
// It also avoids typed conditional-GET ETags on private preparing resources.
func (d Dependencies) bulkMetadataPreviewHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(err error) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), v1Error(bulkMetadataPreviewError(err)))
	}
	if r.URL.RawQuery != "" || len(r.PathValue("queryId")) > 128 {
		fail(metadata.ErrRepairInput)
		return
	}
	var releases []func()
	defer func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}()
	hold := func(release func()) bool { releases = append(releases, release); return true }
	var out bulkMetadataPreviewResponse
	var err error
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		fail(metadata.ErrRepairInput)
		return
	}
	p, caller, fence, e := d.bulkMetadataPreviewScope(r)
	if e != nil {
		fail(e)
		return
	}
	if r.Method == http.MethodDelete {
		store := d.Catalog.ReadPreparations()
		if store == nil {
			fail(readprep.ErrClosed)
			return
		}
		if e = store.CancelNamespace(r.Context(), caller, readprep.Handle(r.PathValue("queryId")), metadata.BulkPreviewPreparationNamespace); e != nil {
			fail(e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	out, err = d.bulkMetadataPreviewDeliver(r.Context(), r, p, caller, fence, r.PathValue("queryId"), hold)
	if err != nil {
		fail(err)
		return
	}
	status := 200
	if out.Preparing != nil {
		status = 202
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Location", strings.Replace(bulkMetadataPreviewQueryPath, "{queryId}", out.Preparing.QueryID, 1))
	}
	write(w, status, out)
}
