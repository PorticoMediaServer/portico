package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http"
	"reflect"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

func (d Dependencies) readingSeriesScopeTx(ctx context.Context, tx *sql.Tx, captured identity.Principal, library string) (reading.SeriesScope, error) {
	var out reading.SeriesScope
	if d.Identity == nil {
		return out, identity.ErrUnauthorized
	}
	live, err := d.Identity.PrincipalByDigestTx(ctx, tx, captured.Hash)
	if err != nil {
		return out, err
	}
	if live.Viewer != captured.Viewer {
		return out, identity.ErrUnauthorized
	}
	if err = live.PermitsRequest(ctx); err != nil {
		return out, err
	}
	if err = d.allowedLibraryTx(ctx, live, library, tx); err != nil {
		return out, err
	}
	restrictions, err := contentaccess.RestrictionsTx(ctx, tx, live.Viewer)
	if err != nil {
		return out, err
	}
	out.Profile = identity.PersonalKey(live.Viewer)
	out.RestrictionFence = catalog.RestrictionFence(restrictions)
	var policy, restriction int64
	if live.Authority != "local" {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM policy WHERE server_id=?),0),COALESCE((SELECT revision FROM restrictions WHERE profile_id=?),0)`, d.Identity.ID(), live.ProfileID).Scan(&policy, &restriction); err != nil {
			return out, err
		}
	}
	// This is the existing browse viewer fence, from the same live facts as
	// its predicate. No cached restriction or earlier authority snapshot enters.
	out.Fence = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%d:%s", presentationIdentity(live), live.Epoch, policy, restriction, out.RestrictionFence))))
	if d.Reading != nil {
		out.Formats = d.Reading.SourceFormatScope()
	}
	err = tx.QueryRowContext(ctx, `SELECT r.revision+(SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1),COALESCE(v.revision,0) FROM library_revisions r LEFT JOIN viewer_revisions v ON v.library_id=r.library_id AND v.profile_id=? WHERE r.library_id=?`, out.Profile, library).Scan(&out.Catalog, &out.Viewer)
	return out, err
}

type ReadingSeriesSummary struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Count     int64          `json:"count"`
	ReadCount int64          `json:"readCount"`
	Author    *ReadingAuthor `json:"author,omitempty"`
}

func (ReadingSeriesSummary) JSONSchema() map[string]any {
	type wire ReadingSeriesSummary
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["id"].(map[string]any)["pattern"] = "^[A-Za-z0-9_-]{43}$"
	props["name"].(map[string]any)["minLength"] = 1
	props["name"].(map[string]any)["maxLength"] = 4096
	for _, name := range []string{"count", "readCount"} {
		props[name].(map[string]any)["minimum"] = 0
		props[name].(map[string]any)["maximum"] = 9007199254740991
	}
	props["count"].(map[string]any)["minimum"] = 1
	return shape
}

type ReadingSeriesItemsResponse struct {
	Items      []ReadingItem         `json:"items"`
	NextCursor string                `json:"nextCursor,omitempty"`
	Summary    *ReadingSeriesSummary `json:"summary,omitempty"`
}

func (d Dependencies) readingSeries(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			readingFailure(w, reading.ErrInvalid)
			return
		}
	}
	page, err := d.readingService().Series(r.Context(), p, r.PathValue("libraryId"), r.PathValue("seriesId"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		readingFailure(w, err)
		return
	}
	out := ReadingSeriesItemsResponse{Items: make([]ReadingItem, 0, len(page.Items)), NextCursor: page.NextCursor}
	if summary := page.Summary; summary != nil {
		out.Summary = &ReadingSeriesSummary{ID: summary.ID, Name: summary.Name, Count: summary.Count, ReadCount: summary.ReadCount}
		if summary.Author != nil {
			out.Summary.Author = &ReadingAuthor{Value: summary.Author.Value, Source: ReadingAttributionSource(summary.Author.Source)}
		}
	}
	for _, item := range page.Items {
		out.Items = append(out.Items, readingItemProjection(item))
	}
	if out.Items, err = readingBudget(out.Items, readingPageBytes, page.Truncate); err != nil {
		readingFailure(w, err)
		return
	}
	out.NextCursor = page.NextCursor
	write(w, 200, out)
}

func registerReadingSeriesHTTPContract(registry *apikit.Registry) error {
	m := apikit.Metadata{Audience: apikit.Clients, ID: "get_reading_series_items", Method: "GET", Path: "/v1/libraries/{libraryId}/reading/series/{seriesId}", Summary: "Read one complete authorized Reading series", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "request_in_progress", "internal"}, Query: []string{"cursor", "limit"}, QueryIntegers: emptyLimitQuery(50, 100)}
	return apikit.RegisterHTTP[struct{}, ReadingSeriesItemsResponse](registry, m)
}
