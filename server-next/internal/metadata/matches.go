package metadata

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// ErrUnknownRef is the answer to an id or address that is not one of a provider's titles, or
// that names a provider the library's own cannot look titles up by.
var ErrUnknownRef = errors.New("not an id or address this library's provider can look up")

// ErrWrongKind is the answer to an address of a film given for a show, or the other way round.
var ErrWrongKind = errors.New("that address is of another kind of title")

var (
	imdbID   = regexp.MustCompile(`^tt[0-9]{5,10}$`)
	numberID = regexp.MustCompile(`^[0-9]{1,12}$`)
	// The pages people copy addresses from, with the provider, the kind and the id they hold.
	pages = []struct {
		re       *regexp.Regexp
		provider string
		kinds    map[string]string
	}{
		{regexp.MustCompile(`^(?:www\.)?themoviedb\.org/(movie|tv)/([0-9]+)`), "tmdb", map[string]string{"movie": "movie", "tv": "show"}},
		{regexp.MustCompile(`^(?:www\.|m\.)?imdb\.com/(?:[a-z]{2}/)?title/(tt[0-9]+)`), "imdb", nil},
		{regexp.MustCompile(`^(?:www\.)?thetvdb\.com/.*?(?:[?&]id=|/series/)([0-9]+)`), "tvdb", nil},
		{regexp.MustCompile(`^(?:www\.)?anilist\.co/anime/([0-9]+)`), "anilist", nil},
	}
)

// ParseRef reads what a person pasted into Enter a provider ID: an address of a title's page at
// TMDB, TheTVDB, AniList or IMDb, an IMDb id ("tt0133093"), an id written as in a file name
// ("tmdb-603", "{tvdb-81189}", "tmdb:603"), or a bare number, which is an id at the library's
// own provider. The answer's Kind is "movie" or "show" when the address says, else "".
func ParseRef(text, ownProvider string) (Ref, error) {
	text = strings.Trim(strings.TrimSpace(text), "{}")
	if imdbID.MatchString(text) {
		return Ref{Provider: "imdb", ID: text}, nil
	}
	if numberID.MatchString(text) {
		return Ref{Provider: ownProvider, ID: text}, nil
	}
	for _, sep := range []string{"-", ":", "="} {
		if provider, id, ok := strings.Cut(strings.ToLower(text), sep); ok && numberID.MatchString(id) {
			switch provider {
			case "tmdb", "tvdb", "anilist":
				return Ref{Provider: provider, ID: id}, nil
			}
		}
	}
	if u, err := url.Parse(text); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		page := strings.ToLower(u.Host) + u.EscapedPath()
		if u.RawQuery != "" {
			page += "?" + u.RawQuery
		}
		for _, p := range pages {
			m := p.re.FindStringSubmatch(page)
			switch {
			case m == nil:
			case p.kinds != nil:
				return Ref{Provider: p.provider, Kind: p.kinds[m[1]], ID: m[2]}, nil
			default:
				return Ref{Provider: p.provider, ID: m[1]}, nil
			}
		}
	}
	return Ref{}, ErrUnknownRef
}

// Matches answers the titles a person can choose from to fix a title's match, best first: a
// search by the title's own name and year when query is empty, a search by query and year, or
// the one title ref names (see ParseRef). Every answer is a network call to the provider.
func (s *Service) Matches(ctx context.Context, itemID, query string, year int, ref string) ([]Candidate, error) {
	t, p, locale, err := s.target(ctx, itemID)
	if err != nil {
		return nil, err
	}
	kind := providerKinds[t.Kind]
	if ref != "" {
		r, err := ParseRef(ref, p.Name())
		switch {
		case err != nil:
			return nil, err
		case r.Kind != "" && r.Kind != kind:
			return nil, ErrWrongKind
		case r.Provider == p.Name():
			r.Kind = kind
			record, err := p.Details(ctx, r, locale)
			if errors.Is(err, ErrNotFound) {
				return nil, ErrUnknownRef
			}
			if err != nil {
				return nil, err
			}
			return []Candidate{candidate(record)}, nil
		}
		// Another provider's id: the library's provider answers the one title it knows by it.
		found, err := p.Search(ctx, Query{Kind: kind, IDs: []Ref{{Provider: r.Provider, Kind: kind, ID: r.ID}}, Locale: locale,
			IncludeAdult: s.includeAdult()})
		if err != nil {
			return nil, err
		}
		if len(found) != 1 || !found[0].Exact {
			return nil, ErrUnknownRef
		}
		return found, nil
	}
	q := Query{Kind: kind, Title: t.Title, Year: t.Year, Locale: locale, IncludeAdult: s.includeAdult()}
	if strings.TrimSpace(query) != "" {
		q.Title, q.Year = strings.TrimSpace(query), year
	}
	return p.Search(ctx, q)
}

// candidate is the line a provider's full record makes in a list of matches.
func candidate(r Record) Candidate {
	c := Candidate{Ref: r.Ref, Title: r.Title, OriginalTitle: r.OriginalTitle, Year: r.Year, Overview: r.Overview, Exact: true}
	for _, picture := range r.Pictures {
		if picture.Role == "poster" {
			c.PosterURL = picture.URL
			break
		}
	}
	return c
}
