package catalog

import (
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func directoryRevision(q personalReader, a ResourceActor) (ContentRevision, error) {
	var r ContentRevision
	e := q.QueryRow(`SELECT COALESCE((SELECT revision FROM playlist_directory_revisions WHERE authority=? AND account_id=? AND profile_id=?),0)`, a.Authority, a.AccountID, a.ProfileID).Scan(&r.Catalog)
	return r, e
}
func resourceEnvelope(r ContentRequest, title string) ContentEnvelope {
	return ContentEnvelope{Scope: ContentScope{r.ServerID, "", "mixed", r.View, r.EntityID, r.ViewerFence}, Heading: ContentHeading{Key: "saved." + r.View, Fallback: title}, Query: ContentQuery{Sort: r.Sort, Direction: r.Direction, Limit: r.Limit, SearchMode: "none"}, Navigation: savedNavigation(), Sorts: []ContentSort{}, Filters: []ContentFilter{}, Sections: []ContentSection{}}
}

// playlistDirectorySorts are the orders a list of playlists offers: by name,
// by when each was made or last changed, and by how many entries it holds.
// Each is a key over the playlist row itself, so a page costs the viewer's
// own playlists, never the library.
var playlistDirectorySorts = []struct {
	id, label, key, direction string
}{
	{"title", "playlists.sort.title", "p.name COLLATE NOCASE", "asc"},
	{"created", "playlists.sort.created", "p.created_at", "desc"},
	{"updated", "playlists.sort.updated", "COALESCE(NULLIF(p.updated_at,''),p.created_at)", "desc"},
	{"count", "playlists.sort.count", "printf('%010d',p.entry_count)", "desc"},
}

// playlistDirectoryFilters are the named groups a list of playlists offers.
var playlistDirectoryFilters = map[string]string{
	"all":    "1",
	"mine":   "(p.owner_authority=? AND p.owner_account=? AND p.owner_profile=?)",
	"shared": "NOT (p.owner_authority=? AND p.owner_account=? AND p.owner_profile=?)",
	"pinned": "EXISTS(SELECT 1 FROM saved_pins pin WHERE pin.owner_key=? AND pin.kind='playlist' AND pin.resource_id=p.token AND pin.pinned=1)",
}

func (s *Service) PlaylistDirectory(r ContentRequest, a ResourceActor) (ContentEnvelope, error) {
	if r.Sort == "" {
		r.Sort = "title"
	}
	if r.Filter == "" {
		r.Filter = "all"
	}
	if r.Limit == 0 {
		r.Limit = 40
	}
	out := resourceEnvelope(r, "Playlists")
	key := ""
	for _, sort := range playlistDirectorySorts {
		out.Sorts = append(out.Sorts, ContentSort{sort.id, sort.label, []string{"asc", "desc"}})
		if sort.id == r.Sort {
			key = sort.key
			if r.Direction == "" {
				r.Direction = sort.direction
			}
		}
	}
	out.Query.Sort, out.Query.Direction = r.Sort, r.Direction
	predicate, known := playlistDirectoryFilters[r.Filter]
	if key == "" || !known || (r.Direction != "asc" && r.Direction != "desc") || r.Limit < 1 || r.Limit > 100 {
		return out, requestinput.New("invalid playlist directory query")
	}
	filterArgs := func() []any {
		switch r.Filter {
		case "mine", "shared":
			return []any{a.Authority, a.AccountID, a.ProfileID}
		case "pinned":
			return []any{actorKey(a)}
		}
		return nil
	}
	ctx := s.Context()
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	rev, e := directoryRevision(tx, a)
	if e != nil {
		return out, e
	}
	out.Revision = rev
	actions := []string{}
	var owned int
	if err := tx.QueryRow(`SELECT count(*) FROM catalog_playlists WHERE owner_authority=? AND owner_account=? AND owner_profile=? AND deleted=0`, a.Authority, a.AccountID, a.ProfileID).Scan(&owned); err != nil {
		return out, err
	}
	if owned < 10000 {
		actions = append(actions, "create_playlist")
	}
	out.Actions = &actions
	scope := cursorScope{Profile: a.ProfileID, View: "playlists", Viewer: r.ViewerFence, Sort: r.Sort, Direction: r.Direction, Category: r.Filter, Limit: r.Limit}
	visible := ` FROM catalog_playlists p WHERE p.deleted=0 AND p.id IN(SELECT owned.id FROM catalog_playlists owned WHERE owned.owner_authority=? AND owned.owner_account=? AND owned.owner_profile=? AND owned.deleted=0 UNION SELECT shared.id FROM playlist_shares share JOIN catalog_playlists shared ON shared.token=share.playlist_id WHERE share.authority=? AND share.account_id=? AND share.profile_id=?)`
	visibleArgs := []any{a.Authority, a.AccountID, a.ProfileID, a.Authority, a.AccountID, a.ProfileID}
	// One pass counts every group the filter offers, over the viewer's own playlists.
	var all, mine, pinned int
	if e = tx.QueryRow(`SELECT count(*),COALESCE(sum(p.owner_authority=? AND p.owner_account=? AND p.owner_profile=?),0),COALESCE(sum(EXISTS(SELECT 1 FROM saved_pins pin WHERE pin.owner_key=? AND pin.kind='playlist' AND pin.resource_id=p.token AND pin.pinned=1)),0)`+visible, append([]any{a.Authority, a.AccountID, a.ProfileID, actorKey(a)}, visibleArgs...)...).Scan(&all, &mine, &pinned); e != nil {
		return out, e
	}
	out.Filters = []ContentFilter{{ID: "filter", LabelKey: "playlists.filter", Options: []ContentFilterOption{{ID: "all", Label: "All", Count: all}, {ID: "mine", Label: "Mine", Count: mine}, {ID: "shared", Label: "Shared with me", Count: all - mine}, {ID: "pinned", Label: "Pinned", Count: pinned}}}}
	count := map[string]int{"all": all, "mine": mine, "shared": all - mine, "pinned": pinned}[r.Filter]
	where := visible + ` AND ` + predicate
	args := append(append([]any{}, visibleArgs...), filterArgs()...)
	op := ">"
	if r.Direction == "desc" {
		op = "<"
	}
	if r.Cursor != "" {
		c, err := s.decodeRevisionCursor(r.Cursor, scope, rev)
		if err != nil {
			return out, err
		}
		where += ` AND (` + key + op + `? OR (` + key + `=? AND p.token` + op + `?))`
		args = append(args, c.Value, c.Value, c.ID)
	}
	args = append(args, r.Limit+1)
	// The card's own two facts: this viewer's pin (the same viewer-scoped predicate the
	// Pinned filter reads, bound once here) and the playlist's own last change, which is its
	// creation until an edit writes updated_at. The sort keeps ordering by that same value.
	rows, e := tx.Query(`SELECT p.token,p.name,p.summary,p.entry_count,`+key+` AS sort_value,`+
		`EXISTS(SELECT 1 FROM saved_pins pin WHERE pin.owner_key=? AND pin.kind='playlist' AND pin.resource_id=p.token AND pin.pinned=1),`+
		`COALESCE(NULLIF(p.updated_at,''),p.created_at)`+where+` ORDER BY `+key+` `+r.Direction+`,p.token `+r.Direction+` LIMIT ?`, append([]any{actorKey(a)}, args...)...)
	if e != nil {
		return out, e
	}
	entries := []ContentEntry{}
	values := []string{}
	for rows.Next() {
		var v ContentEntry
		var n int
		var value string
		if e = rows.Scan(&v.ID, &v.Title, &v.Overview, &n, &value, &v.Pinned, &v.LastChanged); e != nil {
			rows.Close()
			return out, e
		}
		values = append(values, value)
		v.Kind = "playlist"
		v.Count = &n
		v.Navigation = &ContentNavigation{View: "playlist", EntityID: v.ID}
		entries = append(entries, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	next := ""
	if len(entries) > r.Limit {
		entries = entries[:r.Limit]
		v := entries[len(entries)-1]
		next, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: values[r.Limit-1], ID: v.ID, Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
		if e != nil {
			return out, e
		}
	}
	if len(entries) > 0 {
		out.Sections = append(out.Sections, contentSection("playlists", "grid", "Playlists", entries, count, next))
	} else {
		out.Empty = &ContentHeading{Key: "playlists.empty", Fallback: "No playlists yet."}
	}
	if e = gated.Commit(); e != nil {
		return out, e
	}
	after, e := directoryRevision(s.read(), a)
	if e != nil {
		return out, e
	}
	if after != rev {
		return out, ErrStaleContinuation
	}
	return out, nil
}
func (s *Service) PlaylistContent(r ContentRequest, a ResourceActor, libraries []string) (ContentEnvelope, error) {
	if r.Profile != "" && r.Profile != r.Viewer.Profile || r.ViewerFence != "" && r.ViewerFence != r.Viewer.Fence {
		return ContentEnvelope{}, ErrCursor
	}
	r = r.scoped()
	libraries = r.Viewer.Libraries
	if err := s.prepareViewer(r.Viewer); err != nil {
		return ContentEnvelope{}, err
	}
	if r.Limit == 0 {
		r.Limit = 40
	}
	r.View = "playlist"
	query, sort, invalid := ResourceContentQuery{Sort: r.Sort, Direction: r.Direction, Filter: r.Filter}.normalize()
	r.Sort, r.Direction, r.Filter = query.Sort, query.Direction, query.Filter
	out := resourceEnvelope(r, "Playlist")
	out.Query.Category = r.Filter
	out.Sorts = resourceContentSortOptions()
	if invalid != nil {
		return out, invalid
	}
	if r.Limit < 1 || r.Limit > 100 {
		return out, requestinput.New("invalid playlist page limit")
	}
	role, resourceRevision, deleted, e := catalogPlaylistRole(s.read(), r.EntityID, a)
	if e != nil {
		return out, e
	}
	if deleted || role == "" {
		return out, sql.ErrNoRows
	}
	resource := Playlist{ServerID: r.ServerID, ViewerFence: r.ViewerFence, ID: r.EntityID, Role: role, Revision: resourceRevision}
	var playlistRow int64
	if e = s.read().QueryRow(`SELECT id,name,summary,entry_count FROM catalog_playlists WHERE token=? AND deleted=0`, r.EntityID).Scan(&playlistRow, &resource.Name, &resource.Summary, &resource.EntryCount); e != nil {
		return out, e
	}
	out.Heading = ContentHeading{Key: "playlist.title", Fallback: resource.Name}
	out.PlaylistRevision = &resource.Revision
	rev, e := s.homeRevision(libraries, r.Profile)
	if e != nil {
		return out, e
	}
	rev.Catalog += resource.Revision
	out.Revision = rev
	scope := cursorScope{Profile: a.ProfileID, View: "playlist", Entity: r.EntityID, Viewer: r.ViewerFence, Sort: r.Sort, Direction: r.Direction, Category: r.Filter, Limit: r.Limit}
	// The occurrences, each with its item only when this viewer may see it.
	visibility, visibleArgs := r.Viewer.itemVisibilitySQL("e.item_id")
	from := ` FROM (SELECT ? AS profile_id) viewer JOIN catalog_playlist_entries e LEFT JOIN catalog_entities i ON i.id=e.item_id AND ` + visibility + ` WHERE e.playlist_id=?`
	fromArgs := append(append([]any{r.Profile}, visibleArgs...), playlistRow)
	counts := make([]any, len(resourceContentFilterIDs))
	counted := make([]int, len(counts))
	for n := range counts {
		counts[n] = &counted[n]
	}
	if e = s.read().QueryRow(resourceContentCountsSQL()+from, fromArgs...).Scan(counts...); e != nil {
		return out, e
	}
	out.Filters = resourceContentFilterOptions(counted)
	total := counted[0]
	for n, f := range resourceContentFilterIDs {
		if f == r.Filter {
			total = counted[n]
		}
	}
	order := newResourceContentOrder(query, sort, "e.id", true)
	where := ` AND ` + resourceContentFilterSQL(r.Filter)
	args := append([]any{}, fromArgs...)
	start := 0
	if r.Cursor != "" {
		c, err := s.decodeRevisionCursor(r.Cursor, scope, rev)
		if err != nil {
			return out, err
		}
		seek, seekArgs, err := order.after(c.Keys)
		if err != nil {
			return out, err
		}
		where += ` AND ` + seek
		args = append(args, seekArgs...)
		start = c.Start
	}
	args = append(args, r.Limit+1)
	rows, e := s.read().Query(`SELECT e.token,CASE WHEN i.id IS NULL THEN NULL ELSE pid(i.public_id) END,`+order.selectSQL()+from+where+` ORDER BY `+order.orderBy+` LIMIT ?`, args...)
	if e != nil {
		return out, e
	}
	type occurrence struct {
		token string
		item  sql.NullString
		keys  []any
	}
	selected := []occurrence{}
	for rows.Next() {
		v := occurrence{keys: make([]any, len(order.columns))}
		dest := []any{&v.token, &v.item}
		for n := range v.keys {
			dest = append(dest, &v.keys[n])
		}
		if e = rows.Scan(dest...); e != nil {
			rows.Close()
			return out, e
		}
		selected = append(selected, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	next, following := "", ""
	if len(selected) > r.Limit {
		following = selected[r.Limit].token
		selected = selected[:r.Limit]
		last := selected[len(selected)-1]
		next, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Keys: resourceOrderKeys(last.keys), Start: start + r.Limit, Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
		if e != nil {
			return out, e
		}
	}
	var witness *ContentOrderWitness
	if query.manual() && len(selected) > 0 {
		witness = &ContentOrderWitness{Next: following}
		if r.Cursor != "" {
			first := selected[0].keys
			if e = s.read().QueryRow(`SELECT token FROM catalog_playlist_entries WHERE playlist_id=? AND (order_key,id)<(?,?) ORDER BY order_key DESC,id DESC LIMIT 1`, playlistRow, first[0], first[1]).Scan(&witness.Previous); e != nil && !errors.Is(e, sql.ErrNoRows) {
				return out, e
			}
		}
		if following == "" {
			witness.Last = selected[len(selected)-1].token
		} else if e = s.read().QueryRow(`SELECT token FROM catalog_playlist_entries WHERE playlist_id=? ORDER BY order_key DESC,id DESC LIMIT 1`, playlistRow).Scan(&witness.Last); e != nil {
			return out, e
		}
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, v := range selected {
		if v.item.Valid && !seen[v.item.String] {
			ids = append(ids, v.item.String)
			seen[v.item.String] = true
		}
	}
	items, e := s.mediaPage(r.Profile, ids, false)
	if e != nil {
		return out, e
	}
	media := map[string]ContentEntry{}
	for _, v := range items {
		entry := contentItem(v)
		if !v.Available {
			entry.Playback = nil
		}
		media[v.ID] = entry
	}
	entries := []ContentEntry{}
	for _, v := range selected {
		hidden := true
		entry := ContentEntry{ID: v.token, Kind: "playlist_entry", Hidden: &hidden}
		if item, ok := media[v.item.String]; v.item.Valid && ok {
			hidden = false
			entry.Media = &item
		}
		entries = append(entries, entry)
	}
	if len(entries) > 0 {
		section := contentSection("entries", "list", resource.Name, entries, total, next)
		section.Start, section.Order = start, witness
		out.Sections = append(out.Sections, section)
	} else {
		out.Empty = &ContentHeading{Key: "playlist.empty", Fallback: "This playlist has no entries yet."}
	}
	_, afterResource, deleted, e := catalogPlaylistRole(s.read(), r.EntityID, a)
	if e != nil {
		return out, e
	}
	after, e := s.homeRevision(libraries, r.Profile)
	if e != nil {
		return out, e
	}
	after.Catalog += afterResource
	if deleted || afterResource != resourceRevision || after != rev {
		return out, ErrStaleContinuation
	}
	return out, nil
}
