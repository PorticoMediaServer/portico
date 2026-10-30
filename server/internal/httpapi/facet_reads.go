package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// One process-owned runtime shares the read-preparation store with other
// domains. A nil runtime answers unavailable; requests never build a store.
type FacetReadRuntime interface {
	Initial(context.Context, readprep.Caller, catalog.Viewer, catalog.FacetRequest) (catalog.SimpleFacetResult, error)
	Continue(context.Context, readprep.Caller, catalog.Viewer, catalog.FacetRequest, string) (catalog.SimpleFacetResult, error)
	Poll(context.Context, readprep.Caller, string) (catalog.SimpleFacetResult, error)
	Cancel(context.Context, readprep.Caller, string) error
}

type facetReadResponse struct{}
type facetQueryResponse struct{}

func facetSchema[T any]() map[string]any {
	s, err := apikit.Schema(reflect.TypeFor[T]())
	if err != nil {
		panic(err)
	}
	return s
}

// facetPageSchema is the ready page of a library whose facets are not windowed;
// `facet-page` tells it apart from the windowed and preparing answers.
func facetPageSchema() map[string]any {
	return withVariantTag(facetSchema[catalog.FacetPage](), "page")
}

func windowSchema() map[string]any {
	return withVariantTag(facetSchema[catalog.SimpleFacetPage](), "window")
}
func fallbackSchema() map[string]any {
	return withVariantTag(facetSchema[catalog.SimpleFacetFallbackPage](), "fallback")
}
func preparingSchema() map[string]any {
	return withVariantTag(facetSchema[catalog.SimpleFacetPreparing](), "preparing")
}
func readingPreparingSchema() map[string]any {
	return withVariantTag((ReadingFacetPreparing{}).JSONSchema(), "reading-preparing")
}
func (facetReadResponse) JSONSchema() map[string]any {
	return map[string]any{"anyOf": []any{facetPageSchema(), windowSchema(), fallbackSchema(), preparingSchema(), readingPreparingSchema()}}
}
func (facetReadResponse) JSONSchemaForStatus(status int) map[string]any {
	if status == 200 {
		return map[string]any{"anyOf": []any{facetPageSchema(), windowSchema(), fallbackSchema()}}
	}
	if status == 202 {
		return map[string]any{"anyOf": []any{preparingSchema(), readingPreparingSchema()}}
	}
	return nil
}
func (facetQueryResponse) JSONSchema() map[string]any {
	return map[string]any{"anyOf": []any{windowSchema(), fallbackSchema(), preparingSchema()}}
}
func (facetQueryResponse) JSONSchemaForStatus(status int) map[string]any {
	if status == 200 {
		return map[string]any{"anyOf": []any{windowSchema(), fallbackSchema()}}
	}
	if status == 202 {
		return preparingSchema()
	}
	return nil
}

func facetPreparingHeaders() []apikit.ResponseHeader {
	return []apikit.ResponseHeader{
		{Status: 202, Name: "Location", Description: "Same-server facet preparation poll path", Required: true},
		{Status: 202, Name: "Retry-After", Description: "Minimum delay before polling", Required: true},
		{Status: 200, Name: "Location", Description: "Same-server poll path when a lower-bound fallback is shown"},
		{Status: 200, Name: "Retry-After", Description: "Minimum delay before polling a lower-bound fallback"},
	}
}

// Family identity survives access-token rotation. Policy revision is private
// authority, separate from the shared effective-policy content equivalence key.
func (d Dependencies) facetCallerTx(ctx context.Context, tx *sql.Tx, r *http.Request, p identity.Principal, across bool) (readprep.Caller, error) {
	family, err := d.Identity.SessionFamilyTx(ctx, tx, p)
	if err != nil {
		return "", err
	}
	fence, err := d.browseFence(r.WithContext(ctx), p)
	if err != nil {
		return "", err
	}
	if across {
		_, scope, err := d.homeScopeFor(r.WithContext(ctx), p)
		if err != nil {
			return "", err
		}
		fence += ":" + scope
	}
	raw, err := json.Marshal([]any{p.ServerID, family.ID, presentationIdentity(p), p.Epoch, across, fence})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return readprep.Caller(hex.EncodeToString(sum[:])), nil
}

func (d Dependencies) facetReadContext(r *http.Request, p identity.Principal) (context.Context, readprep.Caller, error) {
	return d.preparationReadContext(r, p, false)
}

func (d Dependencies) preparationReadContext(r *http.Request, p identity.Principal, across bool) (context.Context, readprep.Caller, error) {
	var caller readprep.Caller
	err := dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		var err error
		caller, err = d.facetCallerTx(ctx, dbwork.Snapshot(ctx), r, p, across)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	guard := func(ctx context.Context, actual readprep.Caller, snapshot catalog.FacetBaseSnapshot) error {
		if actual != caller || snapshot.Scope.Library == "" || snapshot.Scope.Policy == "" {
			return catalog.ErrStaleContinuation
		}
		return dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error {
			tx := dbwork.Snapshot(ctx)
			now, err := d.facetCallerTx(ctx, tx, r, p, across)
			if err != nil {
				return err
			}
			if now != caller {
				return catalog.ErrStaleContinuation
			}
			if err = d.allowedLibraryTx(ctx, p, snapshot.Scope.Library, tx); err != nil {
				return err
			}
			restrictions, _, err := d.viewerRestrictions(r.WithContext(ctx), p)
			if err != nil {
				return err
			}
			if snapshot.Scope.Policy != catalog.FacetBasePolicy(restrictions) {
				return catalog.ErrStaleContinuation
			}
			if snapshot.ID != 0 {
				authority, err := catalog.CaptureFacetAuthority(ctx, tx, snapshot.Scope)
				if err != nil {
					return err
				}
				if authority != snapshot.Authority {
					return catalog.ErrStaleContinuation
				}
			}
			return nil
		})
	}
	return catalog.WithFacetWindowAuthority(r.Context(), guard), caller, nil
}

func facetReadFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, readprep.ErrStale):
		browseFailure(w, catalog.ErrStaleContinuation)
	case errors.Is(err, readprep.ErrCapacity):
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "search_busy", RetryAfterSeconds: 1, Cause: err})
	case errors.Is(err, catalog.ErrFacetBaseUnavailable), errors.Is(err, catalog.ErrFacetBaseCapacity), errors.Is(err, readprep.ErrClosed):
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "metadata_unavailable", RetryAfterSeconds: 1, Cause: err})
	case errors.Is(err, catalog.ErrFacetBaseInput):
		browseFailure(w, catalog.ErrCursor)
	default:
		browseFailure(w, err)
	}
}

func writeFacetRead(w http.ResponseWriter, result catalog.SimpleFacetResult) {
	defer result.Release()
	w.Header().Set("Cache-Control", "private, no-store")
	if result.Fallback != nil && result.Page == nil && result.Preparing == nil {
		pending := result.Fallback.Preparation
		w.Header().Set("Location", "/v1/facet-queries/"+pending.QueryID)
		w.Header().Set("Retry-After", strconv.Itoa(max(1, (pending.RetryAfterMs+999)/1000)))
		write(w, http.StatusOK, variantBody{"fallback", result.Fallback})
		return
	}
	if result.Preparing != nil && result.Page == nil && result.Fallback == nil {
		w.Header().Set("Location", "/v1/facet-queries/"+result.Preparing.QueryID)
		w.Header().Set("Retry-After", strconv.Itoa(max(1, (result.Preparing.RetryAfterMs+999)/1000)))
		write(w, http.StatusAccepted, variantBody{"preparing", result.Preparing})
		return
	}
	if result.Page == nil || result.Preparing != nil || result.Fallback != nil {
		failure(w, errors.New("invalid facet preparation result"))
		return
	}
	write(w, http.StatusOK, variantBody{"window", result.Page})
}

func (d Dependencies) simpleFacetsHandler(w http.ResponseWriter, r *http.Request, p identity.Principal, viewer catalog.Viewer, request catalog.FacetRequest) {
	if d.FacetReads == nil {
		facetReadFailure(w, catalog.ErrFacetBaseUnavailable)
		return
	}
	ctx, caller, err := d.facetReadContext(r, p)
	if err != nil {
		facetReadFailure(w, err)
		return
	}
	var result catalog.SimpleFacetResult
	token := request.After
	if request.Before != "" {
		token = request.Before
	}
	if request.At != "" {
		token = request.At
	}
	if token == "" {
		result, err = d.FacetReads.Initial(ctx, caller, viewer, request)
	} else {
		result, err = d.FacetReads.Continue(ctx, caller, viewer, request, token)
	}
	if err != nil {
		result.Release()
		facetReadFailure(w, err)
		return
	}
	writeFacetRead(w, result)
}

func validFacetQueryID(id string) bool {
	if len(id) == 0 || len(id) > 512 {
		return false
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (d Dependencies) facetQueryHandler(w http.ResponseWriter, r *http.Request) {
	p, err := d.principal(r)
	if err != nil {
		facetReadFailure(w, err)
		return
	}
	if len(r.URL.Query()) != 0 || !validFacetQueryID(r.PathValue("id")) {
		facetReadFailure(w, catalog.ErrFacetBaseInput)
		return
	}
	if d.FacetReads == nil {
		facetReadFailure(w, catalog.ErrFacetBaseUnavailable)
		return
	}
	ctx, caller, err := d.facetReadContext(r, p)
	if err != nil {
		facetReadFailure(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		if err = d.FacetReads.Cancel(ctx, caller, r.PathValue("id")); err != nil {
			facetReadFailure(w, err)
			return
		}
		write(w, http.StatusNoContent, nil)
		return
	}
	result, err := d.FacetReads.Poll(ctx, caller, r.PathValue("id"))
	if err != nil {
		result.Release()
		facetReadFailure(w, err)
		return
	}
	writeFacetRead(w, result)
}
