package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

// HomeTitleContext is the displayed episode's actual, currently visible show.
// It is populated only on Home, never by generic browse/detail enrichment.
type HomeTitleContext struct {
	Kind        string   `json:"kind"`
	Year        *int     `json:"year,omitempty"`
	Genres      []string `json:"genres"`
	SeasonCount *int     `json:"seasonCount,omitempty"`
}

func (s *Service) homeTitleContextRows(v Viewer, rows []HomeRow) error {
	entries := []*ContentEntry{}
	var collect func([]HomeRow)
	collect = func(rows []HomeRow) {
		for i := range rows {
			row := &rows[i]
			for j := range row.Entries {
				entries = append(entries, &row.Entries[j])
			}
			for j := range row.Resumes {
				entries = append(entries, &row.Resumes[j].Target)
			}
			if row.Collection != nil {
				for j := range row.Collection.Members {
					entries = append(entries, &row.Collection.Members[j])
				}
			}
			collect(row.Children)
		}
	}
	collect(rows)
	return s.homeTitleContexts(v, entries)
}

// One returned-ID batch resolves real parent links and policy in Home's WAL
// snapshot. Parent facts are materialized once per distinct parent, not once
// per episode. Indexed season/member EXISTS visits only those parents' ranges;
// no descendant list or global season count is retained or sent to the client.
func (s *Service) homeTitleContexts(v Viewer, entries []*ContentEntry) error {
	if dbwork.Snapshot(s.Context()) == nil {
		return sql.ErrTxDone
	}
	ids, seen := []string{}, map[string]bool{}
	for _, entry := range entries {
		entry.HomeTitleContext = nil
		if entry.Kind == "episode" && !seen[entry.ID] {
			ids = append(ids, entry.ID)
			seen[entry.ID] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	episodeVisible, episodeArgs := v.itemVisibilitySQL("episode.id")
	parentVisible, parentArgs := v.entityVisibilitySQL("show_entity.id", "show_library.library_id")
	seasonVisible, seasonArgs := v.itemVisibilitySQL("season_member.id")
	args := append([]any{idsJSON(ids)}, episodeArgs...)
	args = append(args, parentArgs...)
	args = append(args, seasonArgs...)
	rows, err := s.read().Query(`WITH home_context_requested AS MATERIALIZED (
	 SELECT pid(episode.public_id) AS entry_id,ep.show_id,episode.library_id
	 FROM json_each(?) requested CROSS JOIN catalog_entities episode ON episode.public_id=pid_blob(requested.value)
	 CROSS JOIN catalog_episodes ep ON ep.entity_id=episode.id
	 WHERE episode.kind=4 AND `+episodeVisible+recordingsClause("episode.id", v.EffectiveRestrictions())+`
	 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=episode.id)),
	 home_context_parents AS MATERIALIZED (
	 SELECT show_entity.id,show_entity.year,show_entity.library_id AS library_entity,show_library.library_id
	 FROM (SELECT DISTINCT show_id,library_id FROM home_context_requested) requested
	 CROSS JOIN catalog_entities show_entity ON show_entity.id=requested.show_id AND show_entity.library_id=requested.library_id
	 CROSS JOIN catalog_libraries show_library ON show_library.id=show_entity.library_id
	 WHERE show_entity.kind=2 AND show_entity.retired=0 AND show_library.retired=0 AND `+parentVisible+`),
	 home_context_facts AS MATERIALIZED (
	 SELECT parent.id,parent.year,parent.library_id,
	 (SELECT json_group_array(label) FROM (
	  SELECT COALESCE(min(src.label_override),term.label) AS label
	  FROM catalog_term_sources src CROSS JOIN catalog_terms term ON term.id=src.term_id AND term.vocab=1
	  WHERE src.entity_id=parent.id GROUP BY term.id ORDER BY min(src.ordinal),term.label LIMIT 2)) AS genres,
	 (SELECT count(*) FROM catalog_seasons sn CROSS JOIN catalog_entities season_entity ON season_entity.id=sn.entity_id
	  WHERE sn.show_id=parent.id AND sn.number>0 AND season_entity.kind=3 AND season_entity.retired=0 AND season_entity.library_id=parent.library_entity
	  AND EXISTS(SELECT 1 FROM catalog_episodes season_episode
	   CROSS JOIN catalog_entities season_member ON season_member.id=season_episode.entity_id
	   WHERE season_episode.season_id=sn.entity_id AND season_episode.show_id=parent.id
	   AND season_member.library_id=parent.library_entity AND `+seasonVisible+recordingsClause("season_member.id", v.EffectiveRestrictions())+`
	   AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=season_member.id))) AS seasons
	 FROM home_context_parents parent)
	 SELECT requested.entry_id,facts.library_id,facts.year,facts.genres,facts.seasons
	 FROM home_context_requested requested JOIN home_context_facts facts ON facts.id=requested.show_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	type contextFact struct {
		library string
		context HomeTitleContext
	}
	facts := map[string]contextFact{}
	for rows.Next() {
		var id, genres string
		var year, seasons int
		var fact contextFact
		if err := rows.Scan(&id, &fact.library, &year, &genres, &seasons); err != nil {
			return err
		}
		fact.context = HomeTitleContext{Kind: "show", Genres: []string{}, SeasonCount: &seasons}
		if year > 0 {
			fact.context.Year = &year
		}
		if err := json.Unmarshal([]byte(genres), &fact.context.Genres); err != nil {
			return err
		}
		facts[id] = fact
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, entry := range entries {
		if fact, ok := facts[entry.ID]; ok && entry.Kind == "episode" && entry.LibraryID == fact.library {
			context := fact.context
			entry.HomeTitleContext = &context
		}
	}
	return nil
}

// HomeReason records the actual source of a selection, separately from its
// localized wording. Source is absent for non-causal recommendations.
type HomeReason struct {
	Kind      string            `json:"kind"`
	Text      ServerText        `json:"text"`
	Source    *ContentEntityRef `json:"source,omitempty"`
	LibraryID string            `json:"libraryId,omitempty"`
}
type HomeSpotlight struct {
	Entry  ContentEntry `json:"entry"`
	Reason HomeReason   `json:"reason"`
	// ResumeTarget is a show's actual next episode, including its own position.
	ResumeTarget *ContentEntry `json:"resumeTarget,omitempty"`
}
type HomeResume struct {
	EntryID string       `json:"entryId"`
	Target  ContentEntry `json:"target"`
}
type HomePeriod struct {
	Days   int        `json:"days"`
	Start  string     `json:"start"`
	End    string     `json:"end"`
	Metric string     `json:"metric"`
	Text   ServerText `json:"text"`
}
type HomeCollection struct {
	Actions *HomeActions `json:"actions,omitempty"`
	// Featured Home collections are selected only from movie libraries.
	MediaKind   string         `json:"mediaKind"`
	Collection  Collection     `json:"collection"`
	Members     []ContentEntry `json:"members"`
	BackdropURL string         `json:"backdropUrl,omitempty"`
	// These facts describe the whole collection only when its complete visible
	// membership fits the preview. Otherwise the exact count stands alone.
	FirstYear *int   `json:"firstYear,omitempty"`
	LastTitle string `json:"lastTitle,omitempty"`
}
type HomeChannel struct {
	Actions HomeChannelActions       `json:"actions"`
	Channel livechannels.Channel     `json:"channel"`
	Source  livechannels.GuideSource `json:"source"`
}
type HomeLive struct {
	Revision   string                         `json:"revision"`
	ObservedAt string                         `json:"observedAt"`
	ExpiresAt  string                         `json:"expiresAt"`
	Sort       string                         `json:"sort"`
	Group      string                         `json:"group"`
	Groups     []livechannels.GuideGroupCount `json:"groups"`
	Channels   []HomeChannel                  `json:"channels"`
}

const homeNewWindow = 14 * 24 * time.Hour
const homeCompositionMaximum = 100

// The numbered Home poster row ranks only video. Scope the engine before it
// selects works, computes totals and fingerprints them; its shared audio
// recommendation behavior remains available to other surfaces.
func (s *Service) homeCommunitySource(r HomeRequest, spec homeRowSpec) (homeSource, error) {
	libraries, err := s.homeLibraries(r.Libraries)
	if err != nil {
		return homeSource{}, err
	}
	r.Libraries = []string{}
	for _, library := range libraries {
		if library.Kind == "movie" || library.Kind == "tv" || library.Kind == "anime" {
			r.Libraries = append(r.Libraries, library.ID)
		}
	}
	return s.homeEngineSource(r, spec)
}

// homeMergedRecent merges maintained, already restricted recent-work walks.
// Each library costs <=4096 candidates, each contributes <=100 distinct works;
// the Home row itself is exactly the newest 100 of that bounded union.
func (s *Service) homeMergedRecent(r HomeRequest, audio bool) ([][2]string, error) {
	libraries, err := s.homeLibraries(r.Libraries)
	if err != nil {
		return nil, err
	}
	all := [][2]string{}
	for _, library := range libraries {
		isAudio := library.Kind == "music" || library.Kind == "audiobook"
		if !audio && library.Kind != "movie" && library.Kind != "tv" && library.Kind != "anime" {
			continue
		}
		if isAudio != audio {
			continue
		}
		values, err := s.homeRecentWorks(r, library.ID)
		if err != nil {
			return nil, err
		}
		all = append(all, values...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i][1] != all[j][1] {
			return all[i][1] > all[j][1]
		}
		return all[i][0] > all[j][0]
	})
	out := make([][2]string, 0, min(len(all), homeCompositionMaximum))
	seen := map[string]bool{}
	for _, v := range all {
		if !seen[v[0]] {
			seen[v[0]] = true
			out = append(out, v)
		}
		if len(out) == homeCompositionMaximum {
			break
		}
	}
	return out, nil
}

// The newest completed, still-accessible video among the last 64 history
// events is the causal anchor. A watched mark alone never claims a viewing.
func (s *Service) homeBecause(r HomeRequest) (*ContentEntityRef, []recCandidate, error) {
	restriction, args := r.Viewer.itemVisibilitySQL("i.id")
	bind := append([]any{r.Profile, idsJSON(r.Libraries), r.now().UTC().Format(time.RFC3339Nano)}, args...)
	rows, err := s.read().Query(`SELECT pid(i.public_id),i.title FROM
 (SELECT item_id,completed,updated_at,id FROM personal_history INDEXED BY personal_history_page WHERE profile_id=? ORDER BY updated_at DESC,id DESC LIMIT 64) h
 CROSS JOIN catalog_entities i ON i.id=h.item_id JOIN catalog_libraries l ON l.id=i.library_id
 WHERE h.completed=1 AND i.kind IN(1,4) AND l.library_id IN(SELECT value FROM json_each(?)) AND julianday(h.updated_at)<=julianday(?) AND `+homeVisible("i.id")+` AND `+restriction+` ORDER BY h.updated_at DESC,h.id DESC LIMIT 1`, bind...)
	if err != nil {
		return nil, nil, err
	}
	var source ContentEntityRef
	if rows.Next() {
		err = rows.Scan(&source.ID, &source.Name)
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if source.ID == "" {
		return nil, []recCandidate{}, nil
	}
	ranked, err := s.recommendationCandidates(r, "related", source.ID)
	if err != nil {
		return nil, nil, err
	}
	out := []recCandidate{}
	for _, c := range ranked {
		if c.Kind == "movie" || c.Kind == "show" {
			out = append(out, c)
		}
		if len(out) == homeCompositionMaximum {
			break
		}
	}
	return &source, out, nil
}

func (s *Service) homeAudioSource(r HomeRequest, booksOnly bool) (homeSource, error) {
	libraries, err := s.homeLibraries(r.Libraries)
	if err != nil {
		return homeSource{}, err
	}
	r.Libraries = nil
	for _, library := range libraries {
		if library.Kind == "audiobook" || (!booksOnly && library.Kind == "music") {
			r.Libraries = append(r.Libraries, library.ID)
		}
	}
	// An inapplicable row must not even construct its audio/recent relation.
	if len(r.Libraries) == 0 {
		return candidateSource(nil), nil
	}
	base := homeContinueBase("continue_listening")
	visible, args := r.Viewer.itemVisibilitySQL("candidate.entity")
	kind := "i.kind=7"
	if booksOnly {
		kind = "i.kind=9"
	}
	source := `SELECT pid(parent.public_id) AS id,'1:'||max(candidate.ord) AS ord FROM (` + base + `) candidate
 CROSS JOIN catalog_entities i ON i.public_id=pid_blob(candidate.id)
 LEFT JOIN catalog_book_files bf ON bf.entity_id=i.id LEFT JOIN catalog_songs sg ON sg.entity_id=i.id
 JOIN catalog_entities parent ON parent.id=CASE i.kind WHEN 9 THEN bf.book_id WHEN 7 THEN sg.album_id END
 WHERE ` + visible + ` AND ` + kind + ` GROUP BY parent.id`
	bound := append([]any{r.Profile, r.Profile, idsJSON(r.Libraries)}, args...)
	if !booksOnly {
		// Reuse each library's existing bounded Recently Added shelf. The
		// old union grouped the complete audio library before taking a page.
		values := [][2]string{}
		for _, library := range r.Libraries {
			head, err := s.homeRecentWorks(r, library)
			if err != nil {
				return homeSource{}, err
			}
			values = append(values, head...)
		}
		recent := homeRecentList(values)
		source = `SELECT id,max(ord) AS ord FROM (` + source + ` UNION ALL SELECT id,'0:'||ord AS ord FROM (` + recent.base + `)) GROUP BY id`
		bound = append(bound, recent.args...)
	}
	return homeSource{base: source, args: bound, selfRestricted: true}, nil
}

func (s *Service) homeCollection(r HomeRequest) (*HomeCollection, error) {
	libraries, err := s.homeLibraries(r.Libraries)
	if err != nil {
		return nil, err
	}
	for _, library := range libraries {
		if library.Kind != "movie" {
			continue
		}
		collections, _, err := s.Collections(library.ID, r.Viewer, "", 4)
		if err != nil {
			return nil, err
		}
		for _, collection := range collections {
			if collection.ItemCount == 0 {
				continue
			}
			restriction, args := r.Viewer.itemVisibilitySQL("i.id")
			bind := append([]any{collection.ID}, args...)
			rows, err := s.read().Query(`SELECT pid(i.public_id) FROM catalog_entities co CROSS JOIN catalog_collection_members m ON m.collection_id=co.id CROSS JOIN catalog_entities i ON i.id=m.item_id WHERE co.public_id=pid_blob(?) AND `+homeVisible("i.id")+` AND `+restriction+` ORDER BY m.order_key,m.item_id LIMIT 4`, bind...)
			if err != nil {
				return nil, err
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					break
				}
				ids = append(ids, id)
			}
			if e := rows.Err(); err == nil {
				err = e
			}
			rows.Close()
			if err != nil {
				return nil, err
			}
			if len(ids) == 0 {
				continue
			}
			members, err := s.homeEntries(r.Profile, ids)
			if err != nil {
				return nil, err
			}
			if err = s.homeEnrich(r.Profile, members); err != nil {
				return nil, err
			}
			out := &HomeCollection{Collection: collection, MediaKind: "movie", Members: members}
			for _, member := range members {
				if out.BackdropURL == "" {
					out.BackdropURL = member.BackdropURL
				}
			}
			if collection.ItemCount == len(members) {
				chronological := append([]ContentEntry{}, members...)
				sort.SliceStable(chronological, func(i, j int) bool {
					a, b := chronological[i].Year, chronological[j].Year
					return a != nil && (b == nil || *a < *b)
				})
				if chronological[0].Year != nil && chronological[len(chronological)-1].Year != nil {
					out.FirstYear = chronological[0].Year
					out.LastTitle = chronological[len(chronological)-1].Title
				}
			}
			return out, nil
		}
	}
	return nil, nil
}

// homeSpotlight chooses, in order: newest recent video, first related pick
// from an actual completed history event, newest continuation, then ranked
// recommendations and recent additions until five different works are filled.
// Membership is independent of UI timers. A sparse library yields fewer real
// works, never duplicate slides or manufactured titles/reasons.
func (s *Service) homeSpotlight(r HomeRequest) ([]HomeSpotlight, error) {
	recent, err := s.homeMergedRecent(r, false)
	if err != nil {
		return nil, err
	}
	source, related, err := s.homeBecause(r)
	if err != nil {
		return nil, err
	}
	continuation, err := s.homeRowFor(r, homeRowSpec{ID: "continue", Direction: "desc"}, ContentRevision{}, HomeRowPage{Limit: 5}, false, false)
	if err != nil {
		return nil, err
	}
	recommended, err := s.homeEngineSource(r, homeRowSpec{ID: "recommended"})
	if err != nil {
		return nil, err
	}
	type pick struct {
		id     string
		reason HomeReason
	}
	picks := []pick{}
	seen := map[string]bool{}
	add := func(id string, reason HomeReason) error {
		if len(picks) == 5 {
			return nil
		}
		var work string
		err := s.read().QueryRow(`SELECT CASE i.kind WHEN 4 THEN pid(sh.public_id) WHEN 3 THEN pid(parent.public_id) ELSE pid(i.public_id) END FROM catalog_entities i LEFT JOIN catalog_episodes ep ON ep.entity_id=i.id LEFT JOIN catalog_entities sh ON sh.id=ep.show_id LEFT JOIN catalog_seasons sn ON sn.entity_id=i.id LEFT JOIN catalog_entities parent ON parent.id=sn.show_id WHERE i.public_id=pid_blob(?) AND i.retired=0`, id).Scan(&work)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if work != "" && !seen[work] {
			seen[work] = true
			picks = append(picks, pick{work, reason})
		}
		return nil
	}
	recentReason := func(v [2]string) HomeReason {
		added := strings.Split(v[1], "\x1f")[0]
		at, _ := time.Parse(time.RFC3339Nano, added)
		kind, code, fallback := "recent", "next.home.reason.recentTo", "Recently added to {library}"
		if !at.IsZero() && !at.After(r.now()) && r.now().Sub(at) <= homeNewWindow {
			kind, code, fallback = "new", "next.home.reason.newIn", "New in {library}"
		}
		return HomeReason{Kind: kind, Text: ServerText{Code: code, Fallback: fallback}}
	}
	if len(recent) > 0 {
		if err = add(recent[0][0], recentReason(recent[0])); err != nil {
			return nil, err
		}
	}
	if source != nil && len(related) > 0 {
		if err = add(related[0].ID, HomeReason{Kind: "because_you_watched", Source: source, Text: ServerText{Code: "next.home.becauseYouWatched", Params: map[string]string{"title": source.Name}, Fallback: "Because you watched " + source.Name}}); err != nil {
			return nil, err
		}
	}
	if len(continuation.Entries) > 0 {
		if err = add(continuation.Entries[0].ID, HomeReason{Kind: "continue", Text: ServerText{Code: "next.home.continueWatching", Fallback: "Continue Watching"}}); err != nil {
			return nil, err
		}
	}
	for _, c := range recommended.candidates {
		if c.Kind == "movie" || c.Kind == "show" {
			if err = add(c.ID, HomeReason{Kind: "recommended", Text: ServerText{Code: "next.home.reason.recommended", Fallback: "Recommended for you"}}); err != nil {
				return nil, err
			}
		}
		if len(picks) == 5 {
			break
		}
	}
	for _, v := range recent {
		if err = add(v[0], recentReason(v)); err != nil {
			return nil, err
		}
		if len(picks) == 5 {
			break
		}
	}
	ids := []string{}
	for _, p := range picks {
		ids = append(ids, p.id)
	}
	entries, err := s.homeEngineEntries(r.Profile, ids)
	if err != nil {
		return nil, err
	}
	if err = s.homeEnrich(r.Profile, entries); err != nil {
		return nil, err
	}
	targets := []artworkTarget{}
	for _, e := range entries {
		targets = append(targets, artworkTarget{e.Kind, e.ID})
	}
	art, err := s.resolveArtwork(targets)
	if err != nil {
		return nil, err
	}
	out := []HomeSpotlight{}
	for i, entry := range entries {
		var overview, library string
		err = s.read().QueryRow(`SELECT substr(COALESCE(sh.overview,d.overview,''),1,300),l.name FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id LEFT JOIN catalog_shows sh ON sh.entity_id=e.id LEFT JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.public_id=pid_blob(?)`, entry.ID).Scan(&overview, &library)
		if err != nil {
			return nil, err
		}
		entry.Overview = overview
		artwork := art[artworkTarget{entry.Kind, entry.ID}]
		if artwork.BackdropURL != "" {
			entry.BackdropURL = artwork.BackdropURL
		}
		p := HomeSpotlight{Entry: entry, Reason: picks[i].reason}
		if p.Reason.Kind == "new" {
			unwatched, e := s.homeEntryUnwatched(r, entry)
			if e != nil {
				return nil, e
			}
			if !unwatched {
				p.Reason.Kind = "recent"
				p.Reason.Text = ServerText{Code: "next.home.reason.recentTo", Fallback: "Recently added to {library}"}
			}
		}
		if p.Reason.Kind == "new" || p.Reason.Kind == "recent" {
			p.Reason.LibraryID = entry.LibraryID
			p.Reason.Text.Params = map[string]string{"library": library}
			p.Reason.Text.Fallback = strings.ReplaceAll(p.Reason.Text.Fallback, "{library}", library)
		}
		if entry.Kind == "show" {
			next, _, e := s.workspaceNextUp(ShowWorkspaceRequest{Viewer: r.Viewer, Profile: r.Profile, Library: entry.LibraryID}, entry.ID)
			if e != nil {
				return nil, e
			}
			p.ResumeTarget = next
			one := []ContentEntry{p.Entry}
			if e = s.homeSpotlightSeasonCounts(r, one); e != nil {
				return nil, e
			}
			p.Entry = one[0]
		}
		out = append(out, p)
	}
	contexts := []*ContentEntry{}
	for i := range out {
		contexts = append(contexts, &out[i].Entry)
		if out[i].ResumeTarget != nil {
			contexts = append(contexts, out[i].ResumeTarget)
		}
	}
	if err := s.homeTitleContexts(r.Viewer, contexts); err != nil {
		return nil, err
	}
	return out, nil
}

// homeNewBadges marks the page's entries added within the New window and not yet
// watched. ords are the page scan's order values for the same entries, whose
// first field is the newest visible addition.
func (s *Service) homeNewBadges(r HomeRequest, entries []ContentEntry, ords []string) error {
	if len(ords) != len(entries) {
		return ErrStaleContinuation
	}
	for i := range entries {
		entry := &entries[i]
		stamp := strings.Split(ords[i], "\x1f")[0]
		entry.AddedAt = &stamp
		at, e := time.Parse(time.RFC3339Nano, stamp)
		if e != nil || at.After(r.now()) || r.now().Sub(at) > homeNewWindow {
			continue
		}
		unwatched, e := s.homeEntryUnwatched(r, *entry)
		if e != nil {
			return e
		}
		entry.New = unwatched
	}
	return nil
}

// New eligibility is shared by the row badge and the spotlight's truthful reason.
func (s *Service) homeEntryUnwatched(r HomeRequest, entry ContentEntry) (bool, error) {
	var unwatched bool
	restriction, args := r.Viewer.itemVisibilitySQL("m.item_id")
	bind := append([]any{r.Profile, entry.ID}, args...)
	err := s.read().QueryRow(`WITH scope AS(SELECT ? AS profile),target AS(SELECT * FROM catalog_entities WHERE public_id=pid_blob(?)) SELECT CASE WHEN e.kind IN(2,3) THEN EXISTS(SELECT 1 FROM catalog_browse_memberships m WHERE m.entity_id=e.id AND `+restriction+` AND `+personalstate.CompactSQL("scope.profile", "m.item_id")+`=0) ELSE `+personalstate.CompactSQL("scope.profile", "e.id")+`=0 END FROM scope CROSS JOIN target e`, bind...).Scan(&unwatched)
	return unwatched, err
}

// Selected-page audiobook resumes retain the real playable part position.
// Whole-book totals are exact only when all <=512 visible parts have a length.
func (s *Service) homeAudioResume(r HomeRequest, entries []ContentEntry) ([]HomeResume, error) {
	if len(entries) == 0 {
		return []HomeResume{}, nil
	}
	ids := []string{}
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	restriction, args := r.Viewer.itemVisibilitySQL("candidate.entity")
	base := homeContinueBase("continue_listening")
	rows, err := s.read().Query(`SELECT pid(parent.public_id),candidate.id FROM (`+base+`) candidate
 CROSS JOIN catalog_entities i ON i.public_id=pid_blob(candidate.id)
 LEFT JOIN catalog_book_files bf ON bf.entity_id=i.id LEFT JOIN catalog_songs sg ON sg.entity_id=i.id
 JOIN catalog_entities parent ON parent.id=CASE i.kind WHEN 9 THEN bf.book_id WHEN 7 THEN sg.album_id END
 WHERE parent.public_id IN(SELECT pid_blob(value) FROM json_each(?)) AND `+restriction+` ORDER BY candidate.ord DESC,candidate.id DESC`, append([]any{r.Profile, r.Profile, idsJSON(r.Libraries), idsJSON(ids)}, args...)...)
	if err != nil {
		return nil, err
	}
	fileParents := map[string]string{}
	parentFiles := map[string]string{}
	files := []string{}
	for rows.Next() {
		var parent, file string
		if err = rows.Scan(&parent, &file); err != nil {
			break
		}
		if parentFiles[parent] == "" {
			parentFiles[parent] = file
			fileParents[file] = parent
			files = append(files, file)
		}
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	targets, err := s.homeEntries(r.Profile, files)
	if err != nil {
		return nil, err
	}
	out := []HomeResume{}
	type resume struct {
		item     string
		position float64
	}
	resumes := map[string]resume{}
	for _, target := range targets {
		parent := fileParents[target.ID]
		out = append(out, HomeResume{EntryID: parent, Target: target})
		position := 0.0
		if target.ProgressSeconds != nil {
			position = *target.ProgressSeconds
		}
		resumes[parent] = resume{target.ID, position}
	}
	for index := range entries {
		entry := &entries[index]
		v, ok := resumes[entry.ID]
		if !ok {
			continue
		}
		entry.Playback = &ContentPlayback{ItemID: v.item, StartSeconds: &v.position}
		if entry.Kind != "book" {
			continue
		}
		if err := s.enrichBookProgress(r.Viewer, entry, v.item, int64(math.Round(v.position*1000))); err != nil {
			return nil, err
		}
		if progress := entry.BookProgress; progress != nil {
			if progress.DurationMs != nil {
				n := float64(*progress.DurationMs) / 1000
				entry.Duration = &n
			}
			if progress.PositionMs != nil {
				n := float64(*progress.PositionMs) / 1000
				entry.ProgressSeconds = &n
			}
			for i := range out {
				if out[i].EntryID == entry.ID {
					out[i].Target.BookProgress = progress
				}
			}
		}
	}
	return out, nil
}

func (s *Service) homeSpotlightSeasonCounts(r HomeRequest, entries []ContentEntry) error {
	for i := range entries {
		if entries[i].Kind != "show" {
			continue
		}
		restriction, args := r.Viewer.itemVisibilitySQL("ep.entity_id")
		var count int
		err := s.read().QueryRow(`SELECT count(*) FROM catalog_entities sh CROSS JOIN catalog_seasons sn ON sn.show_id=sh.id WHERE sh.public_id=pid_blob(?) AND sn.number>0 AND EXISTS(SELECT 1 FROM catalog_episodes ep WHERE ep.season_id=sn.entity_id AND `+homeVisible("ep.entity_id")+` AND `+restriction+`)`, append([]any{entries[i].ID}, args...)...).Scan(&count)
		if err != nil {
			return err
		}
		if count > 0 {
			entries[i].SeasonCount = &count
		}
	}
	return nil
}

// homeRecommendationWhy names only the current viewer's actual positive seeds
// from the same bounded pool and eligibility relation as Recommended. It never
// discloses another profile's history or calls providers to explain a row.
func (s *Service) homeRecommendationWhy(r HomeRequest) (*ServerText, []ContentEntityRef, error) {
	fallback := &ServerText{Code: "next.home.whyRecommended", Fallback: "Chosen from your viewing activity and available libraries."}
	// Prove the absence of causal activity with two indexed existence seeks.
	// A negative answer skips the expensive pool/scorer. A positive one still
	// uses the original eligibility and explanation logic below.
	var seed bool
	if err := s.read().QueryRow(`SELECT
 EXISTS(SELECT 1 FROM personal_items INDEXED BY personal_last_played WHERE profile_id=? AND last_played_at>'' LIMIT 1)
 OR EXISTS(SELECT 1 FROM personal_items INDEXED BY personal_play_state WHERE profile_id=? AND watched=1 LIMIT 1)`, r.Profile, r.Profile).Scan(&seed); err != nil {
		return nil, nil, err
	}
	if !seed {
		return fallback, nil, nil
	}
	candidates, _, err := s.recommendationPool(r)
	if err != nil || len(candidates) == 0 {
		return fallback, nil, err
	}
	candidates, err = s.homeExplanationCandidates(r.Profile, candidates)
	if err != nil || len(candidates) == 0 {
		return fallback, nil, err
	}
	base, args := recBase(r, candidates)
	rows, err := s.read().Query(base+` SELECT pid(e.public_id),e.title FROM seeds seed JOIN works w ON w.work=seed.work JOIN catalog_entities e ON e.id=w.id
 WHERE w.kind IN('movie','show') AND EXISTS(SELECT 1 FROM members m WHERE m.work=seed.work AND EXISTS(SELECT 1 FROM personal_items p WHERE p.item_id=m.id AND p.profile_id=? AND (p.last_played_at<>'' OR p.watched=1)))
 ORDER BY seed.weight DESC,w.work LIMIT 3`, append(args, r.Profile)...)
	if err != nil {
		return nil, nil, err
	}
	titles := []ContentEntityRef{}
	for rows.Next() {
		var title ContentEntityRef
		if err = rows.Scan(&title.ID, &title.Name); err != nil {
			break
		}
		titles = append(titles, title)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(titles) == 0 {
		return fallback, nil, nil
	}
	names := make([]string, len(titles))
	for i, title := range titles {
		names[i] = title.Name
	}
	list := names[0]
	if len(names) > 1 {
		list = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	return &ServerText{Code: "next.home.whyRecommendedFromActivity", Params: map[string]string{"titles": list}, Fallback: "Because you’ve been watching " + list + ". Titles you’ve finished are left out."}, titles, nil
}

const homeWeeklyTrendingWindow = 7 * 24 * time.Hour

// homeWeeklyTrendingSource is Home's local activity rail (collections f47,
// design d610, Root ruling 22 October). Provider discovery remains separate.
// A work needs three distinct currently eligible profiles. Replays by one
// profile do not increase its rank. No actor identity or activity count leaves
// this source; the public row contains authorized titles and ordinal positions.
func (s *Service) homeWeeklyTrendingSource(r HomeRequest) (homeSource, error) {
	if !r.CommunityActivity {
		return candidateSource(nil), nil
	}
	eligible, err := s.communityEligibleProfiles(r.now())
	if err != nil {
		return homeSource{}, err
	}
	type policyGroup struct {
		viewer   Viewer
		profiles []string
	}
	groups := map[string]*policyGroup{}
	for _, key := range eligible {
		who, ok := viewerFromPersonalKey(key)
		if !ok || who.Authority != "local" {
			continue
		}
		all, libraries, _, _, e := identity.DirectAccess(s.read(), identity.Principal{Viewer: who})
		if errors.Is(e, identity.ErrUnauthorized) {
			continue
		}
		if e != nil {
			return homeSource{}, e
		}
		permitted := []string{}
		for _, library := range r.Libraries {
			if all || containsString(libraries, library) {
				permitted = append(permitted, library)
			}
		}
		if len(permitted) == 0 {
			continue
		}
		restrictions, e := identity.RestrictionsForViewerTx(s.Context(), dbwork.Snapshot(s.Context()), who)
		if e != nil {
			return homeSource{}, e
		}
		content, e := access.ContentLimitsTx(s.Context(), dbwork.Snapshot(s.Context()), who.AccountID, restrictions.Content())
		if e != nil {
			return homeSource{}, e
		}
		actor := Viewer{Libraries: permitted, Restrictions: content}
		predicate, args := actor.itemVisibilitySQL("i.id")
		// Group identical predicates and values, rather than one SQL arm per actor.
		encoded, _ := json.Marshal([]any{predicate, args, content.BlockRecordings})
		group := groups[string(encoded)]
		if group == nil {
			group = &policyGroup{viewer: actor}
			groups[string(encoded)] = group
		}
		group.profiles = append(group.profiles, key)
	}
	if len(eligible) < communityViewers || len(groups) == 0 {
		return candidateSource(nil), nil
	}
	clauses := []string{}
	actorArgs := []any{}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		visible, args := group.viewer.itemVisibilitySQL("i.id")
		clauses = append(clauses, `(h.profile_id IN(SELECT value FROM json_each(?)) AND `+visible+recordingsClause("i.id", group.viewer.Restrictions)+`)`)
		actorArgs = append(actorArgs, idsJSON(group.profiles))
		actorArgs = append(actorArgs, args...)
	}
	visible, args := r.Viewer.itemVisibilitySQL("i.id")
	query := `WITH signals AS (
 SELECT ` + homeRecentWorkKeySQL("i") + ` AS work,
 CASE i.kind WHEN 4 THEN ep.show_id ELSE i.id END AS title_id,h.profile_id,h.updated_at
 FROM personal_history h INDEXED BY personal_history_recent
 CROSS JOIN catalog_entities i ON i.id=h.item_id LEFT JOIN catalog_episodes ep ON ep.entity_id=i.id
 WHERE h.updated_at>=? AND h.updated_at<=? AND i.kind IN(1,4) AND ` + visible + recordingsClause("i.id", r.Restrictions) + `
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id)
 AND EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets asset ON asset.id=link.asset_id WHERE link.entity_id=i.id AND link.available=1 AND asset.available=1)
 AND (h.completed=1 OR h.position>=30000 AND h.position>=(SELECT max(asset.duration)*50 FROM catalog_asset_links link JOIN catalog_assets asset ON asset.id=link.asset_id WHERE link.entity_id=i.id AND asset.available=1 AND asset.duration>0))
 AND (` + strings.Join(clauses, " OR ") + `)
 ), ranked AS (SELECT work,min(title_id) AS title_id,count(DISTINCT profile_id) AS viewers,max(updated_at) AS recent FROM signals GROUP BY work HAVING count(DISTINCT profile_id)>=` + strconv.Itoa(communityViewers) + `)
 SELECT pid(title.public_id),ranked.work FROM ranked JOIN catalog_entities title ON title.id=ranked.title_id WHERE title.retired=0 ORDER BY viewers DESC,recent DESC,pid(title.public_id) LIMIT ` + strconv.Itoa(recCandidateLimit)
	bound := []any{r.now().Add(-homeWeeklyTrendingWindow).UTC().Format("2006-01-02T15:04:05.000Z"), r.now().UTC().Format("2006-01-02T15:04:05.000Z")}
	bound = append(bound, args...)
	bound = append(bound, actorArgs...)
	rows, err := s.read().Query(query, bound...)
	if err != nil {
		return homeSource{}, err
	}
	candidates := []recCandidate{}
	for rows.Next() {
		var candidate recCandidate
		if err = rows.Scan(&candidate.ID, &candidate.Work); err != nil {
			break
		}
		candidates = append(candidates, candidate)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return homeSource{}, err
	}
	return candidateSource(candidates), nil
}
