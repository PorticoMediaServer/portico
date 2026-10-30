package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unsafe"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type browseSelectionQueryResponse struct {
	Ready     *catalog.BrowseSelectionDescriptor
	Preparing *catalog.BrowseSelectionPreparing
}

func (v browseSelectionQueryResponse) MarshalJSON() ([]byte, error) {
	if (v.Ready == nil) == (v.Preparing == nil) {
		return nil, readprep.ErrInvalid
	}
	if v.Ready != nil {
		return json.Marshal(variantBody{"ready", v.Ready})
	}
	return json.Marshal(v.Preparing)
}

func (browseSelectionQueryResponse) JSONSchema() map[string]any {
	return map[string]any{"oneOf": []any{browseSelectionReadySchema(), (catalog.BrowseSelectionPreparing{}).JSONSchema()}}
}
func (browseSelectionQueryResponse) JSONSchemaForStatus(status int) map[string]any {
	if status == 200 {
		return browseSelectionReadySchema()
	}
	if status == 202 {
		return (catalog.BrowseSelectionPreparing{}).JSONSchema()
	}
	return nil
}
func browseSelectionReadySchema() map[string]any {
	shape, err := apikit.Schema(reflect.TypeFor[catalog.BrowseSelectionDescriptor]())
	if err != nil {
		panic(err)
	}
	shape["additionalProperties"] = false
	selector := shape["properties"].(map[string]any)["selector"].(map[string]any)
	query := selector["properties"].(map[string]any)["query"].(map[string]any)
	required, _ := query["required"].([]string)
	query["required"] = append(required, "selectionBasis")
	return withVariantTag(shape, "ready")
}

func registerBrowseSelectionQueryRoutes(registry *apikit.Registry, d Dependencies) error {
	path := "/v1/libraries/{id}/browse-selection-queries/{queryId}"
	failures := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "rate_limited", "internal"}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, browseSelectionQueryResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients,
		ID: "get_library_browse_selection_query", Method: "GET", Path: path, Summary: "Read an exact whole-view selection preparation", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, SuccessStatuses: []int{202}, Errors: failures,
		ResponseHeaders: []apikit.ResponseHeader{
			{Status: 200, Name: "Cache-Control", Description: "Selection command capabilities are recomputed for this response.", Required: true},
			{Status: 202, Name: "Cache-Control", Description: "Private preparing handles are never cached.", Required: true},
			{Status: 202, Name: "Retry-After", Description: "Minimum delay before polling this preparation again.", Required: true},
			{Status: 202, Name: "Location", Description: "This caller's selection preparation resource.", Required: true},
		},
	}, Serve: d.browseSelectionQueryHandler}); err != nil {
		return err
	}
	return apikit.RegisterRaw(registry, apikit.RawRoute[noBody, noBody]{Metadata: apikit.Metadata{Audience: apikit.Clients,
		ID: "delete_library_browse_selection_query", Method: "DELETE", Path: path, Summary: "Release this caller's whole-view selection demand", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 204, NoResponseBody: true, Errors: failures,
	}, Serve: d.deleteBrowseSelectionQueryHandler})
}

// The caller identity includes domain/library and every session/authority
// binding. It is not content identity and never appears on the wire.
func browseSelectionCaller(p identity.Principal, library, fence string) (readprep.Caller, error) {
	raw, err := json.Marshal([]any{"browse-selection", library, p.ServerID, p.Hash, p.Authority, p.AccountID, p.ProfileID, p.Role, p.Epoch, p.Grant, fence})
	return readprep.Caller(raw), err
}

func (d Dependencies) browseSelectionAccess(p identity.Principal, library string, viewer catalog.Viewer) (catalog.SelectionPreparationAccess, error) {
	library = strings.Clone(library)
	caller, err := browseSelectionCaller(p, library, viewer.Fence)
	if err != nil {
		return catalog.SelectionPreparationAccess{}, err
	}
	policy, err := json.Marshal(viewer.EffectiveRestrictions())
	if err != nil {
		return catalog.SelectionPreparationAccess{}, err
	}
	expectedPolicy := string(policy)
	check := func(cat *catalog.Service) error {
		ctx, tx := cat.Context(), dbwork.Snapshot(cat.Context())
		if tx == nil {
			return readprep.ErrInvalid
		}
		live, err := d.Identity.PrincipalByDigestTx(ctx, tx, p.Hash)
		if err != nil {
			return err
		}
		if live.Viewer != p.Viewer || live.Epoch != p.Epoch || live.Grant != p.Grant {
			return identity.ErrForbidden
		}
		if err = live.PermitsRequest(ctx); err != nil {
			return err
		}
		if err = d.allowedLibraryTx(ctx, live, library, tx); err != nil {
			return err
		}
		current, err := contentaccess.RestrictionsTx(ctx, tx, live.Viewer)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if string(raw) != expectedPolicy {
			return readprep.ErrStale
		}
		return nil
	}
	return catalog.SelectionPreparationAccess{Caller: caller, Check: check, CapturedBytes: int64(unsafe.Sizeof(d)+unsafe.Sizeof(p)) + int64(2*len(caller)+2*len(policy)+len(library)) + 4096}, nil
}

func (d Dependencies) browseSelectionQueryScope(r *http.Request) (identity.Principal, catalog.Viewer, error) {
	p, err := d.strictPrincipalFor(r)
	if err != nil {
		return p, catalog.Viewer{}, err
	}
	if err = d.allowedLibrary(r.Context(), p, r.PathValue("id")); err != nil {
		return p, catalog.Viewer{}, err
	}
	fence, err := d.browseFence(r, p)
	if err != nil {
		return p, catalog.Viewer{}, err
	}
	viewer, err := d.catalogViewer(r, p, []string{r.PathValue("id")}, fence)
	return p, viewer, err
}

func isBrowseSelectionPreparationError(err error) bool {
	return errors.Is(err, readprep.ErrStale) || errors.Is(err, readprep.ErrCapacity) || errors.Is(err, readprep.ErrClosed) || errors.Is(err, readprep.ErrUncached) || errors.Is(err, catalog.ErrSelectionBatchStale) || errors.Is(err, catalog.ErrSelectionBatchCapacity)
}

func browseSelectionPreparationFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, readprep.ErrStale) || errors.Is(err, catalog.ErrSelectionBatchStale) || errors.Is(err, catalog.ErrReadingProjectionChanged) || errors.Is(err, catalog.ErrStaleContinuation) {
		policyError(w, "stale_continuation")
		return
	}
	if errors.Is(err, readprep.ErrCapacity) || errors.Is(err, readprep.ErrClosed) || errors.Is(err, readprep.ErrUncached) || errors.Is(err, catalog.ErrSelectionBatchCapacity) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "rate_limited", RetryAfterSeconds: 1})
		return
	}
	browseFailure(w, err)
}

func (d Dependencies) browseSelectionQueryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		policyError(w, "invalid_request")
		return
	}
	p, viewer, err := d.browseSelectionQueryScope(r)
	if err != nil {
		browseSelectionPreparationFailure(w, err)
		return
	}
	access, err := d.browseSelectionAccess(p, r.PathValue("id"), viewer)
	if err != nil {
		browseSelectionPreparationFailure(w, err)
		return
	}
	var result catalog.BrowseSelectionPreparationResult
	err = d.compose(r, viewer.EffectiveRestrictions(), func(cat *catalog.Service) error {
		authority, err := d.homeActionAuthority(cat.Context(), p)
		if err != nil {
			return err
		}
		result, err = cat.PollBrowseSelectionPreparation(r.PathValue("id"), viewer, r.PathValue("queryId"), access, authority)
		return err
	})
	// Preparing owns a delivery too; release on every later failure and keep
	// the reservation alive through MarshalJSON and the actual response write.
	defer result.Release()
	if err != nil {
		browseSelectionPreparationFailure(w, err)
		return
	}
	if err = d.browseSelectionExposure(r, p, viewer, result.Basis, access); err != nil {
		browseSelectionPreparationFailure(w, err)
		return
	}
	if result.Preparing != nil {
		pending := result.Preparing
		w.Header().Set("Retry-After", strconv.Itoa((pending.RetryAfterMs+999)/1000))
		w.Header().Set("Location", browseSelectionQueryPath(r.PathValue("id"), pending.QueryID))
		write(w, 202, browseSelectionQueryResponse{Preparing: pending})
		return
	}
	if result.Ready == nil {
		browseSelectionPreparationFailure(w, readprep.ErrInvalid)
		return
	}
	write(w, 200, browseSelectionQueryResponse{Ready: result.Ready})
}

func (d Dependencies) browseSelectionExposure(r *http.Request, p identity.Principal, viewer catalog.Viewer, basis string, access catalog.SelectionPreparationAccess) error {
	if err := d.browseAuthorityAfter(r, p, viewer); err != nil {
		return err
	}
	return dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		return d.Catalog.WithContext(ctx).CheckBrowseSelectionPreparationBasis(r.PathValue("id"), viewer, basis, access)
	})
}

func (d Dependencies) deleteBrowseSelectionQueryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		policyError(w, "invalid_request")
		return
	}
	p, viewer, err := d.browseSelectionQueryScope(r)
	if err != nil {
		browseSelectionPreparationFailure(w, err)
		return
	}
	access, err := d.browseSelectionAccess(p, r.PathValue("id"), viewer)
	if err != nil {
		browseSelectionPreparationFailure(w, err)
		return
	}
	err = d.compose(r, viewer.EffectiveRestrictions(), func(cat *catalog.Service) error {
		return cat.CancelBrowseSelectionPreparation(r.PathValue("queryId"), access)
	})
	if err == nil {
		err = d.browseAuthorityAfter(r, p, viewer)
	}
	if err != nil {
		browseSelectionPreparationFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func browseSelectionQueryPath(library, queryID string) string {
	return "/v1/libraries/" + url.PathEscape(library) + "/browse-selection-queries/" + url.PathEscape(queryID)
}

// Parent cohort hook: replace the synchronous aggregate arm of
// attachWholeSelection with this only when every consumer understands pending.
// This performs bounded admission, never waits for aggregate work, and never
// silently turns preparation capacity/errors into an empty successful count.
// The foreground calls it only behind PrepareBrowseSelections, which defaults
// to false until parent cohort enablement.
func (d Dependencies) attachWholeSelectionPreparation(r *http.Request, p identity.Principal, viewer catalog.Viewer, cat *catalog.Service, request catalog.BrowseRequest, body browseRequestBody, out *catalog.BrowseResult) error {
	if out.Bound == nil || body.SelectionBasis != "" || out.PageInfo.Start != 0 || body.Cursor != "" || body.Range != nil && body.Range.Start != 0 || body.Seek != nil || body.HomeRowDisplay != "" {
		return nil
	}
	access, err := d.browseSelectionAccess(p, request.Library, viewer)
	if err != nil {
		return err
	}
	pending, err := cat.SubmitBrowseSelectionPreparation(request, viewer, *out.Bound, access)
	if errors.Is(err, catalog.ErrSelectionBatchSearchUnsupported) {
		out.WholeSelection = nil
		out.WholeSelectionPreparing = nil
		out.WholeSelectionUnavailable = &catalog.BrowseSelectionUnavailable{Reason: "search_not_bounded"}
		return nil
	}
	if errors.Is(err, catalog.ErrSelectionBatchUnsupported) {
		return nil
	}
	if err != nil {
		return err
	}
	out.WholeSelection = nil
	out.WholeSelectionPreparing = &pending
	return nil
}
