package enrich

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

var ErrSourceURL = errors.New("invalid TMDB acquisition source URL")

// Adopted narrow FIRST representation. A parser change must also change the
// provider-subject/evidence request-shape namespace: evidence uniqueness does
// not include parser_version. Other metadata families remain out of scope.
const TMDBMovieParserVersion = "tmdb.movie.header-selected-credits.v1"
const TMDBMovieRequestShape = "tmdb.movie.detail+credits.header-selected-credits.v1"

// SafeTMDBSourceURL retains only the original closed movie-route origin/path.
// Strip ALL query parameters, userinfo and fragments before metadata custody,
// caching, storage or logging. An allowlist of query names could accidentally
// retain a differently named credential; none of them is needed as authority.
// Typed provider ID/locale/request-shape remain separate native witnesses.
// This function grants no fetch permission and performs no network operation.
func SafeTMDBSourceURL(raw, providerID string) (string, error) {
	id, err := strconv.ParseInt(providerID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != providerID {
		return "", ErrSourceURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Scheme != "https" ||
		!strings.EqualFold(u.Hostname(), "api.themoviedb.org") ||
		(u.Port() != "" && u.Port() != "443") || u.Path != "/3/movie/"+providerID {
		// Never include raw in an error: it can contain the credential being
		// removed, including a malformed escape rejected by net/url.
		return "", ErrSourceURL
	}
	u.User = nil
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	u.ForceQuery = false
	// The decoded path was matched against our fixed public route above.
	// Re-encode it without retaining an alternate escaped spelling/backing URL.
	u.RawPath = ""
	return u.String(), nil
}
