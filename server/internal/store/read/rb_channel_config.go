package read

import (
	"encoding/json"
	"slices"
)

// This is an authority/query projection of an ACTUAL accepted stored row, not
// another configuration parser or normalizer. MG's outside-writer adapter uses
// the existing library.Config -> Normalize -> Validate -> Marshal path and
// compares those canonical bytes with ConfigHash. Native core never imports
// that legacy package (and its init callbacks) into writer/plan test graphs.
// No caller-supplied config or pre-normalized transport slot grants authority.
type rbChannelAcceptedConfig struct {
	Version string          `json:"protocolVersion"`
	ID      string          `json:"id"`
	Enabled bool            `json:"enabled"`
	Rules   []rbChannelRule `json:"rules"`
}
type rbChannelRule struct {
	ID    string         `json:"id"`
	Query rbChannelQuery `json:"query"`
}
type rbChannelQuery struct {
	LibraryIDs     []string        `json:"libraryIds"`
	Kinds          []string        `json:"kinds"`
	ShowIDs        []string        `json:"showIds"`
	Genres         []string        `json:"genres,omitempty"`
	YearFrom       int             `json:"yearFrom,omitempty"`
	YearThrough    int             `json:"yearThrough,omitempty"`
	RecentDays     int             `json:"recentDays"`
	Order          string          `json:"order"`
	Limit          int             `json:"limit"`
	Filter         json.RawMessage `json:"filter,omitempty"`
	Text           string          `json:"text,omitempty"`
	IncludeItemIDs []string        `json:"includeItemIds,omitempty"`
	ExcludeItemIDs []string        `json:"excludeItemIds,omitempty"`
}

func rbChannelStableID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func rbChannelConfig(raw, channel string) ([]byte, rbChannelAcceptedConfig, error) {
	var cfg rbChannelAcceptedConfig
	if !rbJSONDepth([]byte(raw), 32) || json.Unmarshal([]byte(raw), &cfg) != nil {
		return nil, cfg, ErrInput
	}
	if cfg.Version != "1.0" || cfg.ID != channel || !rbChannelStableID(cfg.ID) || len(cfg.Rules) < 1 || len(cfg.Rules) > 16 {
		return nil, cfg, ErrInput
	}
	ids := make(map[string]bool, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		if !rbChannelStableID(rule.ID) || ids[rule.ID] {
			return nil, cfg, ErrInput
		}
		ids[rule.ID] = true
		q := rule.Query
		if len(q.LibraryIDs) < 1 || len(q.LibraryIDs) > 64 || len(q.Kinds) > 2 || len(q.Kinds) == 0 && len(q.IncludeItemIDs) == 0 || len(q.ShowIDs) > 128 || len(q.IncludeItemIDs) > 500 || len(q.ExcludeItemIDs) > 500 || q.RecentDays < 0 || q.RecentDays > 36500 || q.Limit < 0 || q.Limit > 1000000 || len(q.Filter) > 64<<10 || len(q.Text) > 200 {
			return nil, cfg, ErrInput
		}
		// These fields MUST already have been normalized by the one accepted
		// configuration path. Native core refuses instead of inventing a migration.
		if len(q.Genres) != 0 || q.YearFrom != 0 || q.YearThrough != 0 {
			return nil, cfg, ErrUnqualified
		}
		for _, list := range [][]string{q.LibraryIDs, q.ShowIDs, q.IncludeItemIDs, q.ExcludeItemIDs} {
			seen := make(map[string]bool, len(list))
			for _, id := range list {
				if !rbChannelStableID(id) || seen[id] {
					return nil, cfg, ErrInput
				}
				seen[id] = true
			}
		}
		seenKinds := make(map[string]bool, 2)
		for _, kind := range q.Kinds {
			if !slices.Contains([]string{"movie", "episode"}, kind) || seenKinds[kind] {
				return nil, cfg, ErrInput
			}
			seenKinds[kind] = true
		}
		if !slices.Contains([]string{"title", "recent", "oldest", "year", "episode", "release", "rating", "least-played"}, q.Order) {
			return nil, cfg, ErrInput
		}
	}
	// Actual stored bytes ARE the authority fence. Canonicalization/validation
	// stays outside the native core; this function never rewrites configuration.
	return []byte(raw), cfg, nil
}

// Bound JSON decoder work before allocating nested generic filter values.
func rbJSONDepth(raw []byte, maximum int) bool {
	depth := 0
	quoted, escaped := false, false
	for _, c := range raw {
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > maximum {
				return false
			}
		case '}', ']':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return !quoted && !escaped && depth == 0
}
