package librarychannels

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

const ProtocolVersion = "1.0"
const CandidateBatch = 128
const EntryBatch = 64
const MaxChannels = 64

var ErrTemplateEmpty = errors.New("This template needs at least three eligible items with resolved durations.")
var ErrInvalid = errors.New("The Library Channel configuration is invalid.")
var ErrConflict = errors.New("The Library Channel changed. Refresh before saving.")
var ErrUnavailable = errors.New("Library Channels are temporarily unavailable.")
var ErrDenied = livechannels.ErrDenied
var ErrInUse = errors.New("This channel still has accepted playback. Disable it and wait for those references to end before deleting.")
var ErrOverlay = errors.New("Choose a logo and a supported corner, size, inset and treatment for the on-screen logo.")
var stableID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validID(v string) bool { return stableID.MatchString(v) }
func text(v string, max int) bool {
	return len(v) > 0 && len(v) <= max && utf8.ValidString(v) && !strings.ContainsFunc(v, unicode.IsControl)
}
func digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func oneOf(v string, values ...string) bool {
	for _, s := range values {
		if v == s {
			return true
		}
	}
	return false
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }

// Queries are structured predicates over current normalized catalog fields.
// An empty library list is invalid rather than a wildcard exposing new libraries.
//
// Selection (Spec — Custom Channels Builder §1): `Filter` is the library browse
// expression (every browse field, all/any/not groups), evaluated against the
// movie for movies and against the show for episodes. `Text` matches words in
// titles and summaries. `IncludeItemIDs` pins titles in (a show pins every
// episode) and `ExcludeItemIDs` pins them out. Genres/YearFrom/YearThrough are
// the first-version fields; Normalize folds them into Filter.
type Query struct {
	LibraryIDs     []string        `json:"libraryIds"`
	Kinds          []string        `json:"kinds"`
	ShowIDs        []string        `json:"showIds"`
	Genres         []string        `json:"genres,omitempty"`
	YearFrom       int             `json:"yearFrom,omitempty"`
	YearThrough    int             `json:"yearThrough,omitempty"`
	RecentDays     int             `json:"recentDays"`
	Order          string          `json:"order"`
	Limit          int             `json:"limit"` // Explicit semantic limit; zero means all matches, paged.
	Filter         json.RawMessage `json:"filter,omitempty"`
	Text           string          `json:"text,omitempty"`
	IncludeItemIDs []string        `json:"includeItemIds,omitempty"`
	ExcludeItemIDs []string        `json:"excludeItemIds,omitempty"`
}

const maxPinned = 500

// selects reports whether the rule narrows by criteria (rather than only pins).
func (q Query) selects() bool {
	return len(q.Filter) > 0 || strings.TrimSpace(q.Text) != "" || len(q.ShowIDs) > 0 || len(q.Genres) > 0 || q.YearFrom > 0 || q.YearThrough > 0 || q.RecentDays > 0
}

// filterNode parses and validates the browse expression (nil means none).
func (q Query) filterNode(path string) (*catalog.BrowseNode, error) {
	if len(q.Filter) == 0 {
		return nil, nil
	}
	return catalog.ParseBrowseQuery(q.Filter, path)
}

// PersonalHint is the message when a rule names a personal field. Library
// Channels are always shared (Justin, 23 Sep), so watched, favorites, watchlist,
// personal ratings and last played never select a channel.
const PersonalHint = "Library Channels are shared, so watched, favorites, watchlist and personal ratings can't be used. Community and critic ratings can."

// Normalize migrates first-version fields forward: genres and year bounds become
// browse predicates in Filter, so one mechanism selects. It is idempotent.
func Normalize(c Config) (Config, error) {
	if c.ViewerAccess == "" {
		// Library Channels are always shared; an older client may omit the field.
		c.ViewerAccess = "server-members"
	}
	for i := range c.Rules {
		q := &c.Rules[i].Query
		extra := []map[string]any{}
		if len(q.Genres) > 0 {
			values := make([]any, len(q.Genres))
			for j, g := range q.Genres {
				values[j] = g
			}
			extra = append(extra, map[string]any{"field": "genre", "operator": "contains-any", "value": values})
		}
		if q.YearFrom > 0 {
			extra = append(extra, map[string]any{"field": "year", "operator": "at-least", "value": q.YearFrom})
		}
		if q.YearThrough > 0 {
			extra = append(extra, map[string]any{"field": "year", "operator": "at-most", "value": q.YearThrough})
		}
		if len(extra) == 0 {
			continue
		}
		all := []any{}
		if len(q.Filter) > 0 {
			var existing any
			if json.Unmarshal(q.Filter, &existing) != nil {
				return c, ErrInvalid
			}
			all = append(all, existing)
		}
		for _, v := range extra {
			all = append(all, v)
		}
		var node any = map[string]any{"all": all}
		if len(all) == 1 {
			node = all[0]
		}
		raw, e := json.Marshal(node)
		if e != nil {
			return c, ErrInvalid
		}
		q.Filter, q.Genres, q.YearFrom, q.YearThrough = raw, nil, 0, 0
	}
	return c, nil
}

// ValidationError names the builder field that is wrong.
type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string { return e.Path + ": " + e.Message }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

type ItemWeight struct {
	ItemID string  `json:"itemId"`
	Weight float64 `json:"weight"`
}
type Rule struct {
	ID                  string       `json:"id"`
	Name                string       `json:"name"`
	Query               Query        `json:"query"`
	Mode                string       `json:"mode"`
	EpisodeMode         string       `json:"episodeMode"`
	Exhaustion          string       `json:"exhaustion"`
	DeduplicationWindow int          `json:"deduplicationWindow"`
	MaxConsecutive      int          `json:"maxConsecutive"`
	Weights             []ItemWeight `json:"weights"`
}
type Block struct {
	ID             string `json:"id"`
	Weekdays       []int  `json:"weekdays"` // Sunday=0, ISO wall date resolved on server.
	StartMinute    int    `json:"startMinute"`
	EndMinute      int    `json:"endMinute"`
	Priority       int    `json:"priority"`
	RuleID         string `json:"ruleId"`
	FallbackRuleID string `json:"fallbackRuleId"`
	Anchor         string `json:"anchor"`  // channel-cursor | block-start
	Overrun        string `json:"overrun"` // finish | cut | fit
}
type Quality struct {
	Mode          string `json:"mode"`
	MaxBitrate    int64  `json:"maxBitrate"`
	MaxHeight     int    `json:"maxHeight"`
	AllowLossy    bool   `json:"allowLossy"`
	AllowHDRToSDR bool   `json:"allowHdrToSdr"`
}
type Overlay struct {
	Enabled      bool   `json:"enabled"`
	Corner       string `json:"corner"`
	SizePercent  int    `json:"sizePercent"`
	InsetPercent int    `json:"insetPercent"`
	Treatment    string `json:"treatment"`
}
type Config struct {
	Version       string  `json:"protocolVersion"`
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Description   string  `json:"description"`
	Enabled       bool    `json:"enabled"`
	Position      int     `json:"position"`
	Timezone      string  `json:"timezone"`
	Seed          string  `json:"seed"`
	DefaultRuleID string  `json:"defaultRuleId"`
	ViewerAccess  string  `json:"viewerAccess"`
	Quality       Quality `json:"quality"`
	LogoItemID    string  `json:"logoItemId"`
	Overlay       Overlay `json:"overlay"`
	Rules         []Rule  `json:"rules"`
	Blocks        []Block `json:"blocks"`
	TemplateID    string  `json:"templateId"`
}

// MarshalJSON keeps collection fields arrays across drafts, saves, reads and
// receipt replay, including configurations saved with omitted optional lists.
func (c Config) MarshalJSON() ([]byte, error) {
	type wireConfig Config
	out := wireConfig(c)
	out.Rules = append([]Rule{}, c.Rules...)
	out.Blocks = append([]Block{}, c.Blocks...)
	for i := range out.Rules {
		r := &out.Rules[i]
		r.Weights = append([]ItemWeight{}, r.Weights...)
		r.Query.LibraryIDs = append([]string{}, r.Query.LibraryIDs...)
		r.Query.Kinds = append([]string{}, r.Query.Kinds...)
		r.Query.ShowIDs = append([]string{}, r.Query.ShowIDs...)
	}
	for i := range out.Blocks {
		out.Blocks[i].Weekdays = append([]int{}, out.Blocks[i].Weekdays...)
	}
	return json.Marshal(out)
}

type SaveInput struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Config           Config `json:"config"`
}
type Mutation struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type Channel struct {
	Config                  Config `json:"config"`
	Revision                int64  `json:"revision"`
	State                   string `json:"state"`
	HealthCode              string `json:"healthCode"`
	Generation              string `json:"generation"`
	GeneratedThrough        string `json:"generatedThrough"`
	CandidateCount          int    `json:"candidateCount"`
	UnresolvedDurationCount int    `json:"unresolvedDurationCount"`
	ReplacementBoundary     string `json:"replacementBoundary"`
}
type Scope struct {
	Fence         string
	Owner         bool
	Principal     identity.Principal
	AllowsLibrary func(string) bool
	// AllowsItem applies the viewer's content restrictions to one item; nil means
	// unrestricted. Guide entries it refuses become a slate for this viewer.
	AllowsItem func(context.Context, *sql.Tx, string) bool
}
type Authority func(context.Context, *sql.Tx, bool) (Scope, error)

func (s Scope) permits(c Config) bool {
	if (!s.Owner && c.ViewerAccess != "server-members") || s.AllowsLibrary == nil {
		return false
	}
	for _, id := range libraries(c) {
		if !s.AllowsLibrary(id) {
			return false
		}
	}
	return true
}
func libraries(c Config) []string {
	set := map[string]bool{}
	for _, r := range c.Rules {
		for _, id := range r.Query.LibraryIDs {
			set[id] = true
		}
	}
	v := []string{}
	for id := range set {
		v = append(v, id)
	}
	sort.Strings(v)
	return v
}
func Validate(c Config) error {
	// Library Channels are one shared lineup (Justin): everyone who can see a
	// channel's libraries sees the same channel; restricted titles become slates
	// per profile. There are no owner-only channels.
	if c.ViewerAccess != "server-members" {
		return &ValidationError{Path: "viewerAccess", Message: "Library Channels are shared with everyone who can see their libraries, so they can't be owner-only."}
	}
	// The mistakes an owner makes in the builder get a field and a sentence.
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 120 {
		return &ValidationError{Path: "name", Message: "Give the channel a name (up to 120 characters)."}
	}
	for i, r := range c.Rules {
		path := fmt.Sprintf("rules[%d].query", i)
		if len(r.Query.LibraryIDs) == 0 {
			return &ValidationError{Path: path + ".libraryIds", Message: "Choose at least one library."}
		}
		if len(r.Query.Kinds) == 0 && len(r.Query.IncludeItemIDs) == 0 {
			return &ValidationError{Path: path + ".kinds", Message: "Choose movies, shows or both."}
		}
	}
	if c.Version != ProtocolVersion || !validID(c.ID) || !text(c.Name, 120) || (c.Description != "" && !text(c.Description, 2048)) || c.Position < 0 || c.Position > 100000 || !text(c.Seed, 128) || !validID(c.DefaultRuleID) || len(c.Rules) < 1 || len(c.Rules) > 16 || len(c.Blocks) > 64 || !oneOf(c.Quality.Mode, "automatic", "original", "limited") || c.Quality.MaxBitrate < 0 || c.Quality.MaxBitrate > 200000000 || c.Quality.MaxHeight < 0 || c.Quality.MaxHeight > 4320 {
		return ErrInvalid
	}
	if c.Quality.Mode == "limited" && c.Quality.MaxBitrate == 0 && c.Quality.MaxHeight == 0 {
		return ErrInvalid
	}
	if c.Timezone == "Local" || len(c.Timezone) > 120 {
		return ErrInvalid
	}
	if _, e := time.LoadLocation(c.Timezone); e != nil {
		return ErrInvalid
	}
	if c.LogoItemID != "" && !validID(c.LogoItemID) {
		return ErrInvalid
	}
	if c.TemplateID != "" && !text(c.TemplateID, 128) {
		return ErrInvalid
	}
	// Disabled overlays still cross the wire. Bound dormant values as well as
	// active settings so another client can always decode a saved configuration.
	if (c.Overlay.Corner != "" && !text(c.Overlay.Corner, 40)) || (c.Overlay.Treatment != "" && !text(c.Overlay.Treatment, 40)) || c.Overlay.SizePercent < 0 || c.Overlay.SizePercent > 100 || c.Overlay.InsetPercent < 0 || c.Overlay.InsetPercent > 100 {
		return ErrOverlay
	}
	if c.Overlay.Enabled && (c.LogoItemID == "" || !oneOf(c.Overlay.Corner, "top-left", "top-right", "bottom-left", "bottom-right") || c.Overlay.SizePercent < 3 || c.Overlay.SizePercent > 20 || c.Overlay.InsetPercent < 0 || c.Overlay.InsetPercent > 15 || !oneOf(c.Overlay.Treatment, "original", "monochrome", "translucent")) {
		return ErrOverlay
	}
	ids := map[string]bool{}
	for _, r := range c.Rules {
		if !validID(r.ID) || ids[r.ID] || !text(r.Name, 120) || !oneOf(r.Mode, "sequential", "shuffle-bag", "weighted-random", "sequential-then-shuffle") || !oneOf(r.EpisodeMode, "none", "in-order", "marathon", "randomized", "rotate") || !oneOf(r.Exhaustion, "loop", "slate") || r.DeduplicationWindow < 0 || r.DeduplicationWindow > 1000 || r.MaxConsecutive < 1 || r.MaxConsecutive > 1000 || len(r.Weights) > 1000 {
			return ErrInvalid
		}
		ids[r.ID] = true
		q := r.Query
		if len(q.LibraryIDs) < 1 || len(q.LibraryIDs) > 64 || (len(q.Kinds) < 1 && len(q.IncludeItemIDs) == 0) || len(q.Kinds) > 2 || len(q.ShowIDs) > 128 || len(q.Genres) > 16 || q.YearFrom < 0 || q.YearFrom > 9999 || q.YearThrough < 0 || q.YearThrough > 9999 || (q.YearThrough > 0 && q.YearFrom > q.YearThrough) || q.RecentDays < 0 || q.RecentDays > 36500 || q.Limit < 0 || q.Limit > 1000000 || !oneOf(q.Order, "title", "recent", "oldest", "year", "episode", "release", "rating", "least-played") {
			return ErrInvalid
		}
		path := fmt.Sprintf("rules[%d].query", len(ids)-1)
		node, e := q.filterNode(path + ".filter")
		if e == nil && catalog.BrowseNodePersonal(node) {
			return &ValidationError{Path: path + ".filter", Message: PersonalHint}
		}
		if e != nil {
			var issue *catalog.BrowseValidationError
			if errors.As(e, &issue) {
				return &ValidationError{Path: issue.Path, Message: issue.Message}
			}
			return &ValidationError{Path: path + ".filter", Message: "the filter is not valid"}
		}
		if len(q.Text) > 200 || strings.ContainsFunc(q.Text, unicode.IsControl) {
			return &ValidationError{Path: path + ".text", Message: "use at most 200 characters"}
		}
		if len(q.IncludeItemIDs) > maxPinned || len(q.ExcludeItemIDs) > maxPinned {
			return &ValidationError{Path: path + ".includeItemIds", Message: fmt.Sprintf("pin at most %d titles", maxPinned)}
		}
		for _, list := range [][]string{q.IncludeItemIDs, q.ExcludeItemIDs} {
			seen := map[string]bool{}
			for _, id := range list {
				if !validID(id) || seen[id] {
					return &ValidationError{Path: path + ".includeItemIds", Message: "a pinned title is not valid"}
				}
				seen[id] = true
			}
		}
		if !q.selects() && len(q.IncludeItemIDs) == 0 && len(q.Kinds) == 0 {
			return ErrInvalid
		}
		for _, list := range [][]string{q.LibraryIDs, q.ShowIDs} {
			seen := map[string]bool{}
			for _, id := range list {
				if !validID(id) || seen[id] {
					return ErrInvalid
				}
				seen[id] = true
			}
		}
		seen := map[string]bool{}
		for _, k := range q.Kinds {
			if !oneOf(k, "movie", "episode") || seen[k] {
				return ErrInvalid
			}
			seen[k] = true
		}
		for _, g := range q.Genres {
			if !text(g, 120) {
				return ErrInvalid
			}
		}
		seen = map[string]bool{}
		for _, w := range r.Weights {
			if !validID(w.ItemID) || seen[w.ItemID] || math.IsNaN(w.Weight) || math.IsInf(w.Weight, 0) || w.Weight <= 0 || w.Weight > 1000000 {
				return ErrInvalid
			}
			seen[w.ItemID] = true
		}
	}
	if !ids[c.DefaultRuleID] {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, b := range c.Blocks {
		if !validID(b.ID) || seen[b.ID] || !ids[b.RuleID] || (b.FallbackRuleID != "" && !ids[b.FallbackRuleID]) || len(b.Weekdays) < 1 || len(b.Weekdays) > 7 || b.StartMinute < 0 || b.StartMinute >= 1440 || b.EndMinute < 0 || b.EndMinute >= 1440 || b.Priority < -100000 || b.Priority > 100000 || !oneOf(b.Anchor, "channel-cursor", "block-start") || !oneOf(b.Overrun, "finish", "cut", "fit") {
			return ErrInvalid
		}
		seen[b.ID] = true
		days := map[int]bool{}
		for _, d := range b.Weekdays {
			if d < 0 || d > 6 || days[d] {
				return ErrInvalid
			}
			days[d] = true
		}
	}
	return nil
}

// unavailable is ErrUnavailable with its cause kept: errors.Is still matches
// (the API answer is unchanged) and the server log names the real failure.
func unavailable(cause error) error {
	if cause == nil || errors.Is(cause, ErrUnavailable) {
		if cause == nil {
			return ErrUnavailable
		}
		return cause
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, cause)
}
