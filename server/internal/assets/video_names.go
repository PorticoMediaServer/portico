package assets

import (
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ffprobe supplies only this small title-tag whitelist for video. Audio's own
// tag reader remains authoritative for tracks and audiobook parts.
func EmbeddedVideoName(tags map[string]string) (naming.Embedded, bool) {
	e := naming.Embedded{}
	skipped := false
	values := map[string]string{}
	for key, value := range tags {
		key = strings.ToLower(key)
		value = strings.TrimSpace(value)
		if len(value) > 1000 || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			skipped = true
			continue
		}
		values[key] = value
	}
	e.Title = values["title"]
	e.ShowTitle = values["show"]
	if e.ShowTitle == "" {
		e.ShowTitle = values["tv_show"]
	}
	e.Edition = values["edition"]
	year := values["year"]
	if year == "" {
		year = values["date"]
	}
	if len(year) > 4 && year[4] == '-' {
		year = year[:4]
	}
	if n, err := strconv.Atoi(year); err == nil && n >= 1800 && n <= 2200 {
		e.Year = n
	} else if year != "" {
		skipped = true
	}
	number := func(value string) *int {
		if value == "" {
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 9999 {
			skipped = true
			return nil
		}
		return &n
	}
	e.Season = number(values["season_number"])
	e.Number = number(values["episode_sort"])
	for _, provider := range []string{"imdb", "tmdb", "tvdb", "anilist"} {
		id := values[provider]
		if id == "" {
			id = values[provider+"id"]
		}
		if id == "" {
			continue
		}
		r := naming.Read("Title {"+provider+"-"+id+"}.mkv", "movie")
		if len(r.ProviderIDs) == 1 && r.ProviderIDs[0].Value.ID == strings.ToLower(id) {
			e.IDs = append(e.IDs, r.ProviderIDs[0].Value)
		} else {
			skipped = true
		}
	}
	return e, skipped
}
