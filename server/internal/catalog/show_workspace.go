package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadatalanguage"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"strings"
	"time"
)

var ErrShowContext = requestinput.New("show, season and episode context do not agree")

func (s *Service) showWorkspaceOnce(r ShowWorkspaceRequest) (ShowWorkspace, error) {
	out := ShowWorkspace{Scope: ShowWorkspaceScope{r.ServerID, r.Library, r.ViewerFence}, Seasons: []Season{}, Groups: []ShowWorkspaceGroup{}}
	if !r.Viewer.AllowsLibrary(r.Library) {
		return out, sql.ErrNoRows
	}
	if r.Profile != "" && r.Profile != r.Viewer.Profile || r.ViewerFence != "" && r.ViewerFence != r.Viewer.Fence {
		return out, ErrCursor
	}
	r.Profile, r.ViewerFence = r.Viewer.Profile, r.Viewer.Fence
	out.Scope.ViewerFence = r.ViewerFence
	if e := s.prepareViewer(r.Viewer); e != nil {
		return out, e
	}
	if _, _, err := episodePlayStateSQL(r.Profile, r.PlayState); err != nil {
		return out, err
	}
	if r.EpisodeDirection == "" {
		r.EpisodeDirection = "asc"
	}
	if r.EpisodeDirection != "asc" && r.EpisodeDirection != "desc" {
		return out, ErrShowContext
	}
	if r.Limit == 0 {
		r.Limit = 40
	}
	if r.SeasonLimit == 0 {
		r.SeasonLimit = 128
	}
	if r.Limit < 1 || r.Limit > 100 || r.SeasonLimit < 1 || r.SeasonLimit > 512 || r.Group != "" && !validShowGroup(r.Group) || r.SelectedSeasonID != "" && r.Group != "" {
		return out, ErrShowContext
	}
	before, e := s.ContentRevision(r.Library, r.Profile)
	if e != nil {
		return out, e
	}
	out.Revision = before
	show, season, number, e := s.workspaceTarget(r)
	if e != nil {
		if r.Cursor != "" || r.SeasonCursor != "" {
			return out, ErrStaleContinuation
		}
		return out, e
	}
	restriction, bound := ItemRestrictionSQL("visible_item.id", r.Viewer.EffectiveRestrictions())
	showArgs := []any{show, r.Library, compactcatalog.Show}
	visibleShow := ""
	if restriction != "1" {
		visibleShow = ` AND EXISTS(SELECT 1 FROM catalog_episodes visible_episode JOIN catalog_entities visible_item ON visible_item.id=visible_episode.entity_id WHERE visible_episode.show_id=s.id AND ` + restriction + `)`
		showArgs = append(showArgs, bound...)
	}
	e = s.read().QueryRow(`SELECT pid(s.public_id),l.library_id,s.title,s.year,CASE l.kind WHEN 2 THEN 'tv' WHEN 3 THEN 'anime' END,d.provider_match_status,d.original_title,d.tagline,d.content_rating,d.studio,d.network,d.country,COALESCE((SELECT json_extract(value,'$') FROM screen_metadata_fields WHERE target_kind='show' AND target_id=s.id AND field='overview'),c.overview,''),COALESCE((SELECT source_url FROM screen_metadata_fields WHERE target_kind='show' AND target_id=s.id AND field='overview'),CASE WHEN c.provider_id IS NULL THEN '' ELSE 'https://thetvdb.com/dereferrer/series/'||c.provider_id END),COALESCE((SELECT observed_at FROM screen_metadata_fields WHERE target_kind='show' AND target_id=s.id AND field='overview'),c.observed_at,'') FROM catalog_entities s JOIN catalog_shows d ON d.entity_id=s.id JOIN catalog_libraries l ON l.id=s.library_id LEFT JOIN tvdb_jobs j ON j.show_id=s.id LEFT JOIN tvdb_series_candidates c ON c.show_id=s.id AND c.provider_id=j.provider_id AND NOT EXISTS(SELECT 1 FROM screen_metadata_work sw WHERE sw.target_kind='show' AND sw.target_id=s.id AND sw.accepted_publication<>'') WHERE s.public_id=pid_blob(?) AND l.library_id=? AND s.kind=? AND l.retired=0 AND s.retired=0`+visibleShow, showArgs...).Scan(&out.Show.ID, &out.Show.LibraryID, &out.Show.Title, &out.Show.Year, &out.Show.LibraryKind, &out.Show.ProviderMatchStatus, &out.Show.OriginalTitle, &out.Show.Tagline, &out.Show.ContentRating, &out.Show.Studio, &out.Show.Network, &out.Show.Country, &out.Show.Overview, &out.Show.ProviderSourceURL, &out.Show.ProviderObservedAt)
	if e != nil {
		return out, e
	}
	counts, err := s.ContainerPersonalCounts(r.Viewer, "show", show)
	if err != nil {
		return out, err
	}
	watched, unwatched := int(counts.WatchedCount), int(counts.UnwatchedCount)
	out.Show.WatchedCount, out.Show.UnwatchedCount = &watched, &unwatched
	out.Show.EpisodeCount = counts.WatchedCount + counts.UnwatchedCount
	visibleAdded, addedArgs := r.Viewer.itemVisibilitySQL("item.id")
	if e = s.read().QueryRow(`SELECT COALESCE(min(d.added_text),'') FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_item_details d ON d.entity_id=item.id WHERE ep.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND `+visibleAdded, append([]any{show}, addedArgs...)...).Scan(&out.Show.AddedAt); e != nil {
		return out, e
	}
	if out.Show.LibraryKind != "tv" && out.Show.LibraryKind != "anime" {
		return out, ErrShowContext
	}
	if e = s.showHeroEvidence(show, r.Viewer, &out); e != nil {
		return out, e
	}

	arts, err := s.resolveArtwork([]artworkTarget{{"show", show}})
	if err != nil {
		return out, err
	}
	art := arts[artworkTarget{"show", show}]
	out.Show.PosterURL, out.Show.BackdropURL, out.Show.LogoURL = art.PosterURL, art.BackdropURL, art.LogoURL
	var nextSeason string
	out.NextUp, nextSeason, e = s.workspaceNextUp(r, show)
	if e != nil {
		return out, e
	}

	if e = s.workspaceSeasons(r, show, before, &out); e != nil {
		return out, e
	}
	if out.Settings, e = readShowSettings(s.read(), show); e != nil {
		return out, e
	}
	// A show whose owner changed its presentation: a flat show lists its
	// episodes in groups instead of seasons; any setting pages by position.
	if out.Settings.active() {
		return s.settingWorkspace(r, out, show, season, before)
	}
	if r.Group != "" && r.Group != "unassigned_absolute" {
		return out, ErrShowContext
	}
	var absolute int
	absRestriction, absBound := ItemRestrictionSQL("item.id", r.Viewer.EffectiveRestrictions())
	absArgs := append([]any{show}, absBound...)
	if e = s.read().QueryRow(`SELECT count(*) FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_entities sh ON sh.id=ep.show_id WHERE sh.public_id=pid_blob(?) AND ep.season_id IS NULL AND ep.numbering='absolute' AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=item.id) AND `+absRestriction, absArgs...).Scan(&absolute); e != nil {
		return out, e
	}
	if absolute > 0 {
		out.Groups = append(out.Groups, ShowWorkspaceGroup{"unassigned_absolute", "Unassigned absolute episodes", absolute})
	}
	if r.SelectedSeasonID != "" {
		if season != "" && season != r.SelectedSeasonID || r.EpisodeID != "" && season == "" {
			return out, ErrShowContext
		}
		season = r.SelectedSeasonID
	}
	if r.Group != "" && (season != "" || absolute == 0) {
		return out, ErrShowContext
	}
	if season == "" && r.EpisodeID == "" && r.Group == "" {
		if nextSeason != "" {
			season = nextSeason
		}
	}
	if season == "" && r.EpisodeID == "" && r.Group == "" {
		defaultArgs := append([]any{show}, absBound...)
		e = s.read().QueryRow(`SELECT pid(se.public_id) FROM catalog_entities se JOIN catalog_seasons d ON d.entity_id=se.id JOIN catalog_entities sh ON sh.id=d.show_id WHERE sh.public_id=pid_blob(?) AND se.retired=0 AND EXISTS(SELECT 1 FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_asset_links a ON a.entity_id=item.id WHERE ep.season_id=se.id AND `+absRestriction+`) ORDER BY d.number=0,d.number LIMIT 1`, defaultArgs...).Scan(&season)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
	}
	var selectedNumber int
	if season != "" {
		selectedRestriction, selectedBound := ItemRestrictionSQL("selected_item.id", r.Viewer.EffectiveRestrictions())
		selectedArgs := append([]any{season, show}, selectedBound...)
		selectedVisible := ""
		if selectedRestriction != "1" {
			selectedVisible = ` AND EXISTS(SELECT 1 FROM catalog_episodes se_episode JOIN catalog_entities selected_item ON selected_item.id=se_episode.entity_id WHERE se_episode.season_id=se.id AND ` + selectedRestriction + `)`
		}
		if e = s.read().QueryRow(`SELECT d.number FROM catalog_entities se JOIN catalog_seasons d ON d.entity_id=se.id JOIN catalog_entities sh ON sh.id=d.show_id WHERE se.public_id=pid_blob(?) AND sh.public_id=pid_blob(?) AND se.retired=0`+selectedVisible, selectedArgs...).Scan(&selectedNumber); e != nil {
			return out, e
		}
		out.Selected.SeasonID = &season
	} else if absolute > 0 {
		out.Selected.Group = "unassigned_absolute"
	}
	out.Selected.EpisodeID = r.EpisodeID
	out.Episodes, e = s.workspaceEpisodes(r, out, number, selectedNumber, before)
	if e != nil {
		return out, e
	}
	if r.EpisodeID != "" {
		out.EpisodeCredits, e = s.workspaceEpisodeCredits(r.EpisodeID, r.Viewer)
		if e != nil {
			return out, e
		}
	}
	after, e := s.ContentRevision(r.Library, r.Profile)
	if e != nil {
		return out, e
	}
	if before != after {
		return out, ErrStaleContinuation
	}
	return out, nil
}

// The accepted show record owns these facts. An episode's genres or language
// cannot stand in for its parent: a restricted sibling must never supply a
// hero field merely because the selected episode is visible.
func (s *Service) showHeroEvidence(show string, viewer Viewer, out *ShowWorkspace) error {
	var rawGenres, rawRating string
	err := s.read().QueryRow(`SELECT
	 COALESCE((SELECT value FROM screen_metadata_fields WHERE target_kind='show' AND target_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND field='genres'),'[]'),
	 COALESCE((SELECT json_extract(value,'$') FROM screen_metadata_fields WHERE target_kind='show' AND target_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND field='status'),''),
	 COALESCE((SELECT json_extract(value,'$') FROM screen_metadata_fields WHERE target_kind='show' AND target_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND field='date'),''),
	 COALESCE((SELECT value FROM screen_metadata_fields WHERE target_kind='show' AND target_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND field='rating'),'')`, show, show, show, show).Scan(&rawGenres, &out.Show.Status, &out.Show.FirstAired, &rawRating)
	if err != nil {
		return err
	}
	if out.Show.OriginalLanguage, err = metadatalanguage.Published(s.Context(), s.read(), show); err != nil {
		return err
	}
	var names []struct {
		Name string `json:"name"`
	}
	if err = json.Unmarshal([]byte(rawGenres), &names); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, name := range names {
		value := strings.TrimSpace(name.Name)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		out.Show.Genres = append(out.Show.Genres, value)
		if len(out.Show.Genres) == 12 {
			break
		}
	}
	if rawRating != "" {
		var rating ShowRating
		if json.Unmarshal([]byte(rawRating), &rating) == nil && rating.Value > 0 && rating.Scale > 0 {
			out.Show.Rating = &rating
		}
	}
	// The latest air date among this library's episodes of the show (TVDB's
	// aired date, or the episode's own date), for the "2008–2013" span.
	// The latest episode date: the published compact episode's air_date
	// (days since the epoch), else its TVDB evidence date.
	lastVisible, lastArgs := viewer.itemVisibilitySQL("item.id")
	if err = s.read().QueryRow(`SELECT COALESCE(max(COALESCE(strftime('%Y-%m-%d',ep.air_date*86400,'unixepoch'),substr(json_extract(pe.payload,'$.aired'),1,10),'')),'')
 FROM catalog_entities sh JOIN catalog_episodes ep ON ep.show_id=sh.id JOIN catalog_entities item ON item.id=ep.entity_id
 LEFT JOIN provider_evidence pe ON pe.item_id=item.id AND pe.provider='tvdb' WHERE sh.public_id=pid_blob(?) AND `+lastVisible, append([]any{show}, lastArgs...)...).Scan(&out.Show.LastAired); err != nil {
		return err
	}
	out.ShowCredits, err = s.showCredits(show, viewer)
	return err
}

// showCredits is the show's complete cast (acting first, by billing), then its
// crew — a show's own list, never cut off — as
// /v1/people persons (show_people_credits, 0260). Cast is shown to every
// viewer who may see the show, restricted profiles included: a restriction
// filters titles, not people. The photo is the show's own portrait for the
// person (TVDB), else the person's portrait from any visible title.
func (s *Service) showCredits(show string, viewer Viewer) ([]ShowCredit, error) {
	// show_people_credits is the projection; until a show's is built (a show
	// published before 0260, before its backfill runs) the same keys are read
	// straight from the credits field.
	rows, err := s.read().Query(`WITH projected AS (
	 SELECT show_id,identity_key,name,role,department,ordinal FROM show_people_credits WHERE show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?1))
	 ), fallback AS (
	 SELECT (SELECT id FROM catalog_entities WHERE public_id=pid_blob(?1)) show_id,
	  CASE WHEN COALESCE(json_extract(j.value,'$.id'),'')<>'' THEN sf.provider||':'||json_extract(j.value,'$.id') ELSE 'name:'||lower(trim(json_extract(j.value,'$.name'))) END identity_key,
	  trim(json_extract(j.value,'$.name')) name,COALESCE(json_extract(j.value,'$.role'),'') role,COALESCE(json_extract(j.value,'$.department'),'') department,CAST(j.key AS INTEGER) ordinal
	 FROM screen_metadata_fields sf,json_each(CASE WHEN json_valid(sf.value) AND json_type(sf.value)='array' THEN sf.value ELSE '[]' END) j
	 WHERE sf.target_kind='show' AND sf.target_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?1)) AND sf.field IN('credits','creditsOnline') AND trim(COALESCE(json_extract(j.value,'$.name'),''))<>''
	 AND NOT EXISTS(SELECT 1 FROM projected)
	 ), credits AS (SELECT * FROM projected UNION ALL SELECT * FROM fallback)
	 SELECT COALESCE(p.token,''),c.identity_key,c.name,c.role,c.department,c.ordinal,
	 COALESCE((SELECT a.digest FROM artwork_selections a JOIN artwork_sources o ON o.digest=a.digest WHERE a.kind='show' AND a.entity_id=c.show_id AND a.role='portrait' AND a.subject=c.identity_key),'')
	 FROM credits c LEFT JOIN catalog_people p INDEXED BY catalog_people_identity ON p.identity_key=c.identity_key
	 ORDER BY CASE WHEN c.department IN('Acting','Cast','Actor') THEN 0 WHEN c.department IN('Creator','Writing','Writer','Directing','Director','Production','Producer') THEN 1 ELSE 2 END,c.ordinal,c.name`, show)
	if err != nil {
		return nil, err
	}
	credits := []ShowCredit{}
	ids := []string{}
	type ownPortrait struct {
		index       int
		key, source string
	}
	own := []ownPortrait{}
	for rows.Next() {
		var credit ShowCredit
		var key, digest string
		if err = rows.Scan(&credit.ID, &key, &credit.Name, &credit.Role, &credit.Department, &credit.Ordinal, &digest); err != nil {
			rows.Close()
			return nil, err
		}
		if digest != "" {
			own = append(own, ownPortrait{len(credits), key, digest})
		} else if credit.ID != "" {
			ids = append(ids, credit.ID)
		}
		credits = append(credits, credit)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	sources := make([]string, 0, len(own))
	for _, p := range own {
		sources = append(sources, p.source)
	}
	described, err := artworkref.Load(s.Context(), s.read(), sources)
	if err != nil {
		return nil, err
	}
	for _, p := range own {
		credits[p.index].PortraitURL = selectedArtworkURL("show", show, "portrait", p.key, p.source, described[p.source])
	}
	portraits, err := s.portraitSelections(ids, viewer)
	if err != nil {
		return nil, err
	}
	for i := range credits {
		if credits[i].PortraitURL == "" && credits[i].ID != "" {
			credits[i].PortraitURL = portraits[credits[i].ID].url()
		}
	}
	return credits, nil
}

func (s *Service) workspaceTarget(r ShowWorkspaceRequest) (show, season string, number int, e error) {
	show = r.ShowID
	season = r.SeasonID
	if show == "" && season == "" && r.EpisodeID == "" {
		e = ErrShowContext
		return
	}
	if r.EpisodeID != "" {
		var actualShow, actualSeason, library string
		restriction, bound := ItemRestrictionSQL("item.id", r.Viewer.EffectiveRestrictions())
		args := append([]any{r.EpisodeID}, bound...)
		e = s.read().QueryRow(`SELECT pid(sh.public_id),COALESCE(pid(se.public_id),''),ep.number,l.library_id FROM catalog_entities item JOIN catalog_episodes ep ON ep.entity_id=item.id JOIN catalog_entities sh ON sh.id=ep.show_id LEFT JOIN catalog_entities se ON se.id=ep.season_id JOIN catalog_libraries l ON l.id=item.library_id WHERE item.public_id=pid_blob(?) AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=item.id) AND `+restriction, args...).Scan(&actualShow, &actualSeason, &number, &library)
		if e != nil {
			return
		}
		if library != r.Library || show != "" && show != actualShow || season != "" && season != actualSeason {
			e = ErrShowContext
			return
		}
		show, season = actualShow, actualSeason
	}
	if season != "" {
		var actualShow, library string
		seasonRestriction, bound := ItemRestrictionSQL("visible_item.id", r.Viewer.EffectiveRestrictions())
		args := append([]any{season}, bound...)
		visibleSeason := ""
		if seasonRestriction != "1" {
			visibleSeason = ` AND EXISTS(SELECT 1 FROM catalog_episodes visible_episode JOIN catalog_entities visible_item ON visible_item.id=visible_episode.entity_id WHERE visible_episode.season_id=se.id AND ` + seasonRestriction + `)`
		}
		e = s.read().QueryRow(`SELECT pid(sh.public_id),l.library_id FROM catalog_entities se JOIN catalog_seasons d ON d.entity_id=se.id JOIN catalog_entities sh ON sh.id=d.show_id JOIN catalog_libraries l ON l.id=sh.library_id WHERE se.public_id=pid_blob(?) AND se.retired=0`+visibleSeason, args...).Scan(&actualShow, &library)
		if e != nil {
			return
		}
		if library != r.Library || show != "" && show != actualShow {
			e = ErrShowContext
			return
		}
		show = actualShow
	}
	return
}

// seasonOrderKey sorts Specials after every numbered season (numbers are at
// most 9999). It reads the published compact season number.
const seasonOrderKey = `(CASE WHEN d.number=0 THEN 10000 ELSE d.number END)`

func seasonOrder(number int) int {
	if number == 0 {
		return 10000
	}
	return number
}

func seasonTitle(number int) string {
	if number == 0 {
		return "Specials"
	}
	return fmt.Sprintf("Season %d", number)
}
func (s *Service) workspaceSeasons(r ShowWorkspaceRequest, show string, revision ContentRevision, out *ShowWorkspace) error {
	scope := cursorScope{Library: r.Library, Viewer: r.ViewerFence, Profile: r.Profile, View: "show_workspace_seasons", Entity: show, Sort: "season", Direction: "asc", Limit: r.SeasonLimit}
	after := -1
	if r.SeasonCursor != "" {
		c, e := s.decodeDetailCursor(r.SeasonCursor, scope, revision)
		if e != nil {
			return e
		}
		after, e = strconv.Atoi(c.Value)
		if e != nil {
			return ErrCursor
		}
	}
	restriction, bound := r.Viewer.itemVisibilitySQL("item.id")
	restriction += recordingsClause("item.id", r.Viewer.EffectiveRestrictions())
	where := `sh.public_id=pid_blob(?) AND se.retired=0 AND EXISTS(SELECT 1 FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_asset_links a ON a.entity_id=item.id WHERE ep.season_id=se.id AND ` + restriction + `)`
	seasonFrom := ` FROM catalog_entities se JOIN catalog_seasons d ON d.entity_id=se.id JOIN catalog_entities sh ON sh.id=d.show_id WHERE `
	args := append([]any{show}, bound...)
	if e := s.read().QueryRow(`SELECT count(*)`+seasonFrom+where, args...).Scan(&out.SeasonTotalCount); e != nil {
		return e
	}
	// Specials (season 0) come last, as in every other client of the spec
	// (Spec — Page Content §2); the cursor pages on the same ordering key.
	pageArgs := append(append([]any{}, args...), after, r.SeasonLimit+1)
	rows, e := s.read().Query(`SELECT pid(se.public_id),pid(sh.public_id),d.number`+seasonFrom+where+` AND `+seasonOrderKey+`>? ORDER BY `+seasonOrderKey+` LIMIT ?`, pageArgs...)
	if e != nil {
		return e
	}
	for rows.Next() {
		var season Season
		if e = rows.Scan(&season.ID, &season.ShowID, &season.Number); e != nil {
			rows.Close()
			return e
		}
		season.Title = seasonTitle(season.Number)
		out.Seasons = append(out.Seasons, season)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(out.Seasons) > r.SeasonLimit {
		out.Seasons = out.Seasons[:r.SeasonLimit]
		last := out.Seasons[len(out.Seasons)-1]
		out.NextSeasonCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: strconv.Itoa(seasonOrder(last.Number)), Expires: time.Now().Add(30 * time.Minute).Unix()}, revision)
	}
	if e != nil || len(out.Seasons) == 0 {
		return e
	}
	// Each season's own poster; a season without one has none (never the
	// show's), so a season grid never repeats one image.
	targets := make([]artworkTarget, 0, len(out.Seasons))
	for _, season := range out.Seasons {
		targets = append(targets, artworkTarget{"season", season.ID})
	}
	arts, e := s.resolveArtwork(targets)
	if e != nil {
		return e
	}
	for i := range out.Seasons {
		if poster := arts[artworkTarget{"season", out.Seasons[i].ID}].PosterURL; strings.HasPrefix(poster, "/v1/metadata/season/") {
			out.Seasons[i].PosterURL = poster
		}
	}
	seasonIDs := make([]string, 0, len(out.Seasons))
	byID := make(map[string]*Season, len(out.Seasons))
	for i := range out.Seasons {
		season := &out.Seasons[i]
		season.EpisodeCount, season.WatchedCount, season.UnwatchedCount = new(int), new(int), new(int)
		seasonIDs = append(seasonIDs, season.ID)
		byID[season.ID] = season
	}
	countArgs := append([]any{r.Profile, idsJSON(seasonIDs)}, bound...)
	counts, e := s.read().Query(`SELECT pid(se.public_id),count(*),COALESCE(min(strftime('%Y-%m-%d',ep.air_date*86400,'unixepoch')),''),COALESCE(max(strftime('%Y-%m-%d',ep.air_date*86400,'unixepoch')),''),sum(`+personalstate.CompactSQL("?", "item.id")+`)
		FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_entities se ON se.id=ep.season_id
		WHERE se.public_id IN(SELECT pid_blob(value) FROM json_each(?))
		AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=item.id) AND `+restriction+`
		GROUP BY se.id`, countArgs...)
	if e != nil {
		return e
	}
	for counts.Next() {
		var id string
		var total, watched int
		var first, last string
		if e = counts.Scan(&id, &total, &first, &last, &watched); e != nil {
			counts.Close()
			return e
		}
		if season := byID[id]; season != nil {
			*season.EpisodeCount, *season.WatchedCount, *season.UnwatchedCount = total, watched, total-watched
			season.FirstAired, season.LastAired = first, last
		}
	}
	e = counts.Err()
	counts.Close()
	return e
}

// ShowWorkspace retries a first-page read that raced a catalog publication.
// A continuation cannot be retried: its cursor was minted against the earlier
// revision, so the client must restart from its first page.
func (s *Service) ShowWorkspace(r ShowWorkspaceRequest) (ShowWorkspace, error) {
	return consistentRead(r.Cursor == "" && r.SeasonCursor == "", func() (ShowWorkspace, error) { return s.showWorkspaceOnce(r) })
}

// validShowGroup accepts the episode groups a workspace can name.
func validShowGroup(group string) bool {
	if group == "unassigned_absolute" || group == "all" || group == "specials" {
		return true
	}
	if !strings.HasPrefix(group, "range:") {
		return false
	}
	start, e := strconv.Atoi(strings.TrimPrefix(group, "range:"))
	return e == nil && start >= 0 && start%showRangeSize == 0 && start < 1000000
}

// settingWorkspace finishes a workspace for a show with its own presentation.
func (s *Service) settingWorkspace(r ShowWorkspaceRequest, out ShowWorkspace, show, season string, before ContentRevision) (ShowWorkspace, error) {
	ranked, e := s.showRankedEpisodes(r, show)
	if e != nil {
		return out, e
	}
	selectedNumber := 0
	if out.Settings.flat() {
		// The season switcher is gone: the groups stand in for it, and a link to a season opens the show.
		out.Groups = showEpisodeGroups(out.Settings, ranked)
		group := r.Group
		if group == "" && r.EpisodeID != "" {
			group = groupOf(out.Groups, ranked, r.EpisodeID)
		}
		if group == "" && out.NextUp != nil {
			group = groupOf(out.Groups, ranked, out.NextUp.ID)
		}
		if group == "" && len(out.Groups) > 0 {
			group = out.Groups[0].ID
			if out.Settings.NewestFirst {
				for _, candidate := range out.Groups {
					if candidate.ID != "specials" {
						group = candidate.ID
					}
				}
			}
		}
		known := group == ""
		for _, candidate := range out.Groups {
			known = known || candidate.ID == group
		}
		if !known {
			return out, ErrShowContext
		}
		out.Selected.Group = group
	} else {
		if r.Group != "" {
			return out, ErrShowContext
		}
		if r.SelectedSeasonID != "" {
			season = r.SelectedSeasonID
		}
		if season == "" && r.EpisodeID == "" && out.NextUp != nil {
			for _, row := range ranked {
				if row.id == out.NextUp.ID {
					season = row.season
				}
			}
		}
		if season == "" {
			for _, row := range ranked {
				if row.season != "" && (season == "" || out.Settings.NewestFirst && !row.special) {
					season = row.season
				}
			}
		}
		if season != "" {
			found := false
			for _, candidate := range out.Seasons {
				if candidate.ID == season {
					found, selectedNumber = true, candidate.Number
				}
			}
			if !found {
				if e = s.read().QueryRow(`SELECT d.number FROM catalog_entities se JOIN catalog_seasons d ON d.entity_id=se.id WHERE se.public_id=pid_blob(?)`, season).Scan(&selectedNumber); e != nil {
					return out, e
				}
			}
			out.Selected.SeasonID = &season
		}
	}
	// The hero's Play names the episode the way its rows do.
	if out.Settings.AbsoluteNumbering && out.NextUp != nil {
		for _, row := range ranked {
			if row.id == out.NextUp.ID && !row.special {
				absolute := row.absolute
				out.NextUp.AbsoluteNumber = &absolute
			}
		}
	}
	out.Selected.EpisodeID = r.EpisodeID
	if out.Episodes, e = s.workspaceSettingEpisodes(r, out, ranked, selectedNumber, before); e != nil {
		return out, e
	}
	if r.EpisodeID != "" {
		if out.EpisodeCredits, e = s.workspaceEpisodeCredits(r.EpisodeID, r.Viewer); e != nil {
			return out, e
		}
	}
	after, e := s.ContentRevision(r.Library, r.Profile)
	if e != nil {
		return out, e
	}
	if before != after {
		return out, ErrStaleContinuation
	}
	return out, nil
}
