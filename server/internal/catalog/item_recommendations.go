package catalog

import (
	"database/sql"
	"errors"
	"strings"
)

// One recommendation engine serves per-item rows, detail.related and the
// viewer's suggestions. Every candidate comes from persisted library identity
// (genres, credited people, collections, albums, books), never a title guess,
// and every relation is bounded before media hydration.

const itemRecommendationRowLimit = 12
const itemRecommendationRows = 6

func recommendationVisible(entityID string) string {
	return `EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets asset ON asset.id=link.asset_id WHERE link.entity_id=` + entityID + ` AND link.available=1)
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=` + entityID + `)`
}

func (s *Service) recommendationLibrary(id string) (string, error) {
	var name string
	e := s.read().QueryRow(`SELECT name FROM catalog_libraries WHERE library_id=? AND retired=0`, id).Scan(&name)
	return name, e
}

func recommendationRowShape(kind string) string {
	switch kind {
	case "song", "album":
		return "square"
	case "episode":
		return "landscape"
	}
	return "poster"
}

// ItemRecommendations publishes related rows for any item kind: the engine's
// rows, the director, the listening or movie genre and person rows, then
// collection membership.
func (s *Service) ItemRecommendations(viewer Viewer, item Item, genres []Genre, limit int, community bool) ([]HomeRow, error) {
	return s.itemRecommendations(viewer, item, genres, limit, true, community)
}

// itemRecommendations is each row's first page, in display order, up to
// itemRecommendationRows rows with something to show. Every row is the first
// page of the order its row route continues (relationPage).
func (s *Service) itemRecommendations(viewer Viewer, item Item, genres []Genre, limit int, full, community bool) ([]HomeRow, error) {
	if limit <= 0 || limit > itemRecommendationRowLimit {
		limit = itemRecommendationRowLimit
	}
	specs, err := s.itemRelationSpecs(viewer, item, genres, full, community)
	if err != nil {
		return nil, err
	}
	owner := relationOwner{kind: "item", id: item.ID, library: item.LibraryID, itemKind: item.Kind}
	out := []HomeRow{}
	for _, spec := range specs {
		if len(out) == itemRecommendationRows {
			break
		}
		row, err := s.relationPage(viewer, owner, spec, "", limit)
		if err != nil {
			return nil, err
		}
		if len(row.Entries) == 0 {
			continue
		}
		row.Priority = len(out) * 10
		out = append(out, row)
	}
	return out, nil
}

// relatedProjection keeps detail.related on its published shape while drawing
// its rows from the unified engine: at most three rows, two genre rows and
// one person row, each the first page of the order its row route continues.
func (s *Service) relatedProjection(viewer Viewer, item Item, genres []Genre) (*RelatedMovies, error) {
	name, e := s.recommendationLibrary(item.LibraryID)
	if e != nil {
		return nil, e
	}
	out := &RelatedMovies{Version: 1, LibraryID: item.LibraryID, LibraryName: name, Rows: []RelatedMovieRow{}}
	allowed := map[string]bool{"genre": true, "person": true}
	if item.Kind == "song" {
		allowed = map[string]bool{"album": true, "artist": true, "genre": true}
	} else if item.Kind == "audiobook_file" {
		allowed = map[string]bool{"book": true, "author": true, "genre": true}
	} else if item.Kind != "movie" {
		allowed = map[string]bool{"genre": true, "person": true, "show": true}
	}
	specs, e := s.itemRelationSpecs(viewer, item, genres, false, false)
	if e != nil {
		return nil, e
	}
	owner := relationOwner{kind: "item", id: item.ID, library: item.LibraryID, itemKind: item.Kind}
	genreRows, people := 0, 0
	for _, spec := range specs {
		if !allowed[spec.relation] || len(out.Rows) == 3 || spec.relation == "genre" && genreRows == 2 || spec.relation == "person" && people == 1 {
			continue
		}
		row, e := s.relationPage(viewer, owner, spec, "", relatedEntryLimit)
		if e != nil {
			return nil, e
		}
		if len(row.Entries) == 0 {
			continue
		}
		if spec.relation == "genre" {
			genreRows++
		}
		if spec.relation == "person" {
			people++
		}
		out.Rows = append(out.Rows, RelatedMovieRow{ID: row.ID, Relation: row.Relation, Provider: row.Provider,
			EvidenceID: row.EvidenceID, Heading: row.Title, HeadingText: relatedRowText(row.Relation, row.Title), Entries: row.Entries,
			NextCursor: row.NextCursor, HasMore: row.HasMore})
	}
	return out, nil
}

// --- viewer suggestions ----------------------------------------------------

type Suggestion struct {
	Entry  ContentEntry `json:"entry"`
	Reason string       `json:"reason"`
	Source string       `json:"source"`
	Score  float64      `json:"score"`
}

type Suggestions struct {
	Items       []Suggestion    `json:"items"`
	Total       int             `json:"total"`
	Revision    ContentRevision `json:"revision"`
	GeneratedAt string          `json:"generatedAt"`
}

// Suggestions ranks unwatched candidates for the viewer from the same facet
// overlap that drives the recommended home row, then tops up from the watchlist.
func (s *Service) Suggestions(r HomeRequest) (Suggestions, error) {
	if r.Profile != "" && r.Profile != r.Viewer.Profile || r.ViewerFence != "" && r.ViewerFence != r.Viewer.Fence {
		return Suggestions{}, ErrCursor
	}
	r = r.scoped()
	if err := s.prepareViewer(r.Viewer); err != nil {
		return Suggestions{}, err
	}
	r.Libraries = homeUnique(r.Libraries)
	limit := homeLimit(r.Limit)
	out := Suggestions{Items: []Suggestion{}}
	before, e := s.homeRevision(r.Libraries, r.Profile)
	if e != nil {
		return out, e
	}
	out.Revision = before
	candidates, e := s.recommendationCandidates(r, "recommended", "")
	if e != nil {
		return out, e
	}
	ids := []string{}
	for _, c := range candidates {
		if len(ids) == limit {
			break
		}
		ids = append(ids, c.ID)
	}
	entries, e := s.homeEngineEntries(r.Profile, ids)
	if e != nil {
		return out, e
	}
	for _, entry := range entries {
		out.Items = append(out.Items, Suggestion{Entry: entry, Reason: "Recommended for you", Source: "because_you_watched", Score: 0.6})
	}
	out.Total = len(out.Items)
	after, e := s.homeRevision(r.Libraries, r.Profile)
	if e != nil {
		return out, e
	}
	if before != after {
		return out, ErrStaleContinuation
	}
	out.GeneratedAt = r.now().UTC().Format("2006-01-02T15:04:05.000Z")
	return out, nil
}

// ItemRecommendationRows is the endpoint projection: it loads the item, its
// genres and the related rows under one revision fence.
// ItemRecommendationRows are a title page's rows; community is the owner's
// community-activity setting (Viewers also watched).
func (s *Service) ItemRecommendationRows(viewer Viewer, item string, limit int, community bool) ([]HomeRow, ContentRevision, error) {
	if e := s.VisibleItem(s.Context(), viewer, item); e != nil {
		return nil, ContentRevision{}, e
	}
	profile := viewer.Profile
	library, e := s.LibraryForItem(item)
	if e != nil {
		return nil, ContentRevision{}, e
	}
	before, e := s.ContentRevision(library, profile)
	if e != nil {
		return nil, before, e
	}
	loaded, e := s.Get(profile, item)
	if e != nil {
		return nil, before, e
	}
	genres, e := s.itemGenreList(item)
	if e != nil {
		return nil, before, e
	}
	out, e := s.ItemRecommendations(viewer, loaded, genres, limit, community)
	if e != nil {
		return nil, before, e
	}
	after, e := s.ContentRevision(library, profile)
	if e != nil {
		return nil, before, e
	}
	if before != after {
		return nil, before, ErrStaleContinuation
	}
	return out, before, nil
}

// itemGenres is one item's genre evidence for the recommendation engine.
// itemGenreList is an item's published genres (compact term sources), in
// the order the recommendation seed uses.
func (s *Service) itemGenreList(item string) ([]Genre, error) {
	rows, e := s.itemGenres(item, "")
	if e != nil {
		return nil, e
	}
	genres := []Genre{}
	for rows.Next() {
		var genre Genre
		if e = rows.Scan(&genre.ID, &genre.Name, &genre.Provider); e != nil {
			rows.Close()
			return nil, e
		}
		genres = append(genres, genre)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	return genres, nil
}

// Final relation boundary also protects legacy facet-specific rows. The same
// work map collapses editions, rejects negatives, and requires a visible child.
func (s *Service) filterRecommendationRows(viewer Viewer, rows []HomeRow) ([]HomeRow, error) {
	profile := viewer.Profile
	r := HomeRequest{Profile: profile, Libraries: viewer.Libraries, Restrictions: viewer.EffectiveRestrictions()}
	// Only the listed entries (and a bounded sample of each listed show's,
	// album's or book's members) are resolved; never the viewer's libraries.
	members, err := s.recommendationRowMembers(rows)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return []HomeRow{}, nil
	}
	base, args := recBase(r, members)
	// Entries are named by public id: both the member and its work's
	// canonical entity are returned as public ids (members carry the row's
	// public_id; a work's id is an integer entity id).
	query := base + ` SELECT pid(m.public_id),(SELECT pid(we.public_id) FROM catalog_entities we WHERE we.id=w.id),w.work FROM members m JOIN works w ON w.work=m.work JOIN personal p ON p.work=w.work WHERE p.negative=0
 UNION SELECT (SELECT pid(we.public_id) FROM catalog_entities we WHERE we.id=w.id),(SELECT pid(we.public_id) FROM catalog_entities we WHERE we.id=w.id),w.work FROM works w JOIN personal p ON p.work=w.work WHERE p.negative=0`
	found, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	canonical, work := map[string]string{}, map[string]string{}
	for found.Next() {
		var id, target, key string
		if err = found.Scan(&id, &target, &key); err != nil {
			found.Close()
			return nil, err
		}
		canonical[id] = target
		work[id] = key
	}
	err = found.Err()
	found.Close()
	if err != nil {
		return nil, err
	}
	out := []HomeRow{}
	for _, row := range rows {
		ids := []string{}
		seen := map[string]bool{}
		for _, entry := range row.Entries {
			target := canonical[entry.ID]
			if target == "" || seen[work[entry.ID]] {
				continue
			}
			seen[work[entry.ID]] = true
			ids = append(ids, target)
		}
		if len(ids) == 0 {
			continue
		}
		entries, e := s.homeEngineEntries(profile, ids)
		if e != nil {
			return nil, e
		}
		for i := range entries {
			entries[i].Playback = nil
			entries[i].Overview = ""
			entries[i].BackdropURL = ""
		}
		row.Entries = entries
		row.Total = len(entries)
		row.Limit = len(entries)
		out = append(out, row)
	}
	return out, nil
}

// recommendationRowMembers is the item ids behind a set of recommendation
// rows: item entries themselves, and up to five member items of each entity
// entry (a show's episodes, an album's songs, a book's parts), by index.
func (s *Service) recommendationRowMembers(rows []HomeRow) ([]string, error) {
	if len(rows) == 0 {
		return []string{}, nil
	}
	out, seen := []string{}, map[string]bool{}
	for _, row := range rows {
		for _, entry := range row.Entries {
			if seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			found, err := s.read().Query(`SELECT pid(e.public_id) FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?) AND k.playable=1 AND e.kind<>11
			 UNION ALL SELECT pid(public_id) FROM (SELECT item.public_id FROM catalog_entities parent JOIN catalog_episodes child ON child.show_id=parent.id JOIN catalog_entities item ON item.id=child.entity_id WHERE parent.public_id=pid_blob(?) LIMIT 5)
			 UNION ALL SELECT pid(public_id) FROM (SELECT item.public_id FROM catalog_entities parent JOIN catalog_songs child ON child.album_id=parent.id JOIN catalog_entities item ON item.id=child.entity_id WHERE parent.public_id=pid_blob(?) LIMIT 5)
			 UNION ALL SELECT pid(public_id) FROM (SELECT item.public_id FROM catalog_entities parent JOIN catalog_book_files child ON child.book_id=parent.id JOIN catalog_entities item ON item.id=child.entity_id WHERE parent.public_id=pid_blob(?) LIMIT 5)`, entry.ID, entry.ID, entry.ID, entry.ID)
			if err != nil {
				return nil, err
			}
			for found.Next() {
				var id string
				if err = found.Scan(&id); err != nil {
					found.Close()
					return nil, err
				}
				if !seen["item:"+id] {
					seen["item:"+id] = true
					out = append(out, id)
					if len(out) == relatedPoolLimit {
						found.Close()
						return out, nil
					}
				}
			}
			err = found.Err()
			found.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// relatedRowHeadings are the catalogue ids of the per-item row headings, with
// the fixed text before the one parameter (CON-19).
var relatedRowHeadings = map[string]struct{ code, prefix, param string }{
	"because_you_watched":  {"home.row.becauseYouWatched", "Because you watched ", "title"},
	"more_like":            {"home.row.moreLike", "More like ", "title"},
	"starring":             {"home.row.starring", "Starring ", "name"},
	"creator":              {"home.row.fromCreator", "From ", "name"},
	"viewers_also_watched": {"home.row.viewersAlsoWatched", "Viewers also watched", ""},
	"director":             {"home.row.directedBy", "Directed by ", "name"},
	"show":                 {"home.row.moreLike", "More like ", "title"},
	"collection":           {"home.row.moreFrom", "More from ", "name"},
}

// relatedRowText is a per-item row heading as {code, params, fallback}. A
// heading that doesn't carry its relation's fixed text keeps only the fallback.
func relatedRowText(relation, heading string) ServerText {
	spec, ok := relatedRowHeadings[relation]
	if !ok || !strings.HasPrefix(heading, spec.prefix) {
		return ServerText{Fallback: heading}
	}
	if spec.param == "" {
		return ServerText{Code: spec.code, Fallback: heading}
	}
	return ServerText{Code: spec.code, Params: map[string]string{spec.param: strings.TrimPrefix(heading, spec.prefix)}, Fallback: heading}
}

// DetailMoreLike pages every eligible local recommendation. Ranking is the
// number of shared catalogue facets, then catalogue quality and stable id.
// The revision pins both the ranking inputs and the viewer's access/preferences;
// a later publication is an explicit stale continuation, never a reorder.
func (s *Service) DetailMoreLike(viewer Viewer, item, cursor string, limit int) (HomeRow, error) {
	if err := s.VisibleItem(s.Context(), viewer, item); err != nil {
		return HomeRow{Entries: []ContentEntry{}}, err
	}
	source, err := s.Get(viewer.Profile, item)
	if err != nil {
		return HomeRow{Entries: []ContentEntry{}}, err
	}
	spec, ok, err := s.moreLikeRelation(viewer, source)
	if err != nil || !ok {
		return HomeRow{Entries: []ContentEntry{}}, errors.Join(err, sql.ErrNoRows)
	}
	return s.relationPage(viewer, relationOwner{kind: "item", id: item, library: source.LibraryID, itemKind: source.Kind}, spec, cursor, limit)
}

type DetailCollection struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	MemberCount int      `json:"memberCount"`
	Position    int      `json:"position"`
	YearFrom    int      `json:"yearFrom,omitempty"`
	YearTo      int      `json:"yearTo,omitempty"`
	PosterURLs  []string `json:"posterUrls"`
}

func (s *Service) detailCollections(viewer Viewer, item string) ([]DetailCollection, error) {
	rows, err := s.read().Query(`SELECT pid(e.public_id),e.title,m.order_key FROM catalog_entities item JOIN catalog_collection_members m ON m.item_id=item.id JOIN catalog_entities e ON e.id=m.collection_id JOIN catalog_collections c ON c.entity_id=e.id WHERE item.public_id=pid_blob(?) AND `+collectionHeadFence+` ORDER BY c.name_key,e.id LIMIT 2`, item)
	if err != nil {
		return nil, err
	}
	out := []DetailCollection{}
	orders := []string{}
	for rows.Next() {
		var c DetailCollection
		var order string
		if err = rows.Scan(&c.ID, &c.Name, &order); err != nil {
			rows.Close()
			return nil, err
		}
		c.PosterURLs = []string{}
		out = append(out, c)
		orders = append(orders, order)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		c := &out[i]
		visible, args := viewer.itemVisibilitySQL("member.id")
		values := append([]any{orders[i], c.ID}, args...)
		from := ` FROM catalog_collection_members m JOIN catalog_entities member ON member.id=m.item_id WHERE m.collection_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND ` + visible
		if err = s.read().QueryRow(`SELECT count(*),COALESCE(min(NULLIF(member.year,0)),0),COALESCE(max(NULLIF(member.year,0)),0),COALESCE(sum(m.order_key<=?),0)`+from, values...).Scan(&c.MemberCount, &c.YearFrom, &c.YearTo, &c.Position); err != nil {
			return nil, err
		}
		ids := []string{}
		page, err := s.read().Query(`SELECT pid(member.public_id)`+from+` ORDER BY m.order_key,member.id LIMIT 4`, append([]any{c.ID}, args...)...)
		if err != nil {
			return nil, err
		}
		for page.Next() {
			var id string
			if err = page.Scan(&id); err != nil {
				page.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		err = page.Err()
		page.Close()
		if err != nil {
			return nil, err
		}
		entries, err := s.homeEngineEntries(viewer.Profile, ids)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.PosterURL != "" {
				c.PosterURLs = append(c.PosterURLs, entry.PosterURL)
			}
		}
	}
	return out, nil
}
