package naming

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var episodeToken = regexp.MustCompile(`(?i)s[0-9]{1,4}e[0-9]{1,4}(?:(?:e|-e?)[0-9]{1,4})*|[0-9]{1,4}x[0-9]{1,4}(?:(?:x|-x?)[0-9]{1,4})*`)
var episodeContinuation = regexp.MustCompile(`(?i)^[ ._-]*[ex]([0-9]{1,4})(?:$|[ ._-])`)
var digits = regexp.MustCompile(`[0-9]+`)
var dateToken = regexp.MustCompile(`(?:^|[ ._-])((?:18|19|20)[0-9]{2})[ ._-]([0-9]{2})[ ._-]([0-9]{2})(?:$|[^0-9])`)
var dayFirstDateToken = regexp.MustCompile(`(?:^|[ ._-])([0-9]{2})[ .-]([0-9]{2})[ .-]((?:18|19|20)[0-9]{2})(?:$|[^0-9])`)
var episodeOnly = regexp.MustCompile(`(?i)^(?:e|episode[ ._-]*)([0-9]{1,4})(?:$|[ ._-])`)
var leadingNumber = regexp.MustCompile(`^([0-9]{1,3})(?:$|[ ._-])`)
var numberedTail = regexp.MustCompile(`(?i)^(.*?)[ ._]+-[ ._]+([0-9]{1,4})(?:v([0-9]{1,2}))?(?:$|[ ._\[-])`)
var packedNumber = regexp.MustCompile(`^(.*?)[ ._-]+([0-9]{3})(?:$|[ ._-])`)
var group = regexp.MustCompile(`^\[[^\]]{1,128}\][ ._]*`)
var frameWidths = map[int]bool{7680: true, 4096: true, 3840: true, 3440: true, 2560: true, 2048: true, 1920: true, 1440: true, 1280: true, 1080: true, 1024: true, 960: true, 854: true, 852: true, 848: true, 800: true, 768: true, 720: true, 704: true, 640: true, 624: true, 608: true, 576: true, 544: true, 512: true, 480: true, 426: true, 320: true}
var trackToken = regexp.MustCompile(`^(?:([0-9]{1,3})[.-])?([0-9]{1,4})[ ._-]+(.+)$`)
var chapterToken = regexp.MustCompile(`(?i)^(?:chapter|part|track)[ ._-]*([0-9]{1,4})(?:[ ._-]+|$)`)

func boundary(s string, i int) bool {
	return i < 0 || i >= len(s) || strings.ContainsRune(" ._-[]()", rune(s[i]))
}
func validMarker(s string, loc []int) bool {
	if !boundary(s, loc[0]-1) || !boundary(s, loc[1]) {
		return false
	}
	raw := strings.ToLower(s[loc[0]:loc[1]])
	if raw[0] == 's' {
		return true
	}
	n := digits.FindAllString(raw, -1)
	a, _ := strconv.Atoi(n[0])
	b, _ := strconv.Atoi(n[1])
	// Common frame widths and long dimension pairs are picture sizes. Explicit
	// S/E remains available for unusually large seasonal numbers.
	if (b == 264 || b == 265) && loc[0] >= 2 && s[loc[0]-1] == '.' && s[loc[0]-2] >= '0' && s[loc[0]-2] <= '9' {
		return false
	}
	return !(a >= 240 && b >= 240 || frameWidths[a] && b >= 100)
}
func parseNumbers(raw string) (int, []int, string) {
	ns := digits.FindAllString(raw, -1)
	season, _ := strconv.Atoi(ns[0])
	var values []int
	for _, s := range ns[1:] {
		n, _ := strconv.Atoi(s)
		if n > 9999 {
			return 0, nil, "episode_number_out_of_range"
		}
		values = append(values, n)
	}
	if strings.Contains(raw, "-") {
		if len(values) != 2 || values[1] < values[0] || values[1]-values[0] >= MaxEpisodeLinks {
			return 0, nil, "ambiguous_episode_range"
		}
		first, last := values[0], values[1]
		values = nil
		for i := first; i <= last; i++ {
			values = append(values, i)
		}
	}
	if len(values) > MaxEpisodeLinks {
		return 0, nil, "too_many_episode_links"
	}
	for i, n := range values {
		if i > 0 && n <= values[i-1] {
			return 0, nil, "conflicting_episode_order"
		}
	}
	return season, values, ""
}
func readEpisode(r *Reading, name, p, kind string, folderSeason int, hasFolder bool) string {
	s := group.ReplaceAllString(name, "")
	// Technical labels are skipped individually so a resolution before S/E is
	// harmless. Explicit marker boundaries prevent matches inside title words.
	var locs [][]int
	for _, loc := range episodeToken.FindAllStringIndex(s, 32) {
		if validMarker(s, loc) {
			locs = append(locs, loc)
		}
	}
	if len(locs) > 0 {
		first := locs[0]
		// A second marker following a titled episode is episode-title text; two
		// adjacent markers without a title are contradictory file evidence.
		if len(locs) > 1 {
			between := clean(s[first[1]:locs[1][0]])
			if between == "" && !strings.Contains(s[first[1]:locs[1][0]], " - ") {
				r.Diagnostics = append(r.Diagnostics, "conflicting_episode_markers")
				return s[:first[0]]
			}
		}
		season, values, issue := parseNumbers(s[first[0]:first[1]])
		if issue != "" {
			r.Diagnostics = append(r.Diagnostics, issue)
			return s[:first[0]]
		}
		remainder := s[first[1]:]
		for {
			continuation := episodeContinuation.FindStringSubmatchIndex(remainder)
			if continuation == nil {
				break
			}
			n, _ := strconv.Atoi(remainder[continuation[2]:continuation[3]])
			if len(values) >= MaxEpisodeLinks {
				r.Diagnostics = append(r.Diagnostics, "too_many_episode_links")
				return s[:first[0]]
			}
			if n <= values[len(values)-1] {
				r.Diagnostics = append(r.Diagnostics, "conflicting_episode_order")
				return s[:first[0]]
			}
			values = append(values, n)
			remainder = remainder[continuation[3]:]
		}
		r.Seasons = append([]Evidence[int]{ev(season, FileName, p, .99)}, r.Seasons...)
		r.Episodes = append(r.Episodes, ev(values, FileName, p, .99))
		title := s[:first[0]]
		// Remove only tags from the series prefix, not the title around them.
		if loc := technicalToken.FindStringIndex(title); loc != nil {
			title = title[:loc[0]]
		}
		return title
	}
	if m := dateToken.FindStringSubmatchIndex(s); m != nil {
		d := s[m[2]:m[3]] + "-" + s[m[4]:m[5]] + "-" + s[m[6]:m[7]]
		if date, err := time.Parse("2006-01-02", d); err != nil || date.Year() < 1900 {
			r.Diagnostics = append(r.Diagnostics, "invalid_air_date")
		} else {
			r.AirDates = append(r.AirDates, ev(d, FileName, p, .98))
		}
		return s[:m[0]]
	}
	if m := dayFirstDateToken.FindStringSubmatchIndex(s); m != nil {
		d := s[m[6]:m[7]] + "-" + s[m[4]:m[5]] + "-" + s[m[2]:m[3]]
		if date, err := time.Parse("2006-01-02", d); err != nil || date.Year() < 1900 {
			r.Diagnostics = append(r.Diagnostics, "invalid_air_date")
		} else {
			r.AirDates = append(r.AirDates, ev(d, FileName, p, .98))
		}
		return s[:m[0]]
	}
	if m := episodeOnly.FindStringSubmatchIndex(s); m != nil && hasFolder {
		n, _ := strconv.Atoi(s[m[2]:m[3]])
		addBareEpisode(r, n, p, folderSeason, .9)
		return ""
	}
	if m := numberedTail.FindStringSubmatchIndex(s); m != nil {
		n, _ := strconv.Atoi(s[m[4]:m[5]])
		// Years and resolution widths are not absolute numbers. The explicit
		// seasonal syntax remains available for very large episode numbers.
		if n >= 1800 && n <= 2099 || n == 480 || n == 576 || n == 720 || n == 1080 || n == 2160 || n == 4320 {
			return s
		}
		if kind == "tv" && len(s[m[4]:m[5]]) == 3 && n >= 100 && folderSeason < 0 {
			r.Seasons = append([]Evidence[int]{ev(n/100, FileName, p, .7)}, r.Seasons...)
			r.Episodes = append(r.Episodes, ev([]int{n % 100}, FileName, p, .7))
		} else if folderSeason >= 0 {
			addBareEpisode(r, n, p, folderSeason, .8)
		} else {
			r.AbsoluteNumbers = append(r.AbsoluteNumbers, ev(n, FileName, p, .8))
		}
		if m[6] >= 0 {
			r.Versions = append(r.Versions, ev("v"+s[m[6]:m[7]], FileName, p, .95))
		}
		return s[m[2]:m[3]]
	}
	if m := leadingNumber.FindStringSubmatchIndex(s); m != nil && hasFolder && (folderSeason >= 0 || kind == "anime") {
		n, _ := strconv.Atoi(s[m[2]:m[3]])
		addBareEpisode(r, n, p, folderSeason, .8)
		return ""
	}
	if m := packedNumber.FindStringSubmatchIndex(s); m != nil && hasFolder && kind == "tv" {
		n, _ := strconv.Atoi(s[m[4]:m[5]])
		if n >= 100 && n%100 > 0 {
			r.Seasons = append([]Evidence[int]{ev(n/100, FileName, p, .65)}, r.Seasons...)
			r.Episodes = append(r.Episodes, ev([]int{n % 100}, FileName, p, .65))
			return s[m[2]:m[3]]
		}
	}
	return s
}
func addBareEpisode(r *Reading, n int, p string, season int, strength float64) {
	if season >= 0 {
		r.Episodes = append(r.Episodes, ev([]int{n}, FileName, p, strength))
	} else {
		r.AbsoluteNumbers = append(r.AbsoluteNumbers, ev(n, FileName, p, strength))
	}
}
func readAudio(r *Reading, name, p string, parts []string, kind string) string {
	title := name
	if m := trackToken.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[2])
		r.TrackNumbers = append(r.TrackNumbers, ev(n, FileName, p, .85))
		if m[1] != "" {
			d, _ := strconv.Atoi(m[1])
			r.DiscNumbers = append([]Evidence[int]{ev(d, FileName, p, .85)}, r.DiscNumbers...)
		}
		title = m[3]
	}
	if kind == "audiobook" {
		if m := chapterToken.FindStringSubmatchIndex(name); m != nil {
			n, _ := strconv.Atoi(name[m[2]:m[3]])
			r.TrackNumbers = append(r.TrackNumbers, ev(n, FileName, p, .9))
			title = name[m[1]:]
		}
	}
	album := len(parts) - 2
	if album >= 0 {
		if _, ok := DiscFolder(parts[album]); ok {
			album--
		}
	}
	if album >= 0 {
		var folder Reading
		readComponent(&folder, parts[album], parts[album], source(len(parts)-1-album), strings.Join(parts[:album+1], "/"), .8)
		title, _ := folder.PreferredTitle()
		r.Albums = append(r.Albums, ev(title, source(len(parts)-1-album), strings.Join(parts[:album+1], "/"), .8))
		if album > 0 {
			r.Artists = append(r.Artists, ev(clean(parts[album-1]), source(len(parts)-album), strings.Join(parts[:album], "/"), .7))
		}
	}
	return title
}
