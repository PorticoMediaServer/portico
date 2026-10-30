package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

type playlistReorder struct {
	OperationID      string   `json:"operationId"`
	ExpectedRevision int64    `json:"expectedRevision"`
	EntryIDs         []string `json:"entryIds"`
	AfterEntryID     *string  `json:"afterEntryId,omitempty"`
}

func playlistWindowError(err error) error {
	switch {
	case errors.Is(err, catalog.ErrPlaylistConflict), errors.Is(err, catalog.ErrStaleContinuation):
		return &apierror.Error{Code: "revision_mismatch", Cause: err}
	case errors.Is(err, catalog.ErrPlaylistCapacity):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	}
	return bulkError(err)
}

var playlistOperationID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func registerPlaylistWindows(registry *apikit.Registry, d Dependencies) {
	failures := []string{"invalid_request", "invalid_cursor", "unauthorized", "not_found", "revision_mismatch", "idempotency_key_reused", "rate_limited", "internal"}
	rate := &personalLimiter{}
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_playlist_order", Method: "GET", Path: "/v1/playlists/{id}/order", Summary: "Read a revision-fenced window of playlist occurrence IDs", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: emptyLimitQuery(100, 100), Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (catalog.PlaylistOrder, error) {
		window := catalog.PlaylistOrderWindow{Cursor: r.URL.Query().Get("cursor"), Limit: 100}
		if value := r.URL.Query().Get("limit"); value != "" {
			n, e := strconv.Atoi(value)
			if e != nil || n < 1 || n > 100 {
				return catalog.PlaylistOrder{}, playlistWindowError(catalog.ErrCursor)
			}
			window.Limit = n
		}
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			return catalog.PlaylistOrder{}, e
		}
		out, e := d.Catalog.WithContext(ctx).PlaylistOrder(d.Identity.ID(), fence, r.PathValue("id"), resourceActor(p), func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") }, window)
		if e != nil {
			return out, playlistWindowError(e)
		}
		_, _, after, e := d.homeScope(r)
		if e != nil {
			return out, e
		}
		if fence != after {
			return out, playlistWindowError(catalog.ErrStaleContinuation)
		}
		return out, playlistWindowError(d.Catalog.CheckPlaylistOrder(out.PlaylistID, resourceActor(p), out.Revision))
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "reorder_playlist_window", Method: "PUT", Path: "/v1/playlists/{id}/order", Summary: "Move or permute at most 200 playlist occurrences", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, BodyLimit: 65536, Errors: failures}, func(ctx context.Context, r *http.Request, in playlistReorder) (catalog.PlaylistReceipt, error) {
		p, _, _, e := d.homeScope(r)
		if e != nil {
			return catalog.PlaylistReceipt{}, e
		}
		if !rate.allow(viewerScope(p)) {
			return catalog.PlaylistReceipt{}, &apierror.Error{Code: "rate_limited"}
		}
		if !playlistOperationID.MatchString(in.OperationID) || in.ExpectedRevision < 1 || in.ExpectedRevision > 9007199254740991 || len(in.EntryIDs) < 1 || len(in.EntryIDs) > 200 {
			return catalog.PlaylistReceipt{}, &apierror.Error{Code: "invalid_request"}
		}
		seen := map[string]bool{}
		for _, id := range in.EntryIDs {
			if id == "" || seen[id] {
				return catalog.PlaylistReceipt{}, &apierror.Error{Code: "invalid_request"}
			}
			seen[id] = true
		}
		if in.AfterEntryID != nil && seen[*in.AfterEntryID] {
			return catalog.PlaylistReceipt{}, &apierror.Error{Code: "invalid_request"}
		}
		m := catalog.PlaylistMutation{OperationID: in.OperationID, ExpectedRevision: in.ExpectedRevision, EntryIDs: in.EntryIDs, AfterEntryID: in.AfterEntryID}
		out, e := d.Catalog.WithContext(ctx).MutatePlaylist(resourceActor(p), r.PathValue("id"), "reorder", "", m, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if e != nil {
			return out, playlistWindowError(e)
		}
		_, _, fence, e := d.homeScope(r)
		if e != nil {
			return out, e
		}
		out.Deleted, e = d.Catalog.PlaylistReceiptStatus(out.PlaylistID, resourceActor(p))
		out.ServerID = d.Identity.ID()
		out.ViewerFence = fence
		return out, playlistWindowError(e)
	})
}

func (playlistReorder) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operationId", "expectedRevision", "entryIds"}, "properties": map[string]any{
		"operationId":      map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$"},
		"expectedRevision": map[string]any{"type": "integer", "minimum": 1, "maximum": int64(9007199254740991)},
		"entryIds":         map[string]any{"type": "array", "minItems": 1, "maxItems": 200, "uniqueItems": true, "items": map[string]any{"type": "string", "minLength": 1}},
		"afterEntryId":     map[string]any{"type": "string"},
	}}
}
