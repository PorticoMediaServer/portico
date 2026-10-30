package metadataprovider

// Documented provider trend feeds. TMDB exposes daily/weekly trending
// collections for movies and television (developer.themoviedb.org,
// /trending/movie/{window}, /trending/tv/{window}); AniList exposes trend
// sorting on its Page.media query (MediaSort TRENDING_DESC). MusicBrainz and
// TheTVDB have no documented trend endpoint, so nothing here invents one:
// unsupported domains keep personalized and recently-added discovery rows.
//
// Like every adapter in this package this is transport-only. Snapshot
// persistence, consent, policy, backoff and identity matching are the metadata
// service's durable concerns.

import (
	"context"
	"errors"
	"net/url"
	"strconv"
)

// TrendingWindow is the provider feed window this server reads. Daily is the
// documented feed with the least day-to-day volatility; the refresh cadence is
// the scheduler's business, not the adapter's.
const TrendingWindow = "day"

// trendingMaxPages bounds one refresh. A page holds the provider's default
// page size (TMDB: 20 entries; AniList: aniTrendingPageSize below), so the cap
// bounds both response size and the number of requests per refresh.
const trendingMaxPages = 3

// aniTrendingPageSize is the AniList Page perPage value the trending query
// uses.
const aniTrendingPageSize = 25

// TrendEntry is one provider trend position: the stable provider identity and
// its adult flag, and nothing else. Trend rows are persisted as identities the
// catalog matches against locally available media, so titles and artwork are
// never fetched, decoded or stored here — a lean request is also the privacy
// posture: the provider sees a feed request, not a library.
type TrendEntry struct {
	Provider string
	Type     string
	ID       string
	Adult    bool
}

// TrendingPages returns the page count a refresh may use for a desired row
// size, clamped to the bounded budget.
func TrendingPages(rows int) int {
	if rows <= 0 {
		return 0
	}
	pages := (rows + aniTrendingPageSize - 1) / aniTrendingPageSize
	if pages < 1 {
		pages = 1
	}
	if pages > trendingMaxPages {
		pages = trendingMaxPages
	}
	return pages
}

// TrendingEntryKind maps a persisted discovery media_kind onto the provider
// entity type the feed results are typed with. TMDB's television entities are
// typed "show"; the discovery contract's media_kind is "tv". Keeping the
// mapping in one named function is what makes the persisted kind and the
// transport kind verifiably distinct.
func TrendingType(kind string) (string, bool) {
	switch kind {
	case "movie":
		return "movie", true
	case "tv":
		return "show", true
	case "anime":
		return "anime", true
	}
	return "", false
}

type trendingResults struct {
	Results []struct {
		ID    int64 `json:"id"`
		Adult *bool `json:"adult"`
	} `json:"results"`
}

// Trending fetches the documented daily trending collection for one discovery
// media kind ("movie" or "tv"). Entries arrive in the provider's trend order;
// adult evidence is dropped here so no adult row can reach a snapshot. Only
// the identity fields are decoded, which keeps parsing cost and response-size
// risk off the worker step.
func (s *TMDB) Trending(ctx context.Context, kind, language string, pages int) ([]TrendEntry, error) {
	if kind != "movie" && kind != "tv" {
		return nil, errors.New("unsupported TMDB trending kind")
	}
	if pages < 1 || pages > trendingMaxPages {
		return nil, errors.New("trending page budget exceeded")
	}
	base := "/trending/" + kind + "/" + TrendingWindow
	out := []TrendEntry{}
	for page := 1; page <= pages; page++ {
		route := base + "?page=" + strconv.Itoa(page)
		if language != "" {
			route += "&language=" + url.QueryEscape(language)
		}
		var results trendingResults
		if e := s.get(ctx, route, &results); e != nil {
			return nil, e
		}
		for _, v := range results.Results {
			if v.ID <= 0 {
				continue
			}
			providerType, ok := TrendingType(kind)
			if !ok {
				continue
			}
			adult := v.Adult != nil && *v.Adult
			if adult {
				continue // adult media has no discovery row
			}
			out = append(out, TrendEntry{Provider: "tmdb", Type: providerType, ID: numberID(v.ID)})
		}
	}
	return out, nil
}

type aniTrendingResults struct {
	Page struct {
		Media []struct {
			ID    int64  `json:"id"`
			Type  string `json:"type"`
			Adult *bool  `json:"isAdult"`
		} `json:"media"`
	} `json:"Page"`
}

// aniTrendingQuery is deliberately lean: a trend row needs its identity and
// adult flag, not the work's staff, relations, description and studio graph
// the item matcher asks for. A smaller document is also less exposure to the
// response-size budget.
const aniTrendingQuery = `query($page:Int){Page(page:$page,perPage:25){media(type:ANIME,sort:TRENDING_DESC,isAdult:false){id type isAdult}}}`

// Trending fetches AniList's trend-sorted pages of anime works. The request is
// the documented Page.media query with MediaSort TRENDING_DESC and the adult
// filter; entries keep their stable AniList work identity, which is what the
// snapshot stores.
func (s *AniList) Trending(ctx context.Context, language string, pages int) ([]TrendEntry, error) {
	if pages < 1 || pages > trendingMaxPages {
		return nil, errors.New("trending page budget exceeded")
	}
	out := []TrendEntry{}
	for page := 1; page <= pages; page++ {
		var data aniTrendingResults
		if e := s.query(ctx, "trending:"+strconv.Itoa(page), aniTrendingQuery, map[string]any{"page": page}, &data); e != nil {
			return nil, e
		}
		for _, v := range data.Page.Media {
			// Non-ANIME nodes, malformed identities and adult entries are
			// dropped, not fatal: a snapshot keeps working when the feed
			// carries noise.
			if v.Type != "ANIME" || v.ID <= 0 {
				continue
			}
			if v.Adult != nil && *v.Adult {
				continue
			}
			out = append(out, TrendEntry{Provider: "anilist", Type: "anime", ID: numberID(v.ID)})
		}
	}
	return out, nil
}
