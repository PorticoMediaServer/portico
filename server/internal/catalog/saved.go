package catalog

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"strconv"
	"strings"
	"time"
)

// savedSorts are all computed on the server. progress and duration are derived
// columns rather than client arithmetic, so paging stays stable across devices.
var savedSorts = map[string]string{
	"title":    `lower(i.title)`,
	"added":    `COALESCE(detail.added_text,'')`,
	"updated":  `COALESCE(NULLIF(p.last_played_at,''),COALESCE(detail.added_text,''))`,
	"year":     `CAST(COALESCE(i.year,0) AS REAL)`,
	"duration": `COALESCE((SELECT duration FROM catalog_browse_item_metrics WHERE item_id=i.id),0.0)`,
	"progress": `COALESCE((SELECT g.position FROM progress g WHERE g.profile_id=p.profile_id AND g.item_id=i.id),0.0)`,
}
var savedNumericSorts = map[string]bool{"year": true, "duration": true, "progress": true}

// savedFilters read the viewer's own state only. watched is the compact watch
// state, favorites is the saved item's own favorite flag, inProgress means
// started and not finished; a completed item is never "in progress" because
// its resume was cleared.
var savedFilters = map[string]string{
	"all":        ``,
	"unwatched":  ` AND ` + personalstate.CompactSQL("p.profile_id", "i.id") + `=0`,
	"inProgress": ` AND ` + personalstate.CompactSQL("p.profile_id", "i.id") + `=0 AND ((i.kind NOT IN(20,21) AND COALESCE((SELECT g.position FROM progress g WHERE g.profile_id=p.profile_id AND g.item_id=i.id),0)>0) OR (i.kind IN(20,21) AND ` + savedReadingInProgressSQL("p.profile_id") + `))`,
	"watched":    ` AND ` + personalstate.CompactSQL("p.profile_id", "i.id") + `=1`,
	"favorites":  ` AND p.favorite=1`,
}

func savedNavigation() []ContentTab {
	return []ContentTab{{"watchlist", "saved.watchlist", "watchlist"}, {"favorites", "saved.favorites", "favorites"}, {"playlists", "saved.playlists", "playlists"}}
}
func savedSortOptions() []ContentSort {
	return []ContentSort{{"title", "sort.title", []string{"asc", "desc"}}, {"added", "sort.added", []string{"asc", "desc"}}, {"updated", "sort.updated", []string{"asc", "desc"}}, {"year", "sort.year", []string{"asc", "desc"}}, {"duration", "sort.duration", []string{"asc", "desc"}}, {"progress", "sort.progress", []string{"asc", "desc"}}}
}
func savedFilterOptions(all, unwatched, inProgress, watched, favorites int) []ContentFilter {
	return []ContentFilter{{ID: "filter", LabelKey: "saved.filter", Options: []ContentFilterOption{{ID: "all", Label: "All", Count: all}, {ID: "unwatched", Label: "Unwatched", Count: unwatched}, {ID: "inProgress", Label: "In progress", Count: inProgress}, {ID: "watched", Label: "Watched", Count: watched}, {ID: "favorites", Label: "Favorites", Count: favorites}}}}
}
func (s *Service) Saved(r ContentRequest, libraries []string) (ContentEnvelope, error) {
	return s.saved(r, libraries, false)
}

// saved reads one Saved page. countsOnly stops after the envelope, revision,
// shape and filter counts, for a caller that chooses its own page.
func (s *Service) saved(r ContentRequest, libraries []string, countsOnly bool) (ContentEnvelope, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out ContentEnvelope
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			out, readErr = s.WithContext(ctx).saved(r, libraries, countsOnly)
			return readErr
		})
		return out, err
	}
	if err := s.compactProjectionReady(25, 26); err != nil {
		return ContentEnvelope{}, err
	}
	if r.Profile != "" && r.Profile != r.Viewer.Profile || r.ViewerFence != "" && r.ViewerFence != r.Viewer.Fence {
		return ContentEnvelope{}, ErrCursor
	}
	r = r.scoped()
	libraries = r.Viewer.Libraries
	if err := s.prepareViewer(r.Viewer); err != nil {
		return ContentEnvelope{}, err
	}
	out := ContentEnvelope{Scope: ContentScope{r.ServerID, "", "mixed", r.View, "", r.ViewerFence}, Navigation: savedNavigation(), Sorts: savedSortOptions(), Filters: []ContentFilter{}, Sections: []ContentSection{}}
	var order, filter string
	var e error
	r, order, filter, e = normalizeSavedRequest(r)
	if e != nil {
		return out, e
	}
	heading := "My List"
	flag := "watchlisted"
	if r.View == "favorites" {
		heading = "Favorites"
		flag = "favorite"
	}
	readingScope, e := s.savedReading(r, libraries, flag)
	if e != nil {
		return out, e
	}
	out.Heading = ContentHeading{Key: "saved." + r.View, Fallback: heading}
	// The published query echoes the selected filter alone. The cursor scope below
	// keeps filter:shape composite so a page cannot cross shape boundaries.
	out.Query = ContentQuery{Sort: r.Sort, Direction: r.Direction, Category: r.Filter, Limit: r.Limit, SearchMode: "none"}
	rev, e := s.homeRevision(libraries, r.Profile)
	if e != nil {
		return out, e
	}
	out.Revision = rev
	scope := cursorScope{Profile: r.Profile, View: r.View, Section: r.View, Viewer: r.ViewerFence, Sort: r.Sort, Direction: r.Direction, Category: r.Filter + ":" + r.Shape, Limit: r.Limit}
	scope.Search = readingScope.fence
	base, baseArgs := savedLeafBase(r, libraries, flag, readingScope)
	// One pass over the saved set answers both shape counts and every filter
	// count within the selected shape.
	shapeCounts := &SavedShapeCounts{}
	shape := "1"
	if predicate := savedShapePredicate(r.Shape); predicate != "" {
		shape = strings.TrimPrefix(predicate, " AND ")
	}
	counted := func(filter string) string {
		return `,COALESCE(sum(` + shape + ` AND (` + strings.TrimPrefix(savedFilters[filter], " AND ") + `)),0)`
	}
	var all, unwatched, inProgress, watched, favorites int
	if e = s.read().QueryRow(`SELECT COALESCE(sum(i.kind IN(1,2,3,4,20,21)),0),COALESCE(sum(i.kind IN(5,6,7,8,9,13)),0),COALESCE(sum(`+shape+`),0)`+counted("unwatched")+counted("inProgress")+counted("watched")+counted("favorites")+base, baseArgs...).Scan(&shapeCounts.Posters, &shapeCounts.Squares, &all, &unwatched, &inProgress, &watched, &favorites); e != nil {
		return out, e
	}
	out.ShapeCounts = shapeCounts
	base += savedShapePredicate(r.Shape)
	out.Filters = savedFilterOptions(all, unwatched, inProgress, watched, favorites)
	if countsOnly {
		return out, nil
	}
	count := map[string]int{"all": all, "unwatched": unwatched, "inProgress": inProgress, "watched": watched, "favorites": favorites}[r.Filter]
	where := base + filter
	args := append([]any{}, baseArgs...)
	op := ">"
	if r.Direction == "desc" {
		op = "<"
	}
	if r.Cursor != "" {
		c, err := s.decodeRevisionCursor(r.Cursor, scope, rev)
		if err != nil {
			return out, err
		}
		value, err := cursorOrderValue(c.Value, savedNumericSorts[r.Sort])
		if err != nil {
			return out, err
		}
		entityID, err := strconv.ParseInt(c.ID, 10, 64)
		if err != nil || entityID < 1 {
			return out, ErrCursor
		}
		where += ` AND (` + order + op + `? OR (` + order + `=? AND i.id` + op + `?))`
		args = append(args, value, value, entityID)
	}
	args = append(args, r.Limit+1)
	selected, e := s.homeCandidates(`SELECT i.id,`+order+where+` ORDER BY `+order+` `+r.Direction+`,i.id `+r.Direction+` LIMIT ?`, args...)
	if e != nil {
		return out, e
	}
	next := ""
	if len(selected) > r.Limit {
		selected = selected[:r.Limit]
		last := selected[len(selected)-1]
		next, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: last.order, ID: last.id, Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
		if e != nil {
			return out, e
		}
	}
	ids := []string{}
	entityIDs := make([]int64, 0, len(selected))
	for _, row := range selected {
		entityID, err := strconv.ParseInt(row.id, 10, 64)
		if err != nil || entityID < 1 {
			return out, ErrCursor
		}
		entityIDs = append(entityIDs, entityID)
	}
	rawEntityIDs, _ := json.Marshal(entityIDs)
	publicRows, e := s.read().Query(`SELECT id,pid(public_id) FROM catalog_entities WHERE id IN(SELECT value FROM json_each(?))`, string(rawEntityIDs))
	if e != nil {
		return out, e
	}
	publicIDs := map[int64]string{}
	for publicRows.Next() {
		var id int64
		var public string
		if e = publicRows.Scan(&id, &public); e != nil {
			publicRows.Close()
			return out, e
		}
		publicIDs[id] = public
	}
	e = publicRows.Err()
	publicRows.Close()
	if e != nil {
		return out, e
	}
	for _, id := range entityIDs {
		public, ok := publicIDs[id]
		if !ok {
			return out, ErrStaleContinuation
		}
		ids = append(ids, public)
	}
	// The same entries a recommendation row gives these ids: items with their state, containers
	// with their own art (or a member's).
	entries, e := s.WithRecommendationRestrictions(r.Viewer.EffectiveRestrictions()).savedEntries(r.Profile, ids, readingScope)
	if e != nil {
		return out, e
	}
	for i := range entries {
		if entries[i].Available != nil && !*entries[i].Available {
			entries[i].Playback = nil
		}
	}
	if len(entries) > 0 {
		out.Sections = append(out.Sections, contentSection(r.View, "grid", heading, entries, count, next))
	} else {
		out.Empty = &ContentHeading{Key: "saved.empty", Fallback: "Nothing saved here yet."}
	}
	after, e := s.homeRevision(libraries, r.Profile)
	if e != nil {
		return out, e
	}
	if after != rev {
		return out, ErrStaleContinuation
	}
	return out, nil
}
