package catalog

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var ErrCursor = requestinput.New("browse cursor is invalid or belongs to another viewer or query")

// ErrContentStart refuses a start index on a view that is not a grid, or
// together with a continuation cursor.
var ErrContentStart = requestinput.New("start opens a grid view (browse, collection, collections, artist) and cannot follow a cursor")
var ErrStaleContinuation = errors.New("catalog or viewer state changed; restart this query from its first page")
var ErrCollectionConflict = errors.New("a collection with that name already exists in this library")

type BrowseQuery struct {
	Library, Viewer, Profile, Sort, Direction, Category, Collection, Cursor, Search string
	Limit                                                                           int
	// Start opens the list at this index instead of the top (random access,
	// M10); it is ignored when Cursor continues a page.
	Start        int
	Restrictions identity.ContentRestrictions
}
type cursorScope struct {
	Profile    string `json:"profile,omitempty"`
	View       string `json:"view,omitempty"`
	Entity     string `json:"entity,omitempty"`
	Section    string `json:"section,omitempty"`
	Search     string `json:"search,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Library    string `json:"library"`
	Viewer     string `json:"viewer"`
	Sort       string `json:"sort"`
	Direction  string `json:"direction"`
	Category   string `json:"category"`
	Collection string `json:"collection,omitempty"`
}
type cursorValue struct {
	EvaluationAt string `json:"evaluationAt,omitempty"`
	// Keys carries the sort key of the last row the previous page returned, so
	// the next page can seek to it instead of skipping to it. The cursor is
	// opaque and HMAC-signed, so what it holds is this server's business; what a
	// client round-trips is unchanged.
	Keys []string `json:"keys,omitempty"`
	// Total is the raw count (including a capped filter's threshold+1 sentinel).
	// Reading's full witness permits reuse only after every cursor validation.
	Total int `json:"total,omitempty"`
	// TotalPresent distinguishes an exact zero from an absent legacy count.
	TotalPresent bool `json:"totalPresent,omitempty"`
	// Start is the index, in the whole query, of the next page's first row.
	Start           int         `json:"start,omitempty"`
	Fingerprint     string      `json:"fingerprint,omitempty"`
	CatalogRevision int64       `json:"catalogRevision"`
	ViewerRevision  int64       `json:"viewerRevision"`
	Scope           cursorScope `json:"scope"`
	Value           string      `json:"value"`
	ID              string      `json:"id"`
	Expires         int64       `json:"expires"`
}
type Category struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
	// ArtworkPaths carries up to 4 poster paths of the category's top items
	// by the library's default sort, for a 2x2 mosaic. Absent when the
	// category's top items carry no posters.
	ArtworkPaths []string `json:"artworkPaths,omitempty"`
}
type DiscoverySection struct {
	SeeAll *ContentSeeAll `json:"seeAll,omitempty"`
	// Why and WhyTitles explain a personal row, exactly as Home and the row's
	// own page do.
	Why       *ServerText        `json:"why,omitempty"`
	WhyTitles []ContentEntityRef `json:"whyTitles,omitempty"`
	Total     int                `json:"total"`
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	TitleText ServerText         `json:"titleText"`
	Items     []ContentEntry     `json:"items"`
}
type Discovery struct {
	Library  Library            `json:"library"`
	Sections []DiscoverySection `json:"sections"`
}

// catalogedNow is the added time a new item is stamped with.
func catalogedNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}
func (s *Service) cursorKey() ([]byte, error) {
	s.state.cursorOnce.Do(func() {
		var encoded string
		if s.state.cursorErr = s.read().QueryRow(`SELECT value FROM configuration WHERE key='catalog_cursor_key'`).Scan(&encoded); s.state.cursorErr != nil {
			return
		}
		s.state.cursorSecret, s.state.cursorErr = hex.DecodeString(encoded)
		if len(s.state.cursorSecret) != 32 {
			s.state.cursorErr = ErrCursor
		}
	})
	return s.state.cursorSecret, s.state.cursorErr
}
func (s *Service) encodeCursor(value cursorValue) (string, error) {
	revision, err := s.ContentRevision(value.Scope.Library, value.Scope.Profile)
	if err != nil {
		return "", err
	}
	return s.encodeRevisionCursor(value, revision)
}
func (s *Service) encodeRevisionCursor(value cursorValue, revision ContentRevision) (string, error) {
	key, err := s.cursorKey()
	if err != nil {
		return "", err
	}
	value.CatalogRevision = revision.Catalog
	value.ViewerRevision = revision.Viewer
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (s *Service) decodeCursor(raw string, scope cursorScope) (cursorValue, error) {
	revision, err := s.ContentRevision(scope.Library, scope.Profile)
	if err != nil {
		return cursorValue{}, err
	}
	return s.decodeRevisionCursor(raw, scope, revision)
}
func (s *Service) readSignedBrowseCursor(raw string) (cursorValue, error) {
	var out cursorValue
	if len(raw) > 4096 {
		return out, ErrCursor
	}
	payload, signature, ok := strings.Cut(raw, ".")
	if !ok {
		return out, ErrCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return out, ErrCursor
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return out, ErrCursor
	}
	key, err := s.cursorKey()
	if err != nil {
		return out, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(data, &out) != nil {
		return out, ErrCursor
	}
	return out, nil
}

func (s *Service) decodeRevisionCursor(raw string, scope cursorScope, revision ContentRevision) (cursorValue, error) {
	out, err := s.readSignedBrowseCursor(raw)
	if err != nil {
		return out, err
	}
	if out.Scope != scope {
		return out, ErrCursor
	}
	if out.Expires < time.Now().Unix() {
		out.Total = 0
		out.TotalPresent = false
		out.Expires = time.Now().Add(30 * time.Minute).Unix()
	}
	if out.CatalogRevision != revision.Catalog || out.ViewerRevision != revision.Viewer {
		out.Total = 0
		out.TotalPresent = false
	}
	return out, nil
}
func normalizeBrowse(q BrowseQuery) (BrowseQuery, error) {
	if q.Sort == "" {
		q.Sort = "title"
	}
	if q.Sort != "title" && q.Sort != "added" && q.Sort != "year" {
		return q, requestinput.New("sort must be title, added or year")
	}
	if q.Direction == "" {
		q.Direction = "asc"
		if q.Sort != "title" {
			q.Direction = "desc"
		}
	}
	if q.Direction != "asc" && q.Direction != "desc" {
		return q, requestinput.New("direction must be asc or desc")
	}
	q.Limit = pageLimit(q.Limit)
	return q, nil
}
func browseScope(q BrowseQuery) cursorScope {
	return cursorScope{Library: q.Library, Viewer: q.Viewer, Profile: q.Profile, Sort: q.Sort, Direction: q.Direction, Category: q.Category, Collection: q.Collection, Search: q.Search}
}
func (s *Service) movieLibrary(id string) (Library, error) {
	lib, err := s.library(id)
	if err == nil && lib.Kind != "movie" {
		err = requestinput.New("this discovery surface requires a movie library")
	}
	return lib, err
}
func categoryRange(category string) (int, error) {
	if category == "" {
		return 0, nil
	}
	raw, ok := strings.CutPrefix(category, "decade:")
	if !ok {
		return 0, requestinput.New("unknown category")
	}
	year, err := strconv.Atoi(raw)
	if err != nil || year < 1800 || year > 2190 || year%10 != 0 {
		return 0, requestinput.New("invalid decade category")
	}
	return year, nil
}

// Browse is the original GET surface. It translates its flat parameters into an
// expression query and runs the same engine the POST surface does, so the two
// can never drift apart; its cursor scope is unchanged, so existing clients keep
// their continuations.
func (s *Service) Browse(q BrowseQuery) ([]Item, string, error) {
	var err error
	q, err = normalizeBrowse(q)
	if err != nil {
		return nil, "", err
	}
	if _, err = s.movieLibrary(q.Library); err != nil {
		return nil, "", err
	}
	clauses := []BrowseNode{}
	if q.Search != "" {
		if len(q.Search) > 100 {
			return nil, "", requestinput.New("search prefix exceeds limit")
		}
		clauses = append(clauses, BrowseNode{Field: "title", Operator: "starts-with", Value: q.Search})
	}
	if q.Category != "" {
		node, e := browseCategoryPredicate(q.Category)
		if e != nil {
			return nil, "", e
		}
		clauses = append(clauses, *node)
	}
	if q.Collection != "" {
		var library string
		if err = s.read().QueryRow(`SELECT l.library_id FROM catalog_entities e JOIN catalog_collections c ON c.entity_id=e.id JOIN catalog_libraries l ON l.id=c.library_id WHERE e.public_id=pid_blob(?) AND e.retired=0`, q.Collection).Scan(&library); err != nil {
			return nil, "", err
		}
		if library != q.Library {
			return nil, "", requestinput.New("collection belongs to another library")
		}
		clauses = append(clauses, BrowseNode{Field: "collection", Operator: "contains", Value: q.Collection})
	}
	var query *BrowseNode
	switch len(clauses) {
	case 0:
	case 1:
		query = &clauses[0]
	default:
		query = &BrowseNode{All: clauses}
	}
	scope := browseScope(q)
	var window *BrowseRange
	if q.Start > 0 && q.Cursor == "" {
		window = &BrowseRange{Start: q.Start}
	}
	result, rows, err := s.browseSelect(BrowseRequest{Library: q.Library, Profile: q.Profile, ViewerFence: q.Viewer, Pivot: "movies", Query: query, Sort: []BrowseSortSelection{{Field: q.Sort, Direction: q.Direction}}, Limit: q.Limit, Cursor: q.Cursor, Range: window, Restrictions: q.Restrictions, scope: &scope})
	if err != nil {
		return nil, "", err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.id)
	}
	out, err := s.moviePage(q.Profile, ids)
	return out, result.PageInfo.NextCursor, err
}
func (s *Service) Categories(viewer Viewer, library string) ([]Category, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out []Category
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			out, readErr = s.WithContext(ctx).Categories(viewer, library)
			return readErr
		})
		return out, err
	}
	if !viewer.AllowsLibrary(library) {
		return nil, sql.ErrNoRows
	}
	if err := s.prepareViewer(viewer); err != nil {
		return nil, err
	}
	if _, err := s.movieLibrary(library); err != nil {
		return nil, err
	}
	if viewer.EffectiveRestrictions().Active() {
		key, generation, ready, err := s.publishedVisibilityClass(library, viewer.EffectiveRestrictions())
		if err != nil {
			return nil, err
		}
		if !ready {
			return []Category{}, nil
		}
		return s.compactMovieCategoriesForClass(library, key, generation)
	}
	return s.compactMovieCategories(library)
}

// Discover uses the same work projections as Home. Explicit resume rows retain
// their playable children; every exploratory row contains whole works only.
func (s *Service) DiscoverWithRestrictions(library, profile string, restrictions identity.ContentRestrictions, now time.Time) (Discovery, error) {
	now = s.recommendationNow(now)
	s = s.WithRecommendationRestrictions(restrictions)
	s.recSources = map[string]homeSource{}
	s.recComposition = newRecCompositionSources(s.Context())
	lib, err := s.library(library)
	out := Discovery{Library: lib, Sections: []DiscoverySection{}}
	if err != nil {
		return out, err
	}
	r := HomeRequest{Libraries: []string{library}, Profile: profile, Restrictions: restrictions, Now: now}
	// Resume is explicit intent, not a discovery recommendation.
	mode, title := "continue_watching", "Continue Watching"
	if lib.Kind == "music" || lib.Kind == "audiobook" {
		mode, title = "continue_listening", "Continue Listening"
	}
	clause, bind := ItemRestrictionSQL("candidate.entity", restrictions)
	args := []any{profile, idsJSON([]string{library})}
	args = append(args, bind...)
	ids := []string{}
	var rows *sql.Rows
	var e error
	if mode == "continue_watching" {
		// The same Continue Watching as Home's, for this library alone: titles in
		// progress and the next episode of a show being watched. Reading only the
		// started titles left a show whose next episode is due off its own
		// library's Discover while Home showed it.
		source, sourceErr := s.homeSourceBase(r, homeRowSpec{ID: "continue"})
		if sourceErr != nil {
			return out, sourceErr
		}
		rows, e = s.read().Query(`SELECT id FROM (`+source.base+`) ORDER BY ord DESC,id LIMIT 12`, source.args...)
	} else {
		// The listening walk's bounded window takes the profile first.
		rows, e = s.read().Query(`SELECT id FROM (`+homeContinueBase(mode)+`) candidate WHERE `+clause+` ORDER BY ord DESC,id LIMIT 12`, append([]any{profile}, args...)...)
	}
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > 0 {
		entries, e := s.homeEntries(profile, ids)
		if e != nil {
			return out, e
		}
		out.Sections = append(out.Sections, DiscoverySection{ID: mode, Title: title, TitleText: ServerText{Code: map[string]string{"continue_watching": "home.row.continueWatching", "continue_listening": "home.row.continueListening"}[mode], Fallback: title}, Items: entries, Total: len(entries)})
	}
	for _, spec := range []homeRowSpec{{ID: "recommended", Title: "Recommended for you"}, {ID: "trending_now", Title: "Trending now"}, {ID: "recent_" + library, Title: "Recently added", LibraryID: library}} {
		src, e := s.homeEngineSource(r, spec)
		if e != nil {
			return out, e
		}
		order := "ASC"
		if spec.LibraryID != "" {
			order = "DESC"
		}
		rows, e := s.read().Query(`SELECT id FROM (`+src.base+`) ORDER BY ord `+order+` LIMIT 12`, src.args...)
		if e != nil {
			return out, e
		}
		ids = []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return out, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if len(ids) == 0 || spec.ID == "trending_now" && len(ids) < recMinimumTrend || spec.ID == "recommended" && len(ids) < recMinimumShelf {
			continue
		}
		entries, e := s.homeEngineEntries(profile, ids)
		if e != nil {
			return out, e
		}
		if spec.LibraryID != "" {
			if e = s.homeRecentSeasonCounts(entries); e != nil {
				return out, e
			}
		}
		id := spec.ID
		if spec.LibraryID != "" {
			id = "recently_added"
		}
		total := len(entries)
		if src.total != nil {
			total = *src.total
		} else if e = s.read().QueryRow(`SELECT count(*) FROM (`+src.base+`)`, src.args...).Scan(&total); e != nil {
			return out, e
		}
		var seeAll *ContentSeeAll
		if spec.LibraryID != "" {
			seeAll = &ContentSeeAll{Pivot: map[string]string{"movie": "movies", "tv": "shows", "anime": "shows", "music": "albums", "audiobook": "books"}[lib.Kind], Sort: []BrowseSortSelection{{Field: "added", Direction: "desc"}}}
		}
		out.Sections = append(out.Sections, DiscoverySection{ID: id, Title: spec.Title, TitleText: spec.titleText(), Items: entries, Total: total, SeeAll: seeAll})
	}
	// The personal rows, scoped to this library, after Recommended (or first).
	personal, e := s.recPersonalRowsFor(r, lib.Kind)
	if e != nil {
		return out, e
	}
	var picks []DiscoverySection
	for _, row := range personal {
		ids := []string{}
		for i := 0; i < len(row.items) && i < 12; i++ {
			ids = append(ids, row.items[i].ID)
		}
		entries, e := s.homeEngineEntries(profile, ids)
		if e != nil {
			return out, e
		}
		picks = append(picks, DiscoverySection{ID: row.spec.ID, Title: row.spec.Title, TitleText: row.spec.titleText(), Items: entries, Total: len(row.items), SeeAll: recSeeAll(lib.Kind, row.filter), Why: row.spec.Why, WhyTitles: row.spec.WhyTitles})
	}
	at := 0
	for i, section := range out.Sections {
		if section.ID == "recommended" || section.ID == "continue_watching" || section.ID == "continue_listening" {
			at = i + 1
		}
	}
	out.Sections = append(out.Sections[:at], append(picks, out.Sections[at:]...)...)
	// A small cold-start library must not show the same complete inventory
	// twice under different headings. Keep the useful chronological shelf.
	rec, recent := -1, -1
	for i, row := range out.Sections {
		if row.ID == "recommended" {
			rec = i
		}
		if row.ID == "recently_added" {
			recent = i
		}
	}
	if rec >= 0 && recent >= 0 && out.Sections[rec].Total == out.Sections[recent].Total && out.Sections[rec].Total <= 12 {
		seen := map[string]bool{}
		for _, entry := range out.Sections[rec].Items {
			seen[entry.ID] = true
		}
		same := true
		for _, entry := range out.Sections[recent].Items {
			if !seen[entry.ID] {
				same = false
			}
		}
		if same {
			out.Sections = append(out.Sections[:rec], out.Sections[rec+1:]...)
		}
	}
	return out, nil
}

func (s *Service) Discover(library, profile string) (Discovery, error) {
	return s.DiscoverWithRestrictions(library, profile, identity.ContentRestrictions{}, time.Time{})
}
