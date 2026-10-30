package metadataprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// TMDBAbsoluteOrder reads an explicit numbering offset. Local number plus offset
// is the position in the chosen provider order. No offset is inferred from files.
func TMDBAbsoluteOrder(order string) (base string, offset int, ok bool) {
	base = order
	if strings.HasPrefix(order, "tmdb-absolute:") {
		raw := strings.TrimPrefix(order, "tmdb-absolute:")
		cut := strings.LastIndexByte(raw, ':')
		if cut < 0 {
			return "", 0, false
		}
		base = raw[:cut]
		var err error
		offset, err = strconv.Atoi(raw[cut+1:])
		if err != nil || strconv.Itoa(offset) != raw[cut+1:] || offset < -1000000 || offset > 1000000 {
			return "", 0, false
		}
	}
	return base, offset, base == "official" || strings.HasPrefix(base, "tmdb-group:") && validTMDBGroup(strings.TrimPrefix(base, "tmdb-group:"))
}

func (s *TMDB) ScreenAbsoluteEpisode(ctx context.Context, show string, absolute int, order, language string) (ScreenRecord, error) {
	base, offset, ok := TMDBAbsoluteOrder(order)
	number := absolute + offset
	if !ValidScreenID(ScreenID{"tmdb", "show", show}) || absolute < 1 || absolute > 1000000 || !ok || number < 1 || number > 1000000 {
		return ScreenRecord{}, &Error{Provider: "tmdb", Code: "order_unavailable"}
	}
	season, episode := -1, -1
	if base == "official" {
		var series struct {
			ID      int64 `json:"id"`
			Seasons []struct {
				Number int `json:"season_number"`
				Count  int `json:"episode_count"`
			} `json:"seasons"`
		}
		if err := s.get(ctx, "/tv/"+show+"?"+url.Values{"language": {language}}.Encode(), &series); err != nil {
			return ScreenRecord{}, err
		}
		if numberID(series.ID) != show || len(series.Seasons) > 1000 {
			return ScreenRecord{}, &Error{Provider: "tmdb", Code: "invalid_episode_order"}
		}
		sort.SliceStable(series.Seasons, func(i, j int) bool { return series.Seasons[i].Number < series.Seasons[j].Number })
		last := 0
		for _, row := range series.Seasons {
			if row.Number == 0 {
				continue
			}
			if row.Number < 0 || row.Number > 9999 || row.Number <= last || row.Count < 0 || row.Count > 1000000 {
				return ScreenRecord{}, &Error{Provider: "tmdb", Code: "invalid_episode_order"}
			}
			last = row.Number
			if season < 0 && number <= row.Count {
				season, episode = row.Number, number
			}
			if season < 0 {
				number -= row.Count
			}
		}
	} else {
		// A group belongs to one show. Never use an arbitrary group's coordinates.
		series, err := s.ScreenDetails(ctx, "show", show, language, "")
		if err != nil {
			return ScreenRecord{}, err
		}
		found := false
		for _, row := range series.Orders {
			found = found || row.ID == base
		}
		if !found {
			return ScreenRecord{}, &Error{Provider: "tmdb", Code: "order_not_in_series"}
		}
		var data struct {
			ID     string `json:"id"`
			Groups []struct {
				Order    int `json:"order"`
				Episodes []struct {
					Order  int `json:"order"`
					Season int `json:"season_number"`
					Number int `json:"episode_number"`
				} `json:"episodes"`
			} `json:"groups"`
		}
		group := strings.TrimPrefix(base, "tmdb-group:")
		if err = s.get(ctx, "/tv/episode_group/"+group, &data); err != nil {
			return ScreenRecord{}, err
		}
		if data.ID != group || len(data.Groups) > 1000 {
			return ScreenRecord{}, &Error{Provider: "tmdb", Code: "invalid_episode_order"}
		}
		sort.SliceStable(data.Groups, func(i, j int) bool { return data.Groups[i].Order < data.Groups[j].Order })
		last, total := -1, 0
		seen := map[[2]int]bool{}
		for _, row := range data.Groups {
			if row.Order < 0 || row.Order > 9999 || row.Order <= last {
				return ScreenRecord{}, &Error{Provider: "tmdb", Code: "invalid_episode_order"}
			}
			last = row.Order
			sort.SliceStable(row.Episodes, func(i, j int) bool { return row.Episodes[i].Order < row.Episodes[j].Order })
			for i, ep := range row.Episodes {
				coordinate := [2]int{ep.Season, ep.Number}
				total++
				if ep.Order != i || ep.Season < 0 || ep.Season > 9999 || ep.Number < 0 || ep.Number > 1000000 || total > 1000000 || seen[coordinate] {
					return ScreenRecord{}, &Error{Provider: "tmdb", Code: "invalid_episode_order"}
				}
				seen[coordinate] = true
				if total == number {
					season, episode = ep.Season, ep.Number
				}
			}
		}
	}
	if season < 0 {
		return ScreenRecord{}, &Error{Provider: "tmdb", Code: "episode_outside_order"}
	}
	r, err := s.ScreenEpisode(ctx, show, season, episode, "official", language)
	if err != nil {
		return r, err
	}
	r.Coordinates.Season = nil
	r.Coordinates.Episode = absolute
	r.Coordinates.Absolute = &absolute
	r.Coordinates.Order = order
	return r, nil
}

// ScreenEpisodeConditional refreshes the exact provider episode previously mapped.
// Coordinates remain local; original coordinates address the provider document.
func (s *TMDB) ScreenEpisodeConditional(ctx context.Context, previous ScreenRecord, language string, in Conditional) (ScreenRecord, Conditional, error) {
	c := previous.Coordinates
	if ValidateScreenRecord(previous) != nil || previous.Identity.Provider != "tmdb" || previous.Identity.Type != "episode" || c == nil || !ValidScreenID(ScreenID{"tmdb", "show", c.ShowID}) || c.OriginalSeason == nil || c.OriginalEpisode == nil || *c.OriginalSeason < 0 || *c.OriginalSeason > 9999 || *c.OriginalEpisode < 0 || *c.OriginalEpisode > 1000000 {
		return ScreenRecord{}, in, &Error{Provider: "tmdb", Code: "invalid_episode_order"}
	}
	// The same English fallback as the first fetch, so a refresh never empties a filled overview.
	fallback, parts := s.fallbackLanguage(), "external_ids,credits"
	if fallback == "en" {
		parts += ",translations"
	}
	q := url.Values{"append_to_response": {parts}, "language": {language}}
	raw, out, err := s.http.rawConditional(ctx, nil, "", fmt.Sprintf("/tv/%s/season/%d/episode/%d?%s", c.ShowID, *c.OriginalSeason, *c.OriginalEpisode, q.Encode()), s.credential(), 4<<20, in)
	if err != nil {
		return ScreenRecord{}, out, err
	}
	var v tmdbScreen
	if json.Unmarshal(raw, &v) != nil || v.Season != *c.OriginalSeason || v.Episode != *c.OriginalEpisode {
		return ScreenRecord{}, out, &Error{Provider: "tmdb", Code: "episode_identity_mismatch"}
	}
	r := v.record("episode")
	if fallback == "en" {
		fillFallback(&r, v, language, fallback)
	}
	r.Coordinates = c
	r.Relations = append(r.Relations, ScreenRelation{"show", ScreenID{"tmdb", "show", c.ShowID}, ""})
	if r.Identity != previous.Identity {
		return r, out, &Error{Provider: "tmdb", Code: "episode_identity_mismatch"}
	}
	return r, out, ValidateScreenRecord(r)
}
