package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type listenDiscoverCursor struct {
	Offset int    `json:"offset"`
	Inner  string `json:"inner"`
	Now    int64  `json:"now"`
}
type listenDiscoverSpec struct {
	id, title, query, order string
	args                    []any
}

func (s *Service) musicDiscover(r ContentRequest, revision ContentRevision) ([]ContentSection, error) {
	now := s.recommendationNow(r.Now)
	var savedGeneration int64
	if err := s.read().QueryRow(`SELECT revision FROM saved_resource_generation WHERE id=1`).Scan(&savedGeneration); err != nil {
		return nil, err
	}
	scope := cursorScope{Library: r.Library, Profile: r.Profile, Viewer: r.ViewerFence, View: "listen_discover", Limit: r.Limit, Category: fmt.Sprint(savedGeneration)}
	selected := ""
	cursor := listenDiscoverCursor{Now: now.UnixMilli()}
	if r.Cursor != "" {
		c, e := s.contentContinuation(r.Cursor, scope)
		if e != nil {
			return nil, e
		}
		selected = c.Scope.Section
		if json.Unmarshal([]byte(c.Value), &cursor) != nil || cursor.Offset < 0 || cursor.Now <= 0 {
			return nil, ErrCursor
		}
		now = time.UnixMilli(cursor.Now)
	}
	visible, bind := listeningSectionVisibility("album", r.Viewer)
	visible = strings.ReplaceAll(visible, "projected.entity_id", "e.entity_id")
	visible = strings.ReplaceAll(visible, "WHERE album.id=", `WHERE EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=i.id AND av.available=1 AND av.retired=0) AND album.id=`)
	specs := []listenDiscoverSpec{
		{"new_albums", "New albums", `SELECT pid(entity.public_id) id FROM catalog_browse_rows e INDEXED BY catalog_browse_recent_works JOIN catalog_entities entity ON entity.id=e.entity_id WHERE e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.kind=6 AND e.recent_text IS NOT NULL AND e.recent_text>=? AND e.recent_text<=? AND ` + visible, `e.recent_text DESC,e.entity_id DESC`, append([]any{r.Library, now.Add(-7 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z"), now.UTC().Format("2006-01-02T15:04:05.000Z")}, bind...)},
	}
	if actor, ok := viewerFromPersonalKey(r.Profile); ok {
		itemVisible, itemArgs := r.Viewer.itemVisibilitySQL("item.id")
		query := `SELECT pid(item.public_id) id,count(*) plays FROM play_history h INDEXED BY play_history_listen_profile JOIN catalog_entities item ON item.id=h.item_id WHERE h.authority=? AND h.account_id=? AND h.profile_id=? AND h.started_ms>=? AND h.started_ms<=? AND h.completed=1 AND h.kind=7 AND item.kind=7 AND item.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ` + itemVisible + ` AND EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=item.id AND av.available=1 AND av.retired=0) GROUP BY h.item_id`
		specs = append(specs, listenDiscoverSpec{"most_played_songs", "Songs you play most", query, `plays DESC,id`, append([]any{actor.Authority, actor.AccountID, actor.ProfileID, now.Add(-30 * 24 * time.Hour).UnixMilli(), now.UnixMilli(), r.Library}, itemArgs...)})
	}
	byID := map[string]listenDiscoverSpec{}
	for _, spec := range specs {
		byID[spec.id] = spec
	}
	result := []ContentSection{}
	for _, id := range []string{"recently_played", "new_albums", "artists", "genres", "playlists", "most_played_songs"} {
		if selected != "" && selected != id {
			continue
		}
		sec := ContentSection{}
		next := listenDiscoverCursor{Now: cursor.Now}
		if id == "recently_played" {
			var err error
			sec, err = s.musicDiscoverRecent(r, cursor.Offset, now)
			if err != nil {
				return nil, err
			}
			if sec.NextCursor != "" {
				next.Offset = cursor.Offset + r.Limit
				sec.NextCursor = ""
			}
		} else if id == "artists" || id == "genres" {
			page, err := s.BrowseEntities(r.Viewer, BrowseRequest{ServerID: r.ServerID, Library: r.Library, Profile: r.Profile, ViewerFence: r.ViewerFence, Pivot: id, Limit: r.Limit, Cursor: cursor.Inner})
			if err != nil {
				return nil, err
			}
			title := "Artists"
			if id == "genres" {
				title = "Genres"
			}
			sec = contentSection(id, "rail", title, page.Entries, page.PageInfo.Total, "")
			next.Inner = page.PageInfo.NextCursor

		} else if id == "playlists" {
			var err error
			sec, err = s.musicDiscoverPlaylists(r, cursor.Offset)
			if err != nil {
				return nil, err
			}
			if sec.NextCursor != "" {
				next.Offset = cursor.Offset + r.Limit
				sec.NextCursor = ""
			}
		} else if spec, ok := byID[id]; ok {
			sec = contentSection(spec.id, "rail", spec.title, []ContentEntry{}, 0, "")
			sec.Start = cursor.Offset
			if err := s.read().QueryRow(`SELECT count(*) FROM (`+spec.query+`)`, spec.args...).Scan(&sec.TotalCount); err != nil {
				return nil, err
			}
			cols := "id,0"
			if id == "most_played_songs" {
				cols = "id,plays"
			}
			rows, err := s.read().Query(`SELECT `+cols+` FROM (`+spec.query+` ORDER BY `+spec.order+` LIMIT ? OFFSET ?)`, append(append([]any{}, spec.args...), r.Limit+1, cursor.Offset)...)
			if err != nil {
				return nil, err
			}
			ids := []string{}
			plays := map[string]int{}
			for rows.Next() {
				var id string
				var n int
				if err = rows.Scan(&id, &n); err != nil {
					break
				}
				ids = append(ids, id)
				plays[id] = n
			}
			if e := rows.Err(); err == nil {
				err = e
			}
			rows.Close()
			if err != nil {
				return nil, err
			}
			if len(ids) > r.Limit {
				ids = ids[:r.Limit]
				next.Offset = cursor.Offset + r.Limit
			}
			sec.Entries, err = s.homeEngineEntries(r.Profile, ids)
			if err != nil {
				return nil, err
			}
			if id == "most_played_songs" {
				for i := range sec.Entries {
					n := plays[sec.Entries[i].ID]
					sec.Entries[i].Plays = &n
				}
			}
		} else {
			continue
		}
		sec.Start = cursor.Offset
		if err := s.listeningPageFavorites(r.Profile, sec.Entries); err != nil {
			return nil, err
		}
		if next.Inner != "" || next.Offset > 0 {
			raw, _ := json.Marshal(next)
			cs := scope
			cs.Section = id
			var err error
			sec.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: cs, Value: string(raw), Expires: time.Now().Add(30 * time.Minute).Unix()}, revision)
			if err != nil {
				return nil, err
			}
		}
		if len(sec.Entries) > 0 || selected != "" {
			result = append(result, sec)
		}
	}
	if selected != "" && len(result) == 0 {
		return nil, ErrCursor
	}
	return result, nil
}

func (s *Service) musicDiscoverPlaylists(r ContentRequest, offset int) (ContentSection, error) {
	sec := contentSection("playlists", "rail", "Playlists", []ContentEntry{}, 0, "")
	actor, ok := viewerFromPersonalKey(r.Profile)
	if !ok {
		return sec, nil
	}
	visible, bind := r.Viewer.itemVisibilitySQL("item.id")
	acl := `WITH mine(id) AS (SELECT id FROM catalog_playlists INDEXED BY catalog_playlists_owner WHERE owner_authority=? AND owner_account=? AND owner_profile=? AND deleted=0 UNION SELECT p.id FROM playlist_shares sh INDEXED BY playlist_shares_listen_actor JOIN catalog_playlists p ON p.token=sh.playlist_id WHERE sh.authority=? AND sh.account_id=? AND sh.profile_id=? AND p.deleted=0) `
	member := ` FROM catalog_playlist_entries pe JOIN catalog_entities item ON item.id=pe.item_id WHERE pe.playlist_id=p.id AND item.kind=7 AND item.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ` + visible + ` AND EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=item.id AND av.available=1 AND av.retired=0)`
	from := ` FROM mine JOIN catalog_playlists p ON p.id=mine.id WHERE EXISTS(SELECT 1` + member + `)`
	args := append([]any{actor.Authority, actor.AccountID, actor.ProfileID, actor.Authority, actor.AccountID, actor.ProfileID, r.Library}, bind...)
	if err := s.read().QueryRow(acl+`SELECT count(*)`+from, args...).Scan(&sec.TotalCount); err != nil {
		return sec, err
	}
	direction := "ASC"
	if r.Direction == "desc" {
		direction = "DESC"
	}
	rows, err := s.read().Query(acl+`SELECT p.token,p.name`+from+` ORDER BY p.name COLLATE NOCASE `+direction+`,p.id `+direction+` LIMIT ? OFFSET ?`, append(args, r.Limit+1, offset)...)
	if err != nil {
		return sec, err
	}
	for rows.Next() {
		var e ContentEntry
		if err = rows.Scan(&e.ID, &e.Title); err != nil {
			break
		}
		e.Kind = "playlist"
		e.LibraryID = r.Library
		e.Navigation = &ContentNavigation{View: "playlist", EntityID: e.ID}
		sec.Entries = append(sec.Entries, e)
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return sec, err
	}
	if len(sec.Entries) > r.Limit {
		sec.Entries = sec.Entries[:r.Limit]
		sec.NextCursor = "more"
	}
	for i := range sec.Entries {
		e := &sec.Entries[i]
		params := append([]any{e.ID, r.Library}, bind...)
		// A playlist may mix media and libraries. Only the permitted music
		// members in this library supply its count and cover samples.
		where := strings.Replace(member, "pe.playlist_id=p.id", "pe.playlist_id=(SELECT id FROM catalog_playlists WHERE token=?)", 1)
		var n int
		if err = s.read().QueryRow(`SELECT count(*)`+where, params...).Scan(&n); err != nil {
			return sec, err
		}
		e.Count = &n
		covers, err := s.read().Query(`SELECT pid(item.public_id)`+where+` AND EXISTS(SELECT 1 FROM catalog_item_details d WHERE d.entity_id=item.id AND d.poster_url<>'') ORDER BY pe.order_key,pe.id LIMIT 4`, params...)
		if err != nil {
			return sec, err
		}
		for covers.Next() {
			var id string
			if err = covers.Scan(&id); err != nil {
				break
			}
			e.ArtworkPaths = append(e.ArtworkPaths, "/v1/items/"+id+"/art/poster")
		}
		if x := covers.Err(); err == nil {
			err = x
		}
		covers.Close()
		if err != nil {
			return sec, err
		}
		if err = describeItemArtIn(s, e.ArtworkPaths, func(p *string) *string { return p }); err != nil {
			return sec, err
		}
	}
	return sec, nil
}

func (s *Service) listeningPageFavorites(profile string, entries []ContentEntry) error {
	ids := []string{}
	for _, e := range entries {
		if e.Kind == "song" {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.read().Query(`SELECT pid(e.public_id),COALESCE(p.favorite,0) FROM json_each(?) j JOIN catalog_entities e ON e.public_id=pid_blob(j.value) LEFT JOIN personal_items p ON p.item_id=e.id AND p.profile_id=?`, idsJSON(ids), profile)
	if err != nil {
		return err
	}
	favorites := map[string]bool{}
	for rows.Next() {
		var id string
		var value bool
		if err = rows.Scan(&id, &value); err != nil {
			break
		}
		favorites[id] = value
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return err
	}
	for i := range entries {
		if v, ok := favorites[entries[i].ID]; ok {
			value := v
			entries[i].Favorite = &value
		}
	}
	return nil
}
