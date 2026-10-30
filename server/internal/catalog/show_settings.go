package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// ErrShowSettingsConflict: another edit saved first.
var ErrShowSettingsConflict = errors.New("show settings changed; reload before saving")

// ShowSettings is how one show's episodes are presented (Spec — Title Pages
// §4). The server applies them in the show workspace; clients draw what it
// sends. Revision fences the owner's edit.
type ShowSettings struct {
	HideSeasons       bool `json:"hideSeasons"`
	AbsoluteNumbering bool `json:"absoluteNumbering"`
	Ranges            bool `json:"ranges"`
	NewestFirst       bool `json:"newestFirst"`
	Revision          int  `json:"revision"`
}

// flat reports whether the show's episodes are one list across seasons.
func (v ShowSettings) flat() bool { return v.HideSeasons || v.Ranges }

// active reports whether any setting differs from the default presentation.
func (v ShowSettings) active() bool {
	return v.HideSeasons || v.AbsoluteNumbering || v.Ranges || v.NewestFirst
}

func (v ShowSettings) fingerprint() string {
	bit := func(on bool) string {
		if on {
			return "1"
		}
		return "0"
	}
	return bit(v.HideSeasons) + bit(v.AbsoluteNumbering) + bit(v.Ranges) + bit(v.NewestFirst)
}

// showRangeSize is how many episodes one range of a long show holds.
const showRangeSize = 100

type settingsReader interface {
	QueryRow(query string, args ...any) *sql.Row
}

func readShowSettings(db settingsReader, show string) (ShowSettings, error) {
	out := ShowSettings{}
	e := db.QueryRow(`SELECT hide_seasons,absolute_numbering,ranges,newest_first,revision FROM catalog_show_settings WHERE entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, show).
		Scan(&out.HideSeasons, &out.AbsoluteNumbering, &out.Ranges, &out.NewestFirst, &out.Revision)
	if errors.Is(e, sql.ErrNoRows) {
		e = db.QueryRow(`SELECT COALESCE(p.episode_numbering,'season')='absolute' FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id LEFT JOIN library_preferences p ON p.library_id=cl.library_id WHERE e.public_id=pid_blob(?)`, show).Scan(&out.AbsoluteNumbering)
		if errors.Is(e, sql.ErrNoRows) {
			e = nil
		}
		return out, e
	}
	return out, e
}

// SetShowSettings stores the owner's settings for one show. The expected
// revision is the one the settings were read at (0 for a show that has none).
func (s *Service) SetShowSettings(ctx context.Context, library, show string, next ShowSettings, authorize func(*sql.Tx) error) (ShowSettings, error) {
	if show == "" || len(show) > 128 || library == "" || authorize == nil {
		return ShowSettings{}, ErrAdminQuery
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return ShowSettings{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = authorize(tx); e != nil {
		return ShowSettings{}, e
	}
	r, e := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if e != nil {
		return ShowSettings{}, e
	}
	if r != nil {
		return readShowSettings(tx, show)
	}
	var entity int64
	if e = tx.QueryRowContext(ctx, `SELECT s.id FROM catalog_entities s JOIN catalog_shows d ON d.entity_id=s.id JOIN catalog_libraries l ON l.id=s.library_id WHERE s.public_id=pid_blob(?) AND l.library_id=? AND s.retired=0`, show, library).Scan(&entity); e != nil {
		return ShowSettings{}, e
	}
	var current int
	e = tx.QueryRowContext(ctx, `SELECT revision FROM catalog_show_settings WHERE entity_id=?`, entity).Scan(&current)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return ShowSettings{}, e
	}
	if current != next.Revision {
		return ShowSettings{}, ErrShowSettingsConflict
	}
	next.Revision = current + 1
	if _, e = tx.ExecContext(ctx, `INSERT INTO catalog_show_settings(entity_id,hide_seasons,absolute_numbering,ranges,newest_first,revision) VALUES(?,?,?,?,?,?)
 ON CONFLICT(entity_id) DO UPDATE SET hide_seasons=excluded.hide_seasons,absolute_numbering=excluded.absolute_numbering,ranges=excluded.ranges,newest_first=excluded.newest_first,revision=excluded.revision`,
		entity, next.HideSeasons, next.AbsoluteNumbering, next.Ranges, next.NewestFirst, next.Revision); e != nil {
		return ShowSettings{}, e
	}
	if e = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: show, Revision: int64(next.Revision)}, time.Now().UnixMilli()); e != nil {
		return ShowSettings{}, e
	}
	return next, gated.Commit()
}

// rankedEpisode is one visible episode of a show in aired order. Specials
// (season 0) come after every numbered season and have no absolute number.
type rankedEpisode struct {
	id       string
	season   string
	number   int
	special  bool
	absolute int
}

// showRankedEpisodes reads a show's visible episodes in aired order. It is a
// pass over one show's episodes, whatever the library's size.
func (s *Service) showRankedEpisodes(r ShowWorkspaceRequest, show string) ([]rankedEpisode, error) {
	restriction, bound := ItemRestrictionSQL("item.id", r.Viewer.EffectiveRestrictions())
	stateSQL, stateArgs, err := episodePlayStateSQL(r.Profile, r.PlayState)
	if err != nil {
		return nil, err
	}
	restriction += stateSQL
	args := append([]any{show}, bound...)
	args = append(args, stateArgs...)
	rows, e := s.read().Query(`SELECT pid(item.public_id),COALESCE(pid(se.public_id),''),ep.number,COALESCE(sd.number,1)
 FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_entities sh ON sh.id=ep.show_id
 LEFT JOIN catalog_entities se ON se.id=ep.season_id LEFT JOIN catalog_seasons sd ON sd.entity_id=ep.season_id
 WHERE sh.public_id=pid_blob(?) AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=item.id) AND `+restriction+`
 ORDER BY (COALESCE(sd.number,1)=0),COALESCE(sd.number,1),ep.number,item.id`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []rankedEpisode{}
	absolute := 0
	for rows.Next() {
		var row rankedEpisode
		var seasonNumber int
		if e = rows.Scan(&row.id, &row.season, &row.number, &seasonNumber); e != nil {
			return nil, e
		}
		row.special = seasonNumber == 0
		if !row.special {
			absolute++
			row.absolute = absolute
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// showEpisodeGroups are the groups a flat show offers in place of seasons:
// ranges of a hundred for a long show, then its specials; one group otherwise.
func showEpisodeGroups(settings ShowSettings, ranked []rankedEpisode) []ShowWorkspaceGroup {
	regular, specials := 0, 0
	for _, row := range ranked {
		if row.special {
			specials++
		} else {
			regular++
		}
	}
	if !settings.Ranges || regular <= showRangeSize {
		if len(ranked) == 0 {
			return []ShowWorkspaceGroup{}
		}
		return []ShowWorkspaceGroup{{ID: "all", Title: "Episodes", Count: len(ranked)}}
	}
	out := []ShowWorkspaceGroup{}
	for start := 0; start < regular; start += showRangeSize {
		end := min(start+showRangeSize, regular)
		out = append(out, ShowWorkspaceGroup{ID: "range:" + strconv.Itoa(start), Title: fmt.Sprintf("%d–%d", start+1, end), Count: end - start})
	}
	if specials > 0 {
		out = append(out, ShowWorkspaceGroup{ID: "specials", Title: seasonTitle(0), Count: specials})
	}
	return out
}

// groupEpisodes is the slice of the ranked list one group or season holds.
func groupEpisodes(ranked []rankedEpisode, group, season string) []rankedEpisode {
	out := []rankedEpisode{}
	start := -1
	if strings.HasPrefix(group, "range:") {
		start, _ = strconv.Atoi(strings.TrimPrefix(group, "range:"))
	}
	for _, row := range ranked {
		switch {
		case season != "":
			if row.season == season {
				out = append(out, row)
			}
		case group == "all":
			out = append(out, row)
		case group == "specials":
			if row.special {
				out = append(out, row)
			}
		case start >= 0:
			if !row.special && row.absolute > start && row.absolute <= start+showRangeSize {
				out = append(out, row)
			}
		}
	}
	return out
}

// groupOf names the group that holds an episode in a flat show.
func groupOf(groups []ShowWorkspaceGroup, ranked []rankedEpisode, episode string) string {
	for _, row := range ranked {
		if row.id != episode {
			continue
		}
		for _, group := range groups {
			if group.ID == "all" || group.ID == "specials" && row.special {
				return group.ID
			}
			if strings.HasPrefix(group.ID, "range:") && !row.special {
				start, _ := strconv.Atoi(strings.TrimPrefix(group.ID, "range:"))
				if row.absolute > start && row.absolute <= start+showRangeSize {
					return group.ID
				}
			}
		}
	}
	return ""
}

// workspaceSettingEpisodes is the episode page of a show whose owner changed
// how it is presented: the group's (or season's) episodes in the chosen
// direction, a page at a time by position, numbered across seasons when asked.
func (s *Service) workspaceSettingEpisodes(r ShowWorkspaceRequest, w ShowWorkspace, ranked []rankedEpisode, seasonNumber int, revision ContentRevision) (ContentEnvelope, error) {
	settings := w.Settings
	show := w.Show.ID
	season := ""
	view, entity, section, title := "show", show, w.Selected.Group, "Episodes"
	if w.Selected.SeasonID != nil {
		season = *w.Selected.SeasonID
		view, entity, section, title = "season", season, "episodes", seasonTitle(seasonNumber)
	}
	for _, group := range w.Groups {
		if group.ID == w.Selected.Group {
			title = group.Title
		}
	}
	direction := r.EpisodeDirection
	out := ContentEnvelope{Scope: ContentScope{r.ServerID, r.Library, w.Show.LibraryKind, view, entity, r.ViewerFence}, Revision: revision, Heading: ContentHeading{Key: "entity.title", Fallback: w.Show.Title + " · " + title}, Navigation: []ContentTab{{"discover", "library.discover", "discover"}, {"browse", "library.shows", "browse"}}, Sorts: []ContentSort{{"episode", "sort.episode", []string{"asc", "desc"}}}, Filters: []ContentFilter{}, Sections: []ContentSection{}, Query: ContentQuery{"episode", direction, "", "", r.Limit, "none"}}
	rows := groupEpisodes(ranked, w.Selected.Group, season)
	if direction == "desc" {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
	scope := cursorScope{Library: r.Library, Viewer: r.ViewerFence, Profile: r.Profile, View: "show_workspace_episodes", Entity: show, Section: "settings:" + settings.fingerprint() + ":" + section + ":" + season + ":" + r.PlayState, Search: r.EpisodeID, Sort: "episode", Direction: direction, Limit: r.Limit}
	start := 0
	if r.EpisodeID != "" {
		for index, row := range rows {
			if row.id == r.EpisodeID {
				start = index
			}
		}
	}
	if r.Cursor != "" {
		c, e := s.decodeDetailCursor(r.Cursor, scope, revision)
		if e != nil {
			return out, e
		}
		start, e = strconv.Atoi(c.Value)
		if e != nil || start < 0 {
			return out, ErrCursor
		}
	}
	if start > len(rows) {
		start = len(rows)
	}
	page := rows[start:min(start+r.Limit, len(rows))]
	ids := make([]string, 0, len(page))
	for _, row := range page {
		ids = append(ids, row.id)
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
	byID := map[string]rankedEpisode{}
	for _, row := range page {
		byID[row.id] = row
	}
	for i := range entries {
		row := byID[entries[i].ID]
		entries[i].AirDate = airDates[entries[i].ID]
		entries[i].Subtitle = "Episode " + strconv.Itoa(row.number)
		if settings.AbsoluteNumbering && !row.special {
			absolute := row.absolute
			entries[i].AbsoluteNumber = &absolute
			entries[i].Subtitle = "Episode " + strconv.Itoa(absolute)
		}
	}
	next := ""
	if start+len(page) < len(rows) {
		next, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: strconv.Itoa(start + len(page)), Expires: time.Now().Add(30 * time.Minute).Unix()}, revision)
		if e != nil {
			return out, e
		}
	}
	if len(entries) > 0 || r.Cursor != "" {
		out.Sections = append(out.Sections, contentSection(section, "list", title, entries, len(rows), next))
	} else {
		out.Empty = &ContentHeading{Key: "content.empty", Fallback: "No episodes in this group."}
	}
	return out, nil
}
