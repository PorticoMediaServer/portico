package catalog

import (
	"encoding/json"
	"strconv"
	"time"
)

func (s *Service) workspaceEpisodes(r ShowWorkspaceRequest, w ShowWorkspace, _ int, seasonNumber int, revision ContentRevision) (ContentEnvelope, error) {
	show := w.Show.ID
	season := ""
	view, entity, section, title := "show", show, "unassigned_absolute", "Unassigned absolute episodes"
	if w.Selected.SeasonID != nil {
		season = *w.Selected.SeasonID
		view, entity, section, title = "season", season, "episodes", seasonTitle(seasonNumber)
	}
	out := ContentEnvelope{Scope: ContentScope{r.ServerID, r.Library, w.Show.LibraryKind, view, entity, r.ViewerFence}, Revision: revision, Heading: ContentHeading{Key: "entity.title", Fallback: w.Show.Title + " · " + title}, Navigation: []ContentTab{{"discover", "library.discover", "discover"}, {"browse", "library.shows", "browse"}}, Query: ContentQuery{"episode", r.EpisodeDirection, "", "", r.Limit, "none"}, Sorts: []ContentSort{{"episode", "sort.episode", []string{"asc", "desc"}}}, Filters: []ContentFilter{}, Sections: []ContentSection{}}
	scope := cursorScope{Library: r.Library, Viewer: r.ViewerFence, Profile: r.Profile, View: "show_workspace_episodes", Entity: show, Section: section + ":" + season + ":" + r.PlayState, Search: r.EpisodeID, Sort: "episode", Direction: r.EpisodeDirection, Limit: r.Limit}
	// A full (episode number, public ID) tuple prevents tied numbers from
	// disappearing at a page boundary. Desc reverses both tuple components.
	type episodePosition struct {
		Number int    `json:"number"`
		ID     string `json:"id"`
	}
	var after *episodePosition
	if r.Cursor != "" {
		c, e := s.decodeDetailCursor(r.Cursor, scope, revision)
		if e != nil {
			return out, e
		}
		var position episodePosition
		if json.Unmarshal([]byte(c.Value), &position) != nil || position.Number < 0 || position.ID == "" {
			return out, ErrCursor
		}
		after = &position
	}
	where := `sh.public_id=pid_blob(?) AND ep.season_id IS NULL AND ep.numbering='absolute'`
	args := []any{show}
	if season != "" {
		where = `se.public_id=pid_blob(?)`
		args = []any{season}
	}
	where += ` AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=item.id)`
	restriction, bound := ItemRestrictionSQL("item.id", r.Viewer.EffectiveRestrictions())
	where += ` AND ` + restriction
	args = append(args, bound...)
	stateSQL, stateArgs, err := episodePlayStateSQL(r.Profile, r.PlayState)
	if err != nil {
		return out, err
	}
	where += stateSQL
	args = append(args, stateArgs...)
	from := ` FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_entities sh ON sh.id=ep.show_id LEFT JOIN catalog_entities se ON se.id=ep.season_id WHERE `
	var count int
	if e := s.read().QueryRow(`SELECT count(*)`+from+where, args...).Scan(&count); e != nil {
		return out, e
	}
	pageWhere := where
	pageArgs := append([]any{}, args...)
	comparison, direction := ">", "ASC"
	if r.EpisodeDirection == "desc" {
		comparison, direction = "<", "DESC"
	}
	if after != nil {
		pageWhere += ` AND (ep.number,item.public_id) ` + comparison + ` (?,pid_blob(?))`
		pageArgs = append(pageArgs, after.Number, after.ID)
	} else if r.EpisodeID != "" {
		pageWhere += ` AND (ep.number,item.public_id) ` + comparison + `= (SELECT target.number,target_item.public_id FROM catalog_episodes target JOIN catalog_entities target_item ON target_item.id=target.entity_id WHERE target_item.public_id=pid_blob(?))`
		pageArgs = append(pageArgs, r.EpisodeID)
	}
	pageArgs = append(pageArgs, r.Limit+1)
	rows, e := s.read().Query(`SELECT pid(item.public_id),ep.number`+from+pageWhere+` ORDER BY ep.number `+direction+`,item.public_id `+direction+` LIMIT ?`, pageArgs...)
	if e != nil {
		return out, e
	}
	ids := []string{}
	numbers := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if e = rows.Scan(&id, &n); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
		numbers[id] = n
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	more := len(ids) > r.Limit
	if more {
		ids = ids[:r.Limit]
	}
	items, e := s.mediaPage(r.Profile, ids, false)
	if e != nil {
		return out, e
	}
	entries := contentItems(items)
	airDates, e := s.episodeAirDates(ids)
	if e != nil {
		return out, e
	}
	for i := range entries {
		entries[i].AirDate = airDates[entries[i].ID]
		entries[i].Subtitle = "Episode " + strconv.Itoa(numbers[entries[i].ID])
		if season == "" {
			entries[i].Subtitle = "Absolute episode " + strconv.Itoa(numbers[entries[i].ID])
		}
	}
	next := ""
	if more {
		position, err := json.Marshal(episodePosition{Number: numbers[ids[len(ids)-1]], ID: ids[len(ids)-1]})
		if err != nil {
			return out, err
		}
		next, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: string(position), Expires: time.Now().Add(30 * time.Minute).Unix()}, revision)
		if e != nil {
			return out, e
		}
	}
	if len(entries) > 0 || r.Cursor != "" {
		out.Sections = append(out.Sections, contentSection(section, "list", title, entries, count, next))
	} else {
		out.Empty = &ContentHeading{Key: "content.empty", Fallback: "No episodes in this group."}
	}
	return out, nil
}

// episodeAirDates is each episode's air date: the date in its file name, else
// the TVDB aired date it was matched with, else its accepted screen record's
// date. One bounded read for the page.
func (s *Service) episodeAirDates(ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	// The file-name date is the published compact episode's air_date (days
	// since the epoch); the TVDB and accepted screen dates are provider
	// evidence, keyed by the item's entity id.
	rows, err := s.read().Query(`SELECT pid(item.public_id),COALESCE(strftime('%Y-%m-%d',ep.air_date*86400,'unixepoch'),json_extract(pe.payload,'$.aired'),(SELECT json_extract(f.value,'$') FROM screen_metadata_fields f WHERE f.target_kind='item' AND f.target_id=item.id AND f.field='date'),'')
 FROM json_each(?) j CROSS JOIN catalog_entities item ON item.public_id=pid_blob(j.value) JOIN catalog_episodes ep ON ep.entity_id=item.id
 LEFT JOIN provider_evidence pe ON pe.item_id=item.id AND pe.provider='tvdb'`, idsJSON(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, date string
		if err = rows.Scan(&id, &date); err != nil {
			return nil, err
		}
		if len(date) >= 10 {
			out[id] = date[:10]
		}
	}
	return out, rows.Err()
}
