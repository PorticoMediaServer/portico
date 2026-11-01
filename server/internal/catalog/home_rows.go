package catalog

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Home rows are a server-owned composition. Clients receive membership,
// ordering, policy and paging shape; they never rank or hide rows themselves.
// Every row is expressed as one candidate projection (id, ord) so counting,
// keyset paging and anchored ranges share a single bounded implementation.

const (
	HomeRowDefaultLimit = 12
	HomeRowMaxLimit     = 60
	homeCursorLifetime  = 30 * time.Minute
)

var ErrHomeRowUnknown = requestinput.New("that home row does not exist for this viewer")
var ErrHomeLayoutRow = requestinput.New("home layout refers to a row that does not exist")

type HomeSeeAll struct {
	Type       string                `json:"type"`
	Group      string                `json:"group,omitempty"`
	SourceID   string                `json:"sourceId,omitempty"`
	LibraryID  string                `json:"libraryId,omitempty"`
	Pivot      string                `json:"pivot,omitempty"`
	Query      *BrowseNode           `json:"query,omitempty"`
	Sort       []BrowseSortSelection `json:"sort,omitempty"`
	RowID      string                `json:"rowId,omitempty"`
	ResourceID string                `json:"resourceId,omitempty"`
}

type HomeRow struct {
	ViewerFence string `json:"viewerFence"`
	// Deferred rows publish their policy and endpoint before their preview.
	Deferred bool `json:"deferred,omitempty"`
	// Children are the server-selected rows in the personal recommendation family.
	Children           []HomeRow           `json:"children,omitempty"`
	Resumes            []HomeResume        `json:"resumes,omitempty"`
	Reason             *HomeReason         `json:"reason,omitempty"`
	Collection         *HomeCollection     `json:"collection,omitempty"`
	Live               *HomeLive           `json:"live,omitempty"`
	Period             *HomePeriod         `json:"period,omitempty"`
	SubtitleText       *ServerText         `json:"subtitleText,omitempty"`
	Platforms          []string            `json:"platforms,omitempty"`
	SeeAll             *HomeSeeAll         `json:"seeAll,omitempty"`
	ID                 string              `json:"id"`
	Title              string              `json:"title"`
	TitleText          ServerText          `json:"titleText"`
	Kind               string              `json:"kind"`
	ArtworkShape       string              `json:"artworkShape"`
	Explanation        string              `json:"explanation,omitempty"`
	Why                *ServerText         `json:"why,omitempty"`
	WhyTitles          []ContentEntityRef  `json:"whyTitles,omitempty"`
	DisplayTargets     []HomeDisplayTarget `json:"displayTargets,omitempty"`
	Endpoint           string              `json:"endpoint"`
	LibraryID          string              `json:"libraryId,omitempty"`
	PrivacySensitivity string              `json:"privacySensitivity"`
	PolicyState        string              `json:"policyState"`
	Relation           string              `json:"relation,omitempty"`
	// Family groups rows the layout arranges as one ("for_you": the personal
	// rows, which vary with the profile and the day).
	Family        string         `json:"family,omitempty"`
	Provider      string         `json:"provider,omitempty"`
	EvidenceID    string         `json:"evidenceId,omitempty"`
	Priority      int            `json:"priority"`
	CacheTTLMs    int            `json:"cacheTtlMs"`
	Required      bool           `json:"required"`
	Hideable      bool           `json:"hideable"`
	Reorderable   bool           `json:"reorderable"`
	Critical      bool           `json:"critical"`
	CursorCapable bool           `json:"cursorCapable"`
	Entries       []ContentEntry `json:"entries"`
	// Total is the row's exact number of entries. A Home document row carries
	// it only when it is known without counting the row (a short first page, or
	// a maintained count); a row's own page request always carries it.
	Total      *int            `json:"total,omitempty"`
	Start      int             `json:"start"`
	Limit      int             `json:"limit"`
	HasMore    bool            `json:"hasMore"`
	NextCursor string          `json:"nextCursor"`
	AnchorID   string          `json:"anchorId,omitempty"`
	Revision   ContentRevision `json:"revision"`
	// lead is the first entry's own order value, retained for bounded paging.
	lead string
}

type HomeLayout struct {
	Revision     int64    `json:"revision"`
	RowOrder     []string `json:"rowOrder"`
	HiddenRowIDs []string `json:"hiddenRowIds"`
}

type HomeDocument struct {
	ServerID    string `json:"serverId"`
	ViewerFence string `json:"viewerFence"`
	// PrivacyFence changes only when who the viewer is or what they may see changes;
	// a library added or removed changes ViewerFence alone. Optional for old servers.
	PrivacyFence string          `json:"privacyFence,omitempty"`
	Rows         []HomeRow       `json:"rows"`
	Spotlight    []HomeSpotlight `json:"spotlight"`
	Layout       HomeLayout      `json:"layout"`
	Revision     ContentRevision `json:"revision"`
	GeneratedAt  string          `json:"generatedAt"`
}

// HomeRowPage is the paging intent for one row. Cursor and start are exclusive;
// anchorId re-resolves start against the live projection after a publication.
type HomeRowPage struct {
	Cursor   string
	Revision string
	AnchorID string
	Limit    int
	Start    int
	Group    string
	SourceID string
}

type homeRowSpec struct {
	ID           string
	Title        string
	TitleCode    string // the catalogue id, when not the fixed rows' own
	Family       string
	TitleParams  map[string]string
	Kind         string
	ArtworkShape string
	Explanation  string
	LibraryID    string
	SeeAll       *HomeSeeAll
	Privacy      string
	PolicyState  string
	Direction    string
	Priority     int
	// Rank orders rows of one priority (the libraries' recent shelves, in library order).
	Rank        int
	CacheTTL    int
	Required    bool
	Hideable    bool
	Reorderable bool
	Critical    bool
	Cursors     bool
	// DefaultHidden rows stay off Home until the viewer places them: a
	// layout shows one only when it orders the row and does not hide it.
	DefaultHidden bool
	// Why names why the row is here, and WhyTitles the profile's own titles
	// behind a personal row. Both are carried unchanged to every projection.
	Why       *ServerText
	WhyTitles []ContentEntityRef
}

// hiddenIn says whether a layout hides the row.
func (spec homeRowSpec) hiddenIn(order []string, hidden map[string]bool) bool {
	if spec.Required || !spec.Hideable {
		return false
	}
	if hidden[spec.ID] {
		return true
	}
	if !spec.DefaultHidden {
		return false
	}
	for _, id := range order {
		if id == spec.ID {
			return false
		}
	}
	return true
}

// The recent shelves: one per library ("Recently added in Movies"). Libraries
// are never mixed in one shelf: a row is posters or squares, not both.
const homeRecentPrefix = "recent_"

type homeSource struct {
	base string
	args []any
	cap  int
	// total is the row's exact count when a maintained counter can answer it, so
	// the composition does not run a `count(*)` over the whole projection for a
	// number the client renders. Nil means "count the projection".
	total *int
	// count, when set, is a query for the same exact count as `count(*)` over
	// base that skips work the count does not need (the order values), with
	// countArgs. It describes base as built, so wrapping or capping base drops it.
	count     string
	countArgs []any
	// walk, when set, answers a descending page without ordering the whole
	// projection (home_recent_walk.go), or declines and the page runs the full
	// query. Like count it describes base as built.
	walk homeWalker
	// selfRestricted marks a projection whose SQL already applies the viewer's
	// restriction itself, so the generic item wrap must not run again.
	selfRestricted bool
	evaluationAt   string
	fingerprint    string
	// candidates is an engine source's immutable ranking within this request.
	// Recommended suppresses Trending's preview using this same scored list.
	candidates []recCandidate
}

func (spec homeRowSpec) descriptor() HomeRow {
	row := HomeRow{ID: spec.ID, Title: spec.Title, TitleText: spec.titleText(), Kind: spec.Kind, ArtworkShape: spec.ArtworkShape, Explanation: spec.Explanation,
		SeeAll: spec.destination(), Endpoint: "/v1/home/rows/" + spec.ID, LibraryID: spec.LibraryID, PrivacySensitivity: spec.Privacy, PolicyState: spec.PolicyState,
		Priority: spec.Priority, CacheTTLMs: spec.CacheTTL * 1000, Required: spec.Required, Hideable: spec.Hideable,
		Reorderable: spec.Reorderable, Critical: spec.Critical, CursorCapable: spec.Cursors, Family: spec.Family, Why: spec.Why, WhyTitles: spec.WhyTitles, Entries: []ContentEntry{}}
	if spec.ID == "continue_books" {
		row.Platforms = []string{"web", "phone"}
	}
	return row
}

func (spec homeRowSpec) destination() *HomeSeeAll {
	if spec.SeeAll != nil {
		return spec.SeeAll
	}
	return &HomeSeeAll{Type: "row", RowID: spec.ID}
}

// homeRowTitleCodes are the client catalogue ids for the fixed Home rows.
var homeRowTitleCodes = map[string]string{
	"continue": "next.home.continueWatching", "new_on_server": "next.home.newOnServer", "because_you_watched": "next.home.becauseHeading", "featured_collection": "next.home.collection", "on_now": "next.home.onNow", "community_watching": "next.home.popularOnServer", "listen_next": "next.home.continueListening", "continue_books": "next.home.continueBooks", "recommended": "home.row.recommended", "trending_now": "home.row.trending", recFamily: "home.row.moreRecommendations",
}

// titleText is the row title as {code, params, fallback} (CON-19).
func (spec homeRowSpec) titleText() ServerText {
	code := homeRowTitleCodes[spec.ID]
	if spec.TitleCode != "" {
		code = spec.TitleCode
	}
	if code == "" && strings.HasPrefix(spec.ID, homeRecentPrefix) {
		code = "home.row.recentlyAdded"
		if spec.TitleParams["library"] != "" {
			code = "home.row.recentlyAddedIn"
		}
	}
	return ServerText{Code: code, Params: spec.TitleParams, Fallback: spec.Title}
}

// homeRowBelow reports a row known to hold fewer than n entries. Engine rows
// always know their total; a row without one filled its page and more.
func homeRowBelow(row HomeRow, n int) bool {
	return row.Total != nil && *row.Total < n
}

func homeLimit(limit int) int {
	if limit <= 0 {
		return HomeRowDefaultLimit
	}
	if limit > HomeRowMaxLimit {
		return HomeRowMaxLimit
	}
	return limit
}

func homeUnique(ids []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// A personal row is a list of what one person has done, and it is bounded by
// that person's history rather than by the library. Every one of them is written
// as a CROSS JOIN from the personal table outwards, which in SQLite is not a
// different join — it is the same join with the order fixed.
//
// The order has to be fixed because the planner gets it wrong here, and gets it
// wrong only under a restriction. `i.library_id IN (SELECT value FROM
// json_each(?))` looks selective and countable to it while `pi.profile_id=?` has
// no statistics, so when the viewer's restriction wraps the projection the
// planner flips the loops and reads every item in the library, probing the
// personal table for each. Measured on the release catalogue, the watchlist row
// for a restricted viewer: 2.02 seconds returning no rows, and 135 microseconds
// with the order fixed.
//
// --- candidate projections -------------------------------------------------

const homeRetired = `NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=%s)`

// homeVisible requires a live entity and its available, non-DVR-retired item
// projection.
func homeVisible(alias string) string {
	return fmt.Sprintf(`EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_item_availability v ON v.entity_id=e.id WHERE e.id=%s AND e.retired=0 AND v.available=1 AND v.retired=0 AND EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets asset ON asset.id=link.asset_id WHERE link.entity_id=e.id AND link.available=1 AND asset.available=1))`, alias)
}

func homeContinueBase(mode string) string {
	kind := `k.playable=1 AND k.listening=0 AND i.kind<>11`
	libraries := `a.library_id IN(SELECT value FROM json_each(?))`
	activity := `progress_activity a`
	if mode == "continue_movies" {
		kind = `i.kind=1`
		libraries = `a.library_id IN(SELECT l.id FROM libraries l WHERE l.kind='movie' AND l.id IN(SELECT value FROM json_each(?)))`
	}
	if mode == "continue_listening" {
		kind = `(k.playable=1 AND k.listening=1 AND (i.kind<>9 OR EXISTS(SELECT 1 FROM catalog_book_files f JOIN book_resume b ON b.book_id=f.book_id AND b.profile_id=a.profile_id WHERE f.entity_id=i.id AND b.item_id=i.id)))`
		// Page the profile's actual unfinished activity through its maintained
		// index. An arbitrary history window cannot define a row's true end.
		activity = `(SELECT * FROM progress_activity INDEXED BY progress_home_profile_recent WHERE profile_id=? AND state!='ended') a`
	}
	// id names the public id for hydration, entity the integer id for
	// restriction predicates, ord the activity row's own column so the planner
	// can take the row order straight from the index and stop at the row's limit.
	// Progress positions are milliseconds (unit 0); asset durations are seconds.
	return `SELECT pid(i.public_id) AS id,a.item_id AS entity,a.updated_at AS ord FROM ` + activity + ` CROSS JOIN progress p ON p.profile_id=a.profile_id AND p.item_id=a.item_id JOIN catalog_entities i ON i.id=a.item_id JOIN catalog_kinds k ON k.id=i.kind` +
		` WHERE a.profile_id=? AND ` + libraries + ` AND i.retired=0 AND EXISTS(SELECT 1 FROM catalog_libraries cl WHERE cl.id=i.library_id AND cl.library_id=a.library_id) AND ` + kind +
		` AND ` + homeVisible("i.id") + ` AND ` + fmt.Sprintf(homeRetired, "i.id") + ` AND a.state!='ended' AND NOT EXISTS(SELECT 1 FROM continue_dismissals d WHERE d.profile_id=p.profile_id AND d.item_id=p.item_id AND d.playback_id=p.playback_id) AND p.position>0` +
		` AND p.position<(SELECT COALESCE(max(NULLIF(COALESCE(l.end_seconds,s.duration)-l.start_seconds,0))*1000-CASE WHEN i.kind=9 THEN 0 ELSE 3000 END,9223372036854775807) FROM catalog_asset_links l JOIN catalog_assets s ON s.id=l.asset_id WHERE l.entity_id=i.id AND s.available=1 AND l.available=1)`
}

// homeSource wraps every row's candidate projection in the viewer's content
// restriction. Wrapping the finished projection rather than editing each of the
// nine bases means the row count, the anchor rank and the keyset page all see the
// same rows, and a new row type cannot forget the predicate.
func (s *Service) homeSource(r HomeRequest, spec homeRowSpec) (homeSource, error) {
	src, err := s.homeSourceBase(r, spec)
	if err != nil {
		return src, err
	}
	if !src.selfRestricted {
		// Candidate ids are public ids; the predicate takes integer entity ids.
		restriction, restrictionArgs := ItemRestrictionSQL("(SELECT id FROM catalog_entities WHERE public_id=pid_blob(candidate.id))", r.Restrictions)
		if restriction != "1" {
			src.base = `SELECT candidate.id,candidate.ord FROM (` + src.base + `) candidate WHERE ` + restriction
			src.args = append(src.args, restrictionArgs...)
			src.count, src.countArgs, src.walk = "", nil, nil
		}
	}
	if src.cap > 0 {
		src.base = `SELECT id,ord FROM (` + src.base + `) ORDER BY ord DESC,id DESC LIMIT ` + strconv.Itoa(src.cap)
		src.count, src.countArgs, src.walk = "", nil, nil
	}
	return src, nil
}

func (s *Service) homeSourceBase(r HomeRequest, spec homeRowSpec) (homeSource, error) {
	raw, _ := json.Marshal(r.Libraries)
	libraries := string(raw)
	switch {
	case spec.ID == "new_on_server":
		return s.homeNewServerSource(r)
	case spec.ID == "because_you_watched":
		_, c, e := s.homeBecause(r)
		return candidateSource(c), e
	case spec.ID == "listen_next" || spec.ID == "continue_books":
		return s.homeAudioSource(r, spec.ID == "continue_books")
	case spec.ID == "community_watching":
		return s.homeCommunitySource(r, spec)
	case spec.ID == "continue":

		cutoffs, premieres, maximum, err := s.continueWatchingPolicy(r.Libraries, r.now())
		if err != nil {
			return homeSource{}, err
		}
		live, err := s.homeContinueSource(r, libraries, cutoffs, premieres, maximum)
		if err != nil {
			return homeSource{}, err
		}
		removed, err := s.homeRemovedSource(r.Viewer)
		if err != nil {
			return homeSource{}, err
		}
		var revision int64
		if err = s.read().QueryRow(`SELECT COALESCE((SELECT revision FROM catalog_removed_title_revisions WHERE profile_id=?),0)`, r.Profile).Scan(&revision); err != nil {
			return homeSource{}, err
		}
		return homeSource{base: `SELECT id,ord FROM (` + live.base + ` UNION ALL ` + removed.base + `) ORDER BY ord DESC,id DESC LIMIT ` + strconv.Itoa(maximum), args: append(live.args, removed.args...), selfRestricted: true, fingerprint: fmt.Sprintf("%s:removed:%d", live.fingerprint, revision)}, nil
	case spec.ID == "trending_now":
		return s.homeWeeklyTrendingSource(r)
	case spec.ID == "recommended" || strings.HasPrefix(spec.ID, recFamily+":"):
		return s.homeEngineSource(r, spec)
	case strings.HasPrefix(spec.ID, homeViewPrefix):
		return s.homeViewSource(r, spec)
	case strings.HasPrefix(spec.ID, "recent_"):
		return s.homeEngineSource(r, spec)
	}
	return homeSource{}, ErrHomeRowUnknown
}

// --- row catalogue ---------------------------------------------------------

func homeArtwork(kind string) string {
	if kind == "music" || kind == "audiobook" {
		return "square"
	}
	return "poster"
}

func (s *Service) homeSpecs(r HomeRequest) ([]homeRowSpec, error) {
	specs := []homeRowSpec{
		{ID: "continue", Title: "Continue Watching", Kind: "continue", ArtworkShape: "landscape", Priority: 10, CacheTTL: 45, Required: true, Reorderable: true, Critical: true, Cursors: true, Privacy: "personal", PolicyState: "available"},
		{ID: "new_on_server", Title: "New on your server", Kind: "recent", ArtworkShape: "poster", Priority: 20, CacheTTL: 120, Hideable: true, Reorderable: true, Cursors: true, Privacy: "catalog", PolicyState: "available"},
		{ID: "because_you_watched", Title: "Because you watched", Kind: "recommendation", ArtworkShape: "poster", Direction: "asc", Priority: 30, CacheTTL: 60, Hideable: true, Reorderable: true, Cursors: true, Privacy: "personal", PolicyState: "available"},
		{ID: "featured_collection", Title: "Collection", Kind: "collection_banner", ArtworkShape: "poster", Priority: 40, CacheTTL: 120, Hideable: true, Reorderable: true, Privacy: "catalog", PolicyState: "available"},
		{ID: "community_watching", Title: "Popular on this server", Kind: "community", ArtworkShape: "poster", Direction: "asc", Priority: 60, CacheTTL: 60, DefaultHidden: true, Hideable: true, Reorderable: true, Cursors: true, Privacy: "aggregated", PolicyState: "available"},
		{ID: "listen_next", Title: "Continue Listening", Kind: "listening", ArtworkShape: "square", Direction: "desc", Priority: 70, CacheTTL: 45, Hideable: true, Reorderable: true, Cursors: true, Privacy: "personal", PolicyState: "available"},
		{ID: "continue_books", Title: "Continue your books", Kind: "continue_books", ArtworkShape: "square", Direction: "desc", Priority: 80, CacheTTL: 45, Hideable: true, Reorderable: true, Cursors: true, Privacy: "personal", PolicyState: "available"},
		{ID: "recommended", Title: "Recommended for you", Kind: "recommendation", ArtworkShape: "poster", Direction: "asc", Priority: 90, CacheTTL: 300, Hideable: true, Reorderable: true, Cursors: true, Privacy: "personal", PolicyState: "available"},
		{ID: recFamily, Title: "More recommendations", Kind: "family", Family: recFamily, ArtworkShape: "poster", Direction: "asc", Priority: 91, CacheTTL: 300, Hideable: true, Reorderable: true, Privacy: "personal", PolicyState: "available"},
		{ID: "trending_now", Title: "Trending now", Kind: "recommendation", ArtworkShape: "poster", Direction: "asc", Priority: 92, CacheTTL: 300, Hideable: true, Reorderable: true, Cursors: true, Privacy: "catalog", PolicyState: "available"},
	}
	if r.LiveAvailable {
		specs = append(specs, homeRowSpec{ID: "on_now", Title: "On now", SeeAll: &HomeSeeAll{Type: "guide"}, Kind: "live", ArtworkShape: "landscape", Priority: 50, CacheTTL: 0, Hideable: true, Reorderable: true, Cursors: true, Privacy: "personal", PolicyState: "available"})
	}
	for i := range specs {
		if specs[i].ID == "community_watching" && !r.CommunityActivity {
			specs[i].PolicyState = "disabled_by_owner"
		}
	}
	return append(specs, s.homeViewSpecs(r)...), nil
}

func (s *Service) homeLibraries(ids []string) ([]Library, error) {
	out := []Library{}
	if len(ids) == 0 {
		return out, nil
	}
	raw, _ := json.Marshal(ids)
	rows, e := s.read().Query(`SELECT id,name,kind FROM libraries WHERE id IN(SELECT value FROM json_each(?)) ORDER BY position,name,id`, string(raw))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var library Library
		if e = rows.Scan(&library.ID, &library.Name, &library.Kind); e != nil {
			return nil, e
		}
		library.DefaultView = DefaultLibraryView(library.Kind)
		out = append(out, library)
	}
	return out, rows.Err()
}

// --- paging ----------------------------------------------------------------

type homeCursor struct {
	EvaluationAt string `json:"evaluationAt,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	Row          string `json:"row"`
	Profile      string `json:"profile"`
	Viewer       string `json:"viewer"`
	Direction    string `json:"direction"`
	Value        string `json:"value"`
	ID           string `json:"id"`
	Limit        int    `json:"limit"`
	Offset       int    `json:"offset"`
	Catalog      int64  `json:"catalog"`
	Revision     int64  `json:"revision"`
	Expires      int64  `json:"expires"`
}

func (s *Service) encodeHomeCursor(value homeCursor) (string, error) {
	key, e := s.cursorKey()
	if e != nil {
		return "", e
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return "", e
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Service) readSignedHomeCursor(raw string) (homeCursor, error) {
	var out homeCursor
	if len(raw) > 4096 {
		return out, ErrCursor
	}
	payload, signature, ok := strings.Cut(raw, ".")
	if !ok {
		return out, ErrCursor
	}
	data, e := base64.RawURLEncoding.DecodeString(payload)
	if e != nil {
		return out, ErrCursor
	}
	sig, e := base64.RawURLEncoding.DecodeString(signature)
	if e != nil {
		return out, ErrCursor
	}
	key, e := s.cursorKey()
	if e != nil {
		return out, e
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(data, &out) != nil {
		return out, ErrCursor
	}
	return out, nil
}

func (s *Service) decodeHomeCursor(raw string, expected homeCursor, revision ContentRevision) (homeCursor, error) {
	out, err := s.readSignedHomeCursor(raw)
	if err != nil {
		return out, err
	}
	if out.Row != expected.Row || out.Profile != expected.Profile || out.Viewer != expected.Viewer || out.Direction != expected.Direction || out.Limit != expected.Limit {
		return out, ErrCursor
	}
	if out.Expires <= time.Now().Unix() {
		return out, ErrCursor
	}
	if out.Fingerprint != expected.Fingerprint || out.Revision != revision.Viewer {
		return out, ErrStaleContinuation
	}
	// A computed list (recommendations, personal rows, trending) is fenced by its
	// fingerprint: the same fingerprint is the same list in the same order, so
	// catalogue writes while a scan settles do not strand its next page. Keyset
	// rows follow the order revision, which moves when their membership does.
	if expected.Fingerprint == "" && out.Catalog != revision.Catalog {
		return out, ErrStaleContinuation
	}

	return out, nil
}

func homeOrder(direction string) (string, string) {
	if direction == "asc" {
		return "ASC", ">"
	}
	return "DESC", "<"
}

// ords are the page's order values, one per id.
// total is nil only when exactTotal is false and the first page cannot answer it.
func (s *Service) homeRowIDs(spec homeRowSpec, src homeSource, page HomeRowPage, revision ContentRevision, viewer string, profile string, exactTotal bool) (ids, ords []string, total *int, start int, next, lead string, err error) {
	if page.Revision != "" && page.Revision != fmt.Sprintf("%d:%d", revision.Catalog, revision.Viewer) {
		return nil, nil, nil, 0, "", "", ErrStaleContinuation
	}
	order, operator := homeOrder(spec.Direction)
	limit := homeLimit(page.Limit)
	// SQLite reprepares a VM whenever a bound LIMIT/OFFSET changes or is bound
	// again. Only these validated integers become SQL literals; viewer data,
	// cursor values and all authority predicates remain bound parameters.
	start = page.Start
	if start < 0 {
		return nil, nil, nil, 0, "", "", ErrCursor
	}
	// The count is asked for only when the page cannot answer it.
	//
	// Every row's total is exact and appears in the JSON, and it used to be a
	// second execution of the row's whole projection — which for the personal
	// rows is the expensive half: the up-next row's projection alone measured
	// 11.5 ms, and it ran twice. A first page that comes back short has counted
	// the row by reading it, and a short first page is what these rows almost
	// always produce.
	countTotal := func() (int, error) {
		if src.total != nil {
			return *src.total, nil
		}
		var total int
		if src.count != "" {
			e := s.read().QueryRow(src.count, src.countArgs...).Scan(&total)
			return total, e
		}
		e := s.read().QueryRow(`SELECT count(*) FROM (`+src.base+`)`, src.args...).Scan(&total)
		return total, e
	}
	var e error
	var count int
	if page.Cursor != "" || page.AnchorID != "" || start != 0 || src.total != nil {
		if count, e = countTotal(); e != nil {
			return nil, nil, nil, 0, "", "", e
		}
	}
	base := homeCursor{EvaluationAt: src.evaluationAt, Fingerprint: src.fingerprint, Row: spec.ID, Profile: profile, Viewer: viewer, Direction: spec.Direction, Limit: limit, Catalog: revision.Catalog, Revision: revision.Viewer}
	if page.Cursor != "" {
		if page.Start != 0 || page.AnchorID != "" {
			return nil, nil, nil, 0, "", "", ErrCursor
		}
		cursor, e := s.decodeHomeCursor(page.Cursor, base, revision)
		if e != nil {
			return nil, nil, nil, 0, "", "", e
		}
		if src.fingerprint != "" {
			// Later pages of a computed list keep the revision of its first page.
			base.Catalog = cursor.Catalog
		}
		if src.walk != nil && order == "DESC" {
			walked, walkedOrds, ok, e := src.walk(homeWalkRequest{take: limit + 1, after: &[2]string{cursor.Value, cursor.ID}})
			if e != nil {
				return nil, nil, nil, 0, "", "", e
			}
			if ok {
				ids, ords, next, _, lead, e := s.homePage(walked, walkedOrds, base, cursor.Offset, limit)
				return ids, ords, &count, cursor.Offset, next, lead, e
			}
		}
		args := append(append([]any{}, src.args...), cursor.Value, cursor.Value, cursor.ID)
		query := `SELECT id,ord FROM (` + src.base + `) WHERE (ord` + operator + `? OR (ord=? AND id` + operator + `?)) ORDER BY ord ` + order + `,id ` + order + ` LIMIT ` + strconv.Itoa(limit+1)
		ids, ords, next, _, lead, e := s.homeScanPage(query, args, base, cursor.Offset, limit)
		return ids, ords, &count, cursor.Offset, next, lead, e
	}
	if page.AnchorID != "" {
		anchor := page.AnchorID
		if spec.ID == "new_on_server" {
			// Delivery cards navigate to a real episode or season, while the
			// stable row cursor and order belong to the canonical show work.
			if err := s.read().QueryRow(`SELECT COALESCE((SELECT pid(parent.public_id) FROM catalog_entities child JOIN catalog_entities parent ON parent.id=CASE child.kind WHEN 4 THEN (SELECT show_id FROM catalog_episodes WHERE entity_id=child.id) WHEN 3 THEN (SELECT show_id FROM catalog_seasons WHERE entity_id=child.id) ELSE child.id END WHERE child.public_id=pid_blob(?)),?)`, anchor, anchor).Scan(&anchor); err != nil {
				return nil, nil, nil, 0, "", "", err
			}
		}
		args := append(append([]any{}, src.args...), anchor)
		var rank int
		rankErr := s.read().QueryRow(`SELECT position FROM (SELECT id,ROW_NUMBER() OVER (ORDER BY ord `+order+`,id `+order+`)-1 AS position FROM (`+src.base+`)) WHERE id=?`, args...).Scan(&rank)
		if rankErr == nil {
			start = rank
		} else if !errors.Is(rankErr, sql.ErrNoRows) {
			return nil, nil, nil, 0, "", "", rankErr
		}
	}
	if src.total != nil || page.AnchorID != "" || start != 0 {
		if count == 0 {
			start = 0
		} else if start > count-1 {
			start = count - 1
		}
	}
	var scanned int
	walked := false
	if src.walk != nil && order == "DESC" && page.AnchorID == "" {
		var walkedIDs, walkedOrds []string
		if walkedIDs, walkedOrds, walked, e = src.walk(homeWalkRequest{skip: start, take: limit + 1}); e != nil {
			return nil, nil, nil, 0, "", "", e
		}
		if walked {
			ids, ords, next, scanned, lead, e = s.homePage(walkedIDs, walkedOrds, base, start, limit)
		}
	}
	if !walked {
		args := append([]any{}, src.args...)
		query := `SELECT id,ord FROM (` + src.base + `) ORDER BY ord ` + order + `,id ` + order + ` LIMIT ` + strconv.Itoa(limit+1) + ` OFFSET ` + strconv.Itoa(start)
		ids, ords, next, scanned, lead, e = s.homeScanPage(query, args, base, start, limit)
	}
	if e != nil {
		return nil, nil, nil, 0, "", "", e
	}
	if src.total == nil && page.AnchorID == "" && start == 0 {
		if scanned <= limit {
			// The page is the whole row: it asked for one more than it wanted and
			// did not get it, so the count is what it read.
			count = scanned
		} else if !exactTotal {
			return ids, ords, nil, start, next, lead, nil
		} else if count, e = countTotal(); e != nil {
			return nil, nil, nil, 0, "", "", e
		}
	}
	return ids, ords, &count, start, next, lead, nil
}

// homeScanPage reads one page and its order values. lead is the first entry's order value.
func (s *Service) homeScanPage(query string, args []any, base homeCursor, offset, limit int) (ids, orders []string, next string, scanned int, lead string, e error) {
	rows, e := s.read().Query(query, args...)
	if e != nil {
		return nil, nil, "", 0, "", e
	}
	ids = []string{}
	orders = []string{}
	for rows.Next() {
		var id, ord string
		if e = rows.Scan(&id, &ord); e != nil {
			rows.Close()
			return nil, nil, "", 0, "", e
		}
		ids = append(ids, id)
		orders = append(orders, ord)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, nil, "", 0, "", e
	}
	return s.homePage(ids, orders, base, offset, limit)
}

// homePage is a page from up to limit+1 ordered rows: the first limit, and a
// cursor after them when there was one more.
func (s *Service) homePage(ids, orders []string, base homeCursor, offset, limit int) (_, _ []string, next string, scanned int, lead string, e error) {
	scanned = len(ids)
	if len(orders) > 0 {
		lead = orders[0]
	}
	if len(ids) > limit {
		ids = ids[:limit]
		orders = orders[:limit]
		cursor := base
		cursor.Value = orders[len(orders)-1]
		cursor.ID = ids[len(ids)-1]
		cursor.Offset = offset + limit
		cursor.Expires = time.Now().Add(homeCursorLifetime).Unix()
		if next, e = s.encodeHomeCursor(cursor); e != nil {
			return nil, nil, "", 0, "", e
		}
	}
	return ids, orders, next, scanned, lead, nil
}

func (s *Service) homeEntries(profile string, ids []string) ([]ContentEntry, error) {
	entries := []ContentEntry{}
	if len(ids) == 0 {
		return entries, nil
	}
	items, e := s.mediaPage(profile, ids, false)
	if e != nil {
		return nil, e
	}
	loaded := map[string]ContentEntry{}
	for _, item := range items {
		row := contentItem(item)
		if item.Episode != nil {
			row.Show = &ContentEntityRef{ID: item.Episode.ShowID, Name: item.Episode.ShowTitle}
		}
		if row.Playback != nil && item.ProgressSeconds > 0 {
			p := item.ProgressSeconds
			row.Playback.StartSeconds = &p
		}
		if !item.Available {
			row.Playback = nil
		}
		loaded[item.ID] = row
	}
	for _, id := range ids {
		row, ok := loaded[id]
		if !ok {
			return nil, ErrStaleContinuation
		}
		entries = append(entries, row)
	}
	if e = s.nameEntryMakers(entries); e != nil {
		return nil, e
	}
	return entries, nil
}

// --- public composition ----------------------------------------------------

func (r HomeRequest) now() time.Time {
	if r.Now.IsZero() {
		return time.Now()
	}
	return r.Now
}

// homeRowFor composes one row's page. exactTotal counts the row when its page
// cannot answer the total; a Home document row leaves an unanswered total out.
func (s *Service) homeRowFor(r HomeRequest, spec homeRowSpec, revision ContentRevision, page HomeRowPage, enrich, exactTotal bool) (HomeRow, error) {
	row := spec.descriptor()
	row.ViewerFence = r.ViewerFence
	row.Revision = revision
	row.Limit = homeLimit(page.Limit)
	if spec.ID == "on_now" {
		if r.OnNow == nil {
			return row, ErrHomeRowUnknown
		}
		live, err := r.OnNow(page)
		if err != nil {
			return row, err
		}
		row.Live = live.Live
		row.SeeAll = &HomeSeeAll{Type: "guide", Group: page.Group, SourceID: page.SourceID}
		row.Total = live.Total
		row.Start = live.Start
		row.NextCursor = live.NextCursor
		row.HasMore = live.HasMore
		row.Limit = live.Limit
		row.SubtitleText = live.SubtitleText
		return row, nil
	}
	if spec.ID == "featured_collection" {
		c, err := s.homeCollection(r)
		if err != nil {
			return row, err
		}
		row.Collection = c
		total := 0
		if c != nil {
			total = 1
		}
		row.Total = &total
		if c != nil {
			row.SeeAll = &HomeSeeAll{Type: "collection", ResourceID: c.Collection.ID, LibraryID: c.Collection.LibraryID}
		}
		return row, nil
	}
	// Saved-view continuations evaluate the same relative window as page one.
	if strings.HasPrefix(spec.ID, homeViewPrefix) && page.Cursor != "" {
		cursor, err := s.readSignedHomeCursor(page.Cursor)
		if err != nil {
			return row, err
		}
		if cursor.EvaluationAt != "" {
			at, err := time.Parse(time.RFC3339Nano, cursor.EvaluationAt)
			if err != nil {
				return row, ErrCursor
			}
			copy := *s
			copy.browseInstant = at
			s = &copy
		}
	}
	src, e := s.homeSource(r, spec)
	if e != nil {
		return row, e
	}
	ids, ords, total, start, next, lead, e := s.homeRowIDs(spec, src, page, revision, r.ViewerFence, r.Profile, exactTotal)
	if e != nil {
		return row, e
	}
	if page.Cursor != "" && src.fingerprint != "" {
		// The continuation of a computed list is that list as of its first page:
		// it answers with that page's revision, which clients match page to page.
		if cursor, err := s.readSignedHomeCursor(page.Cursor); err == nil {
			row.Revision = ContentRevision{Catalog: cursor.Catalog, Viewer: cursor.Revision}
		}
	}
	displayIDs := ids
	if spec.ID == "new_on_server" {
		displayIDs, e = s.homeRecentPageTargets(r, ids)
		if e != nil {
			return row, e
		}
	}
	var entries []ContentEntry
	if spec.ID == "continue" {
		entries, e = s.homeContinueEntries(r.Viewer, ids)
	} else if strings.HasPrefix(spec.ID, homeViewPrefix) {
		library, err := s.library(spec.LibraryID)
		if err != nil {
			return row, err
		}
		if isReadingLibrary(library.Kind) {
			projected, err := s.homeReadingPage(r.Viewer, displayIDs, r.ReadingPersonalAuthorize)
			if err != nil {
				return row, err
			}
			entries = make([]ContentEntry, 0, len(displayIDs))
			for _, id := range displayIDs {
				entry, ok := projected[id]
				if !ok || entry.LibraryID != spec.LibraryID {
					return row, ErrReadingProjectionChanged
				}
				entries = append(entries, entry)
			}
		} else {
			entries, e = s.homeRowEntries(r.Profile, spec, displayIDs)
		}
	} else {
		entries, e = s.homeRowEntries(r.Profile, spec, displayIDs)
	}
	if e != nil {
		return row, e
	}
	if enrich {
		if e = s.homeEnrich(r.Profile, entries); e != nil {
			return row, e
		}
	}
	if spec.ID == "new_on_server" {
		if e = s.homeNewBadges(r, entries, ords); e != nil {
			return row, e
		}
		row.SubtitleText = &ServerText{Code: "next.home.videoKinds", Fallback: "Movies, shows and anime"}
	}
	if spec.ID == "because_you_watched" {
		source, _, err := s.homeBecause(r)
		if err != nil {
			return row, err
		}
		if source != nil {
			reason := HomeReason{Kind: "because_you_watched", Source: source, Text: ServerText{Code: "next.home.becauseYouWatched", Params: map[string]string{"title": source.Name}, Fallback: "Because you watched " + source.Name}}
			row.Reason = &reason
			row.Title = reason.Text.Fallback
			row.TitleText = reason.Text
		}
	}
	if spec.ID == "trending_now" {
		row.Why = &ServerText{Code: "next.home.whyTrending", Fallback: "What people on this server are watching most this week."}
	}
	if spec.ID == "recommended" {
		row.Why, row.WhyTitles, e = s.homeRecommendationWhy(r)
		if e != nil {
			return row, e
		}
	}
	if spec.ID == "community_watching" {
		row.Why = &ServerText{Code: "next.home.whyPopular", Fallback: "Watched by people here over the last month. Only profiles that share their activity count."}
		row.Period = &HomePeriod{Days: 30, Start: r.now().Add(-communityWindow).UTC().Format(time.RFC3339), End: r.now().UTC().Format(time.RFC3339), Metric: "distinct_eligible_viewers", Text: ServerText{Code: "next.home.last30Days", Fallback: "Last 30 days"}}
	}
	if spec.ID == "listen_next" || spec.ID == "continue_books" {
		if row.Resumes, e = s.homeAudioResume(r, entries); e != nil {
			return row, e
		}
		if spec.ID == "listen_next" {
			row.SubtitleText = &ServerText{Code: "next.home.audioKinds", Fallback: "Audiobooks and music"}
		} else {
			row.Platforms = []string{"web", "phone"}
		}
	}
	if spec.ID == "new_on_server" {
		for i, entry := range entries {
			row.DisplayTargets = append(row.DisplayTargets, HomeDisplayTarget{MembershipID: ids[i], EntryID: entry.ID})
		}
	}
	row.Entries = entries
	row.lead = lead
	row.Total = total
	row.Start = start
	row.NextCursor = next
	row.HasMore = next != "" || total != nil && start+len(entries) < *total
	row.AnchorID = page.AnchorID
	return row, nil
}

// homeRowEntries hydrates one row's page: engine rows may list container works
// (shows, albums, books), every other row lists items only.
func (s *Service) homeRowEntries(profile string, spec homeRowSpec, ids []string) ([]ContentEntry, error) {
	if strings.HasPrefix(spec.ID, "recent_") || spec.ID == "new_on_server" {
		entries, err := s.homeEngineEntries(profile, ids)
		if err != nil {
			return nil, err
		}
		return entries, s.homeRecentSeasonCounts(entries)
	}
	if spec.ID == "because_you_watched" || spec.ID == "listen_next" || spec.ID == "continue_books" || spec.ID == "recommended" || spec.ID == "trending_now" || spec.ID == "community_watching" || strings.HasPrefix(spec.ID, recFamily+":") || strings.HasPrefix(spec.ID, homeViewPrefix) {
		return s.homeEngineEntries(profile, ids)
	}
	return s.homeEntries(profile, ids)
}

func homeOrdered(specs []homeRowSpec, order []string) []homeRowSpec {
	rank := map[string]int{}
	for index, id := range order {
		if _, seen := rank[id]; !seen {
			rank[id] = index
		}
	}
	sort.SliceStable(specs, func(i, j int) bool {
		a, aOK := rank[specs[i].ID]
		b, bOK := rank[specs[j].ID]
		if aOK != bOK {
			return aOK
		}
		if aOK && a != b {
			return a < b
		}
		if specs[i].Priority != specs[j].Priority {
			return specs[i].Priority < specs[j].Priority
		}
		if specs[i].Rank != specs[j].Rank {
			return specs[i].Rank < specs[j].Rank
		}
		return specs[i].ID < specs[j].ID
	})
	return specs
}

// HomeRowCatalogue publishes the row identities and their policy without
// composing entries. Layout validation uses it so a viewer can never order or
// hide a row that this server does not publish.
func (s *Service) HomeRowCatalogue(r HomeRequest) ([]HomeRow, error) {
	r = r.scoped()
	if err := s.prepareViewer(r.Viewer); err != nil {
		return nil, err
	}
	specs, e := s.homeSpecs(r)
	if e != nil {
		return nil, e
	}
	out := []HomeRow{}
	for _, spec := range homeOrdered(specs, r.RowOrder) {
		out = append(out, spec.descriptor())
	}
	return out, nil
}

// HomeLayoutRow is one row a viewer can arrange (CON-23): its identity and policy, and whether
// the saved layout hides it. No entries: Customise Home lists every row, empty or not, without
// composing any of them.
type HomeLayoutRow struct {
	AvailabilityText *ServerText `json:"availabilityText,omitempty"`
	ID               string      `json:"id"`
	Title            string      `json:"title"`
	TitleText        ServerText  `json:"titleText"`
	Kind             string      `json:"kind"`
	ArtworkShape     string      `json:"artworkShape"`
	LibraryID        string      `json:"libraryId,omitempty"`
	Required         bool        `json:"required"`
	Hideable         bool        `json:"hideable"`
	Reorderable      bool        `json:"reorderable"`
	Hidden           bool        `json:"hidden"`
}

// HomeLayoutView is the saved layout with every row it can arrange, in the viewer's order.
type HomeLayoutView struct {
	Revision     int64           `json:"revision"`
	RowOrder     []string        `json:"rowOrder"`
	HiddenRowIDs []string        `json:"hiddenRowIds"`
	Rows         []HomeLayoutRow `json:"rows"`
}

// HomeLayoutRows lists every row the viewer could have on Home (CON-23), in their order, whether
// it has entries today or not; `GET /v1/home` omits empty rows, so Customise Home can't list from
// it. Rows the owner turned off are left out. It reads only the row definitions and the visible
// libraries: no row is composed.
func (s *Service) HomeLayoutRows(r HomeRequest) (HomeLayoutView, error) {
	r = r.scoped()
	if err := s.prepareViewer(r.Viewer); err != nil {
		return HomeLayoutView{}, err
	}
	r.Libraries = homeUnique(r.Libraries)
	specs, e := s.homeSpecs(r)
	if e != nil {
		return HomeLayoutView{}, e
	}
	hidden := map[string]bool{}
	for _, id := range r.HiddenRowIDs {
		hidden[id] = true
	}
	out := HomeLayoutView{Revision: r.LayoutRevision, RowOrder: append([]string{}, r.RowOrder...), HiddenRowIDs: append([]string{}, r.HiddenRowIDs...), Rows: []HomeLayoutRow{}}
	for _, spec := range homeOrdered(specs, r.RowOrder) {
		if spec.PolicyState != "available" {
			continue
		}
		var availability *ServerText
		if spec.ID == "continue_books" {
			availability = &ServerText{Code: "next.home.booksAvailability", Fallback: "Shown when a book is in progress"}
		}
		out.Rows = append(out.Rows, HomeLayoutRow{AvailabilityText: availability, ID: spec.ID, Title: spec.Title, TitleText: spec.titleText(), Kind: spec.Kind, ArtworkShape: spec.ArtworkShape, LibraryID: spec.LibraryID,
			Required: spec.Required, Hideable: spec.Hideable, Reorderable: spec.Reorderable, Hidden: spec.hiddenIn(r.RowOrder, hidden)})
	}
	return out, nil
}

// Home composes every visible row with an inline preview. Rows with no entries
// are omitted unless they are critical; hidden rows are dropped unless the row
// is required, which no layout may hide.
func (s *Service) homeRowsFresh(r HomeRequest) (HomeDocument, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out HomeDocument
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			out, readErr = s.WithContext(ctx).homeRowsFresh(r)
			return readErr
		})
		return out, err
	}
	if err := s.compactProjectionReady(18, 19, 20, 21, 25, 26); err != nil {
		return HomeDocument{}, err
	}
	r = r.scoped()
	if err := s.prepareViewer(r.Viewer); err != nil {
		return HomeDocument{}, err
	}
	r.Now = s.recommendationNow(r.Now)
	s = s.WithRecommendationRestrictions(r.Restrictions)
	s.recSources = map[string]homeSource{}
	s.recComposition = newRecCompositionSources(s.Context())
	r.Libraries = homeUnique(r.Libraries)
	out := HomeDocument{ServerID: r.ServerID, ViewerFence: r.ViewerFence, PrivacyFence: r.PrivacyFence, Rows: []HomeRow{},
		Layout: HomeLayout{Revision: r.LayoutRevision, RowOrder: append([]string{}, r.RowOrder...), HiddenRowIDs: append([]string{}, r.HiddenRowIDs...)}}
	if out.Layout.RowOrder == nil {
		out.Layout.RowOrder = []string{}
	}
	before, e := s.homeRevision(r.Libraries, r.Profile)
	if e != nil {
		return out, e
	}
	out.Revision = before
	paging, e := s.homePagingRevision(r.Libraries, r.Profile)
	if e != nil {
		return out, e
	}
	specs, e := s.homeSpecs(r)
	if e != nil {
		return out, e
	}
	hidden := map[string]bool{}
	for _, id := range r.HiddenRowIDs {
		hidden[id] = true
	}
	composed := func() bool {
		if r.Rows <= 0 {
			return false
		}
		filled := 0
		for _, row := range out.Rows {
			if len(row.Entries) > 0 {
				filled++
			}
		}
		return filled >= r.Rows
	}
	for _, spec := range homeOrdered(specs, r.RowOrder) {
		if composed() {
			break
		}
		if spec.PolicyState != "available" {
			continue
		}
		if spec.hiddenIn(r.RowOrder, hidden) {
			continue
		}
		if len(out.Rows) == operations.HomeLayoutMaxRows && !spec.Required {
			continue
		}
		if spec.ID == recFamily {
			rows, e := s.recFamilyRows(r, paging)
			if e != nil {
				return out, e
			}
			for _, row := range rows {
				if len(out.Rows) == operations.HomeLayoutMaxRows {
					break
				}
				out.Rows = append(out.Rows, row)
			}
			continue
		}
		row, e := s.homeRowFor(r, spec, paging, HomeRowPage{Limit: r.Limit}, false, false)
		if e != nil {
			return out, e
		}
		if (len(row.Entries) == 0 && row.Collection == nil && (row.Live == nil || len(row.Live.Channels) == 0) && !spec.Critical) || (spec.ID == "recommended" && homeRowBelow(row, recMinimumShelf)) || (spec.ID == "trending_now" && homeRowBelow(row, recMinimumTrend)) {
			continue
		}
		if len(out.Rows) == operations.HomeLayoutMaxRows {
			// A custom order may put required rows last. Preserve them by dropping
			// the last optional preview; all rows remain in the customization catalogue.
			for i := len(out.Rows) - 1; i >= 0; i-- {
				if !out.Rows[i].Required {
					out.Rows = append(out.Rows[:i], out.Rows[i+1:]...)
					break
				}
			}
		}
		out.Rows = append(out.Rows, row)
	}
	if e = s.homeEnrichRows(r.Profile, out.Rows); e != nil {
		return out, e
	}
	if r.ReadingPersonalAuthorize == nil {
		clearReadingHomePersonal(out.Rows)
	}
	if e = s.homeTitleContextRows(r.Viewer, out.Rows); e != nil {
		return out, e
	}
	after, e := s.homeRevision(r.Libraries, r.Profile)
	if e != nil {
		return out, e
	}
	if before != after {
		return out, ErrStaleContinuation
	}
	out.Spotlight, e = s.homeSpotlight(r)
	if e != nil {
		return out, e
	}
	out.GeneratedAt = r.now().UTC().Format("2006-01-02T15:04:05.000Z")
	return out, nil
}

// HomeSingleRow pages one row. A supplied revision that no longer matches the
// live projection is a stale continuation, not a silently different page.
func (s *Service) homeSingleRowFresh(r HomeRequest, id string, page HomeRowPage) (HomeRow, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out HomeRow
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			out, readErr = s.WithContext(ctx).homeSingleRowFresh(r, id, page)
			return readErr
		})
		return out, err
	}
	if err := s.compactProjectionReady(18, 19, 20, 21, 25, 26); err != nil {
		return HomeRow{}, err
	}
	r = r.scoped()
	if err := s.prepareViewer(r.Viewer); err != nil {
		return HomeRow{}, err
	}

	r.Now = s.recommendationNow(r.Now)
	s = s.WithRecommendationRestrictions(r.Restrictions)
	s.recSources = map[string]homeSource{}
	s.recComposition = newRecCompositionSources(s.Context())
	r.Libraries = homeUnique(r.Libraries)
	specs, e := s.homeSpecs(r)
	if e != nil {
		return HomeRow{}, e
	}
	if id == recFamily && !hidesFamily(r.HiddenRowIDs) {
		r.Limit = homeLimit(page.Limit)
		if page.Cursor != "" || page.Start != 0 || page.AnchorID != "" {
			return HomeRow{}, ErrCursor
		}
		revision, err := s.homePagingRevision(r.Libraries, r.Profile)
		if err != nil {
			return HomeRow{}, err
		}
		children, err := s.recFamilyRows(r, revision)
		if err != nil {
			return HomeRow{}, err
		}
		if err = s.homeEnrichRows(r.Profile, children); err != nil {
			return HomeRow{}, err
		}
		if err = s.homeTitleContextRows(r.Viewer, children); err != nil {
			return HomeRow{}, err
		}
		for _, spec := range specs {
			if spec.ID == recFamily {
				row := spec.descriptor()
				row.Children = children
				// The family's own entries are its children's; it has none itself.
				none := 0
				row.Total = &none
				row.ViewerFence = r.ViewerFence
				row.Revision = revision
				row.Limit = r.Limit
				return row, nil
			}
		}
		return HomeRow{}, ErrHomeRowUnknown
	}
	var found *homeRowSpec
	for index := range specs {
		if specs[index].ID == id && id != recFamily {
			found = &specs[index]
		}
	}
	if strings.HasPrefix(id, recFamily+":") && !hidesFamily(r.HiddenRowIDs) {
		row, e := s.recPersonalRow(r, id)
		if e != nil {
			return HomeRow{}, e
		}
		s.recSources[id+":"+idsJSON(r.Libraries)] = candidateSource(row.items)
		found = &row.spec
	}
	if found == nil || found.PolicyState != "available" {
		return HomeRow{}, ErrHomeRowUnknown
	}
	before, e := s.homePagingRevision(r.Libraries, r.Profile)
	if e != nil {
		return HomeRow{}, e
	}

	row, e := s.homeRowFor(r, *found, before, page, true, true)
	if e != nil {
		return row, e
	}
	if r.ReadingPersonalAuthorize == nil {
		clearReadingHomePersonal([]HomeRow{row})
	}
	if e = s.homeTitleContextRows(r.Viewer, []HomeRow{row}); e != nil {
		return row, e
	}

	after, e := s.homePagingRevision(r.Libraries, r.Profile)
	if e != nil {
		return row, e
	}
	if before != after {
		return row, ErrStaleContinuation
	}
	return row, nil
}

// recFamilyRows composes the personal rows Home shows today, each a first page
// of its own ranking (already deduplicated across the family).
func (s *Service) recFamilyRows(r HomeRequest, revision ContentRevision) ([]HomeRow, error) {
	generated, err := s.recPersonalRows(r)
	if err != nil {
		return nil, err
	}
	out := make([]HomeRow, 0, len(generated))
	for _, g := range generated {
		s.recSources[g.spec.ID+":"+idsJSON(r.Libraries)] = candidateSource(g.items)
		row, err := s.homeRowFor(r, g.spec, revision, HomeRowPage{Limit: r.Limit}, false, false)
		if err != nil {
			return nil, err
		}
		if len(row.Entries) > 0 {
			out = append(out, row)
		}
	}
	return out, nil
}

func hidesFamily(hidden []string) bool {
	for _, id := range hidden {
		if id == recFamily {
			return true
		}
	}
	return false
}

// ValidateHomeLayout refuses orders and hidden sets that name rows this server
// does not publish, and refuses to hide a required row by name.
func (s *Service) ValidateHomeLayout(r HomeRequest, order, hidden []string) error {
	r.RowOrder = order
	specs, e := s.homeSpecs(r)
	if e != nil {
		return e
	}
	known := map[string]homeRowSpec{}
	for _, spec := range specs {
		known[spec.ID] = spec
	}
	if len(order) > operations.HomeLayoutMaxRows || len(hidden) > operations.HomeLayoutMaxRows {
		return fmt.Errorf("%w: too many rows", ErrHomeLayoutRow)
	}
	seen := map[string]bool{}
	for _, id := range order {
		spec, ok := known[id]
		if !ok && strings.HasPrefix(id, homeViewPrefix) && utf8.RuneCountInString(id) <= operations.HomeLayoutRowIDMaxLength {
			// A saved view the viewer can't read (deleted, unshared) is kept
			// and ignored: refusing it would block every later layout save.
			seen[id] = true
			continue
		}
		if !ok || utf8.RuneCountInString(id) > operations.HomeLayoutRowIDMaxLength {
			return fmt.Errorf("%w: %s", ErrHomeLayoutRow, id)
		}
		if seen[id] {
			return fmt.Errorf("%w: %s is listed twice", ErrHomeLayoutRow, id)
		}
		seen[id] = true
		if !spec.Reorderable {
			return fmt.Errorf("%w: %s cannot be reordered", ErrHomeLayoutRow, id)
		}
	}
	seen = map[string]bool{}
	for _, id := range hidden {
		spec, ok := known[id]
		if !ok || utf8.RuneCountInString(id) > operations.HomeLayoutRowIDMaxLength {
			return fmt.Errorf("%w: %s", ErrHomeLayoutRow, id)
		}
		if seen[id] {
			return fmt.Errorf("%w: %s is listed twice", ErrHomeLayoutRow, id)
		}
		seen[id] = true
		if spec.Required || !spec.Hideable {
			return fmt.Errorf("%w: %s is required and cannot be hidden", ErrHomeLayoutRow, id)
		}
	}
	return nil
}

// homeEnrichRows decorates the final Home previews together. The document's
// row and page limits bound this batch; hidden, empty and displaced rows cost
// no decoration queries. A row fetched on its own still decorates its own page.
func (s *Service) homeEnrichRows(profile string, rows []HomeRow) error {
	entries := []ContentEntry{}
	for _, row := range rows {
		entries = append(entries, row.Entries...)
	}
	if err := s.homeEnrich(profile, entries); err != nil {
		return err
	}
	start := 0
	for _, row := range rows {
		copy(row.Entries, entries[start:start+len(row.Entries)])
		start += len(row.Entries)
	}
	return nil
}

// homeEnrich adds what a Home card or the hero shows beyond the shared entry (M5): the year, the
// content rating, the first two genres and the viewer's watchlist state. Four bounded reads for
// the whole preview batch, never one per entry. A container work (a show, an
// album) takes its year from the browse projection; attributes and watchlist state are items'.
func (s *Service) homeEnrich(profile string, entries []ContentEntry) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]string, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if !seen[entry.ID] {
			ids = append(ids, entry.ID)
			seen[entry.ID] = true
		}
	}
	list := idsJSON(ids)
	years := map[string]int{}
	rows, e := s.read().Query(`SELECT pid(entity.public_id),entity.year FROM json_each(?) requested CROSS JOIN catalog_entities entity ON entity.public_id=pid_blob(requested.value) WHERE entity.year>0`, list)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id string
		var year int
		if e = rows.Scan(&id, &year); e != nil {
			rows.Close()
			return e
		}
		years[id] = year
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	ratings, genres := map[string]string{}, map[string][]string{}
	rows, e = s.read().Query(`SELECT pid(item.public_id),field.field,edge.source_value
	 FROM json_each(?) requested CROSS JOIN catalog_entities item ON item.public_id=pid_blob(requested.value)
	 CROSS JOIN catalog_item_attribute_edges edge ON edge.item_id=item.id
	 CROSS JOIN catalog_attribute_terms term ON term.id=edge.term_id
	 CROSS JOIN catalog_attribute_fields field ON field.id=term.field_id
	 WHERE field.field='contentRating'
	 ORDER BY item.id,edge.source_rowid`, list)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, field, value string
		if e = rows.Scan(&id, &field, &value); e != nil {
			rows.Close()
			return e
		}
		if field == "contentRating" && ratings[id] == "" {
			ratings[id] = value
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// Every read starts from the requested ids (CROSS JOIN fixes the order):
	// left to the planner, the rating and genre reads started from the
	// field and the vocabulary and walked every title of the server.
	// Genres are the entity's genre terms (catalog_terms vocabulary 1), each
	// once however many providers name it, in the providers' order: the first
	// a provider names is the title's primary genre.
	rows, e = s.read().Query(`SELECT pid(item.public_id),COALESCE(min(src.label_override),term.label)
	 FROM json_each(?) requested CROSS JOIN catalog_entities item ON item.public_id=pid_blob(requested.value)
	 CROSS JOIN catalog_term_sources src ON src.entity_id=item.id
	 CROSS JOIN catalog_terms term ON term.id=src.term_id AND term.vocab=1
	 GROUP BY item.id,term.id ORDER BY item.id,min(src.ordinal),term.label`, list)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, label string
		if e = rows.Scan(&id, &label); e != nil {
			rows.Close()
			return e
		}
		if len(genres[id]) < 2 {
			genres[id] = append(genres[id], label)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	watchlisted := map[string]bool{}
	rows, e = s.read().Query(`SELECT pid(entity.public_id),mine.watchlisted FROM json_each(?) requested CROSS JOIN catalog_entities entity ON entity.public_id=pid_blob(requested.value)
	 CROSS JOIN personal_items mine ON mine.profile_id=? AND mine.item_id=entity.id`, list, profile)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id string
		var on bool
		if e = rows.Scan(&id, &on); e != nil {
			rows.Close()
			return e
		}
		watchlisted[id] = on
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for index := range entries {
		entry := &entries[index]
		if year, ok := years[entry.ID]; ok {
			entry.Year = &year
		}
		entry.ContentRating = ratings[entry.ID]
		entry.Genres = genres[entry.ID]
		if isItemEntity(entry.Kind) || entry.Playback != nil {
			on := watchlisted[entry.ID]
			entry.Watchlisted = &on
		}
	}
	return nil
}

// A catalogue Viewer alone does not authorize Reading personal facts.
func clearReadingHomePersonal(rows []HomeRow) {
	for i := range rows {
		for j := range rows[i].Entries {
			entry := &rows[i].Entries[j]
			if entry.Kind == "ebook" || entry.Kind == "comic" {
				entry.Watchlisted = nil
				entry.ReadingMark = nil
			}
		}
		clearReadingHomePersonal(rows[i].Children)
	}
}
