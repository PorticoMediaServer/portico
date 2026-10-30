package metadataprovider

import (
	"context"
	"net/url"
	"strings"
)

// ParseScreenHint extracts an identifier from known provider syntax. It never
// returns a remote URL to fetch and never accepts a host supplied as a provider.
func ParseScreenHint(input, kind string) (ScreenID, bool) {
	input = strings.TrimSpace(input)
	if len(input) > 2048 {
		return ScreenID{}, false
	}
	if validIMDbID(input) {
		return ScreenID{"imdb", "title", input}, true
	}
	if u, e := url.Parse(input); e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Port() == "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		switch host {
		case "imdb.com":
			if len(parts) == 2 && parts[0] == "title" && validIMDbID(parts[1]) {
				return ScreenID{"imdb", "title", parts[1]}, true
			}
		case "themoviedb.org":
			if len(parts) == 2 {
				typ := map[string]string{"movie": "movie", "tv": "show"}[parts[0]]
				id := ScreenID{"tmdb", typ, strings.Split(parts[1], "-")[0]}
				return id, ValidScreenID(id)
			}
		case "anilist.co":
			if len(parts) == 2 && parts[0] == "anime" {
				id := ScreenID{"anilist", "anime", parts[1]}
				return id, ValidScreenID(id)
			}
		case "thetvdb.com":
			if len(parts) == 3 && parts[0] == "dereferrer" {
				typ := map[string]string{"movie": "movie", "series": "show"}[parts[1]]
				id := ScreenID{"tvdb", typ, parts[2]}
				return id, ValidScreenID(id)
			}
		}
	}
	provider, value, found := strings.Cut(input, ":")
	provider = strings.ToLower(provider)
	if !found {
		return ScreenID{}, false
	}
	if provider == "imdb" {
		id := ScreenID{"imdb", "title", value}
		return id, ValidScreenID(id)
	}
	if provider == "anilist" {
		kind = "anime"
	}
	id := ScreenID{provider, kind, value}
	return id, ValidScreenID(id)
}
func (s *TMDB) ResolveScreenID(ctx context.Context, hint ScreenID, kind, language, region string) (ScreenID, error) {
	if hint.Provider != "imdb" || !ValidScreenID(hint) || (kind != "movie" && kind != "show") {
		return ScreenID{}, &Error{Provider: "tmdb", Code: "invalid_identifier"}
	}
	var found struct {
		Movies []struct {
			ID int64 `json:"id"`
		} `json:"movie_results"`
		Shows []struct {
			ID int64 `json:"id"`
		} `json:"tv_results"`
	}
	q := url.Values{"external_source": {"imdb_id"}, "language": {language}}
	if e := s.get(ctx, "/find/"+hint.ID+"?"+q.Encode(), &found); e != nil {
		return ScreenID{}, e
	}
	var id int64
	if kind == "movie" && len(found.Movies) == 1 {
		id = found.Movies[0].ID
	}
	if kind == "show" && len(found.Shows) == 1 {
		id = found.Shows[0].ID
	}
	out := ScreenID{"tmdb", kind, numberID(id)}
	if !ValidScreenID(out) {
		return out, &Error{Provider: "tmdb", Code: "identifier_not_found"}
	}
	return out, nil
}
