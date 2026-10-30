package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type GenreReadRuntime interface {
	Initial(context.Context, readprep.Caller, catalog.Viewer, catalog.SearchGenreRequest) (catalog.SearchGenreResult, error)
	Continue(context.Context, readprep.Caller, catalog.Viewer, catalog.SearchGenreRequest, string) (catalog.SearchGenreResult, error)
	Poll(context.Context, readprep.Caller, string) (catalog.SearchGenreResult, error)
	Cancel(context.Context, readprep.Caller, string) error
}

type genreReadResponse struct{}

func genreReadySchema() map[string]any {
	s := facetSchema[catalog.SearchGenrePage]()
	p := s["properties"].(map[string]any)
	for _, key := range []string{"queryId", "nextCursor", "previousCursor", "reloadCursor"} {
		p[key] = map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "pattern": "^[A-Za-z0-9_-]+$"}
	}
	p["genres"].(map[string]any)["maxItems"] = 50
	return s
}
func genrePreparingSchema() map[string]any {
	s := facetSchema[catalog.SearchGenrePreparing]()
	p := s["properties"].(map[string]any)
	p["kind"] = map[string]any{"type": "string", "const": "genre-preparing"}
	p["state"] = map[string]any{"type": "string", "const": "preparing"}
	p["queryId"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "pattern": "^[A-Za-z0-9_-]+$"}
	p["retryAfterMs"] = map[string]any{"type": "integer", "minimum": 1, "maximum": int64(9007199254740991)}
	return s
}
func genreFallbackSchema() map[string]any {
	s := facetSchema[catalog.SearchGenreFallbackPage]()
	p := s["properties"].(map[string]any)
	p["kind"] = map[string]any{"type": "string", "const": "genre-fallback"}
	p["hasGenresKnown"] = map[string]any{"type": "boolean", "const": false}
	p["totalIsLowerBound"] = map[string]any{"type": "boolean", "const": true}
	p["preparation"] = genrePreparingSchema()
	p["queryId"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "pattern": "^[A-Za-z0-9_-]+$"}
	p["revision"] = map[string]any{"type": "string", "const": "preparing"}
	p["genres"].(map[string]any)["maxItems"] = 0
	for _, key := range []string{"hasGenres", "nextCursor", "previousCursor", "reloadCursor"} {
		delete(p, key)
	}
	return s
}
func genreVariants() (ready, fallback, preparing map[string]any) {
	return withVariantTag(genreReadySchema(), "ready"), withVariantTag(genreFallbackSchema(), "fallback"), withVariantTag(genrePreparingSchema(), "preparing")
}
func (genreReadResponse) JSONSchema() map[string]any {
	ready, fallback, preparing := genreVariants()
	return map[string]any{"anyOf": []any{ready, fallback, preparing}}
}
func (genreReadResponse) JSONSchemaForStatus(status int) map[string]any {
	if status == 200 {
		ready, fallback, _ := genreVariants()
		return map[string]any{"anyOf": []any{ready, fallback}}
	}
	if status == 202 {
		_, _, preparing := genreVariants()
		return preparing
	}
	return nil
}
func genrePreparingHeaders() []apikit.ResponseHeader {
	return []apikit.ResponseHeader{{Status: 202, Name: "Location", Description: "Same-server genre preparation poll path", Required: true}, {Status: 202, Name: "Retry-After", Description: "Minimum delay before polling", Required: true}, {Status: 200, Name: "Location", Description: "Same-server poll path when a lower-bound fallback is shown"}, {Status: 200, Name: "Retry-After", Description: "Minimum delay before polling a lower-bound fallback"}}
}

func writeGenreRead(w http.ResponseWriter, out catalog.SearchGenreResult) {
	defer out.Release()
	w.Header().Set("Cache-Control", "private, no-store")
	if out.Fallback != nil && out.Page == nil && out.Preparing == nil {
		pending := out.Fallback.Preparation
		w.Header().Set("Location", "/v1/genre-queries/"+pending.QueryID)
		w.Header().Set("Retry-After", strconv.Itoa(max(1, (pending.RetryAfterMs+999)/1000)))
		write(w, http.StatusOK, variantBody{"fallback", out.Fallback})
		return
	}
	if out.Preparing != nil && out.Page == nil && out.Fallback == nil {
		w.Header().Set("Location", "/v1/genre-queries/"+out.Preparing.QueryID)
		w.Header().Set("Retry-After", strconv.Itoa(max(1, (out.Preparing.RetryAfterMs+999)/1000)))
		write(w, http.StatusAccepted, variantBody{"preparing", out.Preparing})
		return
	}
	if out.Page == nil || out.Preparing != nil || out.Fallback != nil {
		failure(w, errors.New("invalid genre preparation result"))
		return
	}
	write(w, http.StatusOK, variantBody{"ready", out.Page})
}

// Mounted with its shared descriptor once generated; every surface uses this
// same read rather than deriving counts or representative art from Search hits.
func (d Dependencies) searchGenresHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "limit" && key != "cursor" && key != "preview") || len(values) != 1 {
			browseFailure(w, catalog.ErrCursor)
			return
		}
	}
	request := catalog.SearchGenreRequest{Cursor: query.Get("cursor"), Preview: query.Get("preview"), LimitPresent: query.Has("limit")}
	if query.Has("preview") && (request.Preview != "backdrop" && request.Preview != "covers" || query.Has("cursor")) || query.Has("cursor") && !validFacetQueryID(request.Cursor) {
		browseFailure(w, catalog.ErrCursor)
		return
	}
	if query.Has("limit") {
		var err error
		request.Limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || request.Limit < 1 || request.Limit > catalog.SearchGenresMaximumLimit {
			browseFailure(w, catalog.ErrCursor)
			return
		}
	}
	principal, viewer, err := d.acrossViewer(r)
	if err != nil {
		failure(w, err)
		return
	}
	if d.GenreReads == nil {
		facetReadFailure(w, catalog.ErrFacetBaseUnavailable)
		return
	}
	ctx, caller, err := d.preparationReadContext(r, principal, true)
	if err != nil {
		facetReadFailure(w, err)
		return
	}
	var out catalog.SearchGenreResult
	if request.Cursor != "" {
		out, err = d.GenreReads.Continue(ctx, caller, viewer, request, request.Cursor)
	} else {
		out, err = d.GenreReads.Initial(ctx, caller, viewer, request)
	}
	if err != nil {
		out.Release()
		facetReadFailure(w, err)
		return
	}
	if err = d.browseAuthorityAfter(r, principal, viewer); err != nil {
		out.Release()
		browseFailure(w, err)
		return
	}
	writeGenreRead(w, out)
}

func (d Dependencies) genreQueryHandler(w http.ResponseWriter, r *http.Request) {
	p, _, err := d.acrossViewer(r)
	if err != nil {
		facetReadFailure(w, err)
		return
	}
	if len(r.URL.Query()) != 0 || !validFacetQueryID(r.PathValue("id")) {
		facetReadFailure(w, catalog.ErrFacetBaseInput)
		return
	}
	if d.GenreReads == nil {
		facetReadFailure(w, catalog.ErrFacetBaseUnavailable)
		return
	}
	ctx, caller, err := d.preparationReadContext(r, p, true)
	if err != nil {
		facetReadFailure(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		if err = d.GenreReads.Cancel(ctx, caller, r.PathValue("id")); err != nil {
			facetReadFailure(w, err)
			return
		}
		write(w, http.StatusNoContent, nil)
		return
	}
	out, err := d.GenreReads.Poll(ctx, caller, r.PathValue("id"))
	if err != nil {
		out.Release()
		facetReadFailure(w, err)
		return
	}
	_, afterCaller, err := d.preparationReadContext(r, p, true)
	if err != nil || afterCaller != caller {
		out.Release()
		if err == nil {
			err = catalog.ErrStaleContinuation
		}
		facetReadFailure(w, err)
		return
	}
	writeGenreRead(w, out)
}
