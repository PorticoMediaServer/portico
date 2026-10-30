package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strconv"
	"time"
)

// Playlist resources have their own ACL and membership table. A Music pivot
// must not pretend they are catalog entities or scan clients' Saved pages to
// guess which libraries they contain.
func musicPlaylistCapabilities(lib Library) BrowseCapabilities {
	pivot := BrowsePivotCapability{ID: "playlists", LabelKey: "library.tab.playlists", EntityKinds: []string{"playlist"},
		DefaultSort: titleAscending(), SupportedViews: []string{"grid"}, Browsable: true, Sorts: []string{"title"}, Filters: []string{}, Paging: "offset"}
	return BrowseCapabilities{Library: lib, Pivots: []BrowsePivotCapability{pivot}, ResolvedPivot: &pivot,
		Fields: []BrowseFieldCapability{}, QuickFilters: []BrowseQuickFilter{}, Surfaces: []BrowseSurfaceCapability{}, QueryLimits: browseQueryLimits(),
		Sorts: []BrowseSortCapability{{ID: "title", LabelKey: "sort.title", Directions: []string{"asc", "desc"}, DefaultDirection: "asc", ApplicableKinds: []string{"playlist"}}}}
}

func (s *Service) browseMusicPlaylists(r BrowseRequest) (BrowseResult, error) {
	out := BrowseResult{Pivot: "playlists", Entries: []ContentEntry{}, PositionIndex: []BrowsePositionAnchor{}}
	lib, err := s.library(r.Library)
	if err != nil {
		return out, err
	}
	if lib.Kind != "music" {
		return out, sql.ErrNoRows
	}
	if r.Query != nil || r.Seek != nil || r.Range != nil && r.Cursor != "" {
		return out, browseIssue("query", "playlist resources support their own unfiltered title order")
	}
	sorts := r.Sort
	if len(sorts) == 0 {
		sorts = titleAscending()
	}
	if len(sorts) != 1 || sorts[0].Field != "title" || sorts[0].Seed != 0 || sorts[0].Direction != "asc" && sorts[0].Direction != "desc" {
		return out, browseIssue("sort", "playlist resources support title ascending or descending")
	}
	out.Applied = BrowseApplied{Sort: sorts}
	limit := pageLimit(r.Limit)
	base, err := s.browseRevision(r, browsePivot{ID: "playlists"}, sorts)
	if err != nil {
		return out, err
	}
	var generation int64
	if err = s.read().QueryRow(`SELECT revision FROM saved_resource_generation WHERE id=1`).Scan(&generation); err != nil {
		return out, err
	}
	digest := sha256.Sum256([]byte(base + "\x00saved:" + strconv.FormatInt(generation, 10)))
	revision := "browse_" + hex.EncodeToString(digest[:])
	out.PageInfo.Revision = revision
	scope := cursorScope{Library: r.Library, Profile: r.Profile, Viewer: r.ViewerFence, View: "browse:playlists", Sort: canonicalSort(sorts), Category: revision, Limit: limit}
	start := 0
	expires := time.Now().Add(BrowseCursorTTLSecond * time.Second).Unix()
	if r.Cursor != "" {
		cursor, e := s.decodeCursor(r.Cursor, scope)
		if e != nil {
			return out, e
		}
		start, err = strconv.Atoi(cursor.Value)
		if err != nil || start < 0 {
			return out, ErrCursor
		}
		expires = cursor.Expires
	}
	if r.Range != nil {
		if r.Range.Start < 0 || len(r.Range.Revision) > 128 || r.Range.AnchorID != "" {
			return out, browseIssue("range", "playlist range identity is invalid")
		}
		if r.Range.Revision != "" && r.Range.Revision != revision {
			return out, ErrStaleContinuation
		}
		start = r.Range.Start
	}
	section, err := s.musicDiscoverPlaylists(ContentRequest{Library: r.Library, Profile: r.Profile, Viewer: r.viewer, Limit: limit, Direction: sorts[0].Direction}, start)
	if err != nil {
		return out, err
	}
	out.Entries = section.Entries
	out.PageInfo.Start, out.PageInfo.Total, out.PageInfo.HasMore = start, section.TotalCount, section.NextCursor != ""
	if out.PageInfo.HasMore && r.Range == nil {
		out.PageInfo.NextCursor, err = s.encodeCursor(cursorValue{Scope: scope, Value: strconv.Itoa(start + len(out.Entries)), Expires: expires})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
