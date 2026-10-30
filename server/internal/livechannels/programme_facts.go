package livechannels

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Programme facts (FEAT-04, Channels spec §8.1) are what a guide cell and the
// programme sheet show beyond the title: the episode line, category chips, the
// rating chip and the live/new/premiere flags. XMLTV sources supply them at
// parse time; Library Channel programmes fill them from the library item.

// ProgrammeEpisode is the episode line: season and number are 1-based;
// display is the provider's own wording ("Part 2") when there is no number.
type ProgrammeEpisode struct {
	Season  int    `json:"season,omitempty"`
	Number  int    `json:"number,omitempty"`
	Display string `json:"display,omitempty"`
}

// ProgrammeRating is one content rating, e.g. {system: "VCHIP", value: "TV-14"}.
type ProgrammeRating struct {
	System string `json:"system"`
	Value  string `json:"value"`
}

// ProgrammeFlags are the guide chips. New and Repeat are derived from the
// programme's newEvidence, so there is one source of truth for series rules.
type ProgrammeFlags struct {
	Live     bool `json:"live"`
	New      bool `json:"new"`
	Premiere bool `json:"premiere"`
	Repeat   bool `json:"repeat"`
}

// programmeFacts is what live_programme_metadata.facts stores.
type programmeFacts struct {
	Subtitle   string            `json:"subtitle,omitempty"`
	Episode    *ProgrammeEpisode `json:"episode,omitempty"`
	Categories []string          `json:"categories,omitempty"`
	Rating     *ProgrammeRating  `json:"rating,omitempty"`
	Year       int               `json:"year,omitempty"`
	StarRating string            `json:"starRating,omitempty"`
	Live       bool              `json:"live,omitempty"`
	Premiere   bool              `json:"premiere,omitempty"`
}

// MarshalJSON always publishes categories (an array) and flags, derived from
// newEvidence, so every reader sees the same shape.
func (p Programme) MarshalJSON() ([]byte, error) {
	type plain Programme
	if p.Categories == nil {
		p.Categories = []string{}
	}
	p.Flags.New = p.NewEvidence == "new"
	p.Flags.Repeat = p.NewEvidence == "repeat"
	return json.Marshal(plain(p))
}

// ApplyFacts copies stored facts (live_programme_metadata.facts) onto p. A
// malformed or empty document leaves p unchanged.
func (p *Programme) ApplyFacts(raw string) {
	if raw == "" || raw == "{}" {
		return
	}
	var f programmeFacts
	if json.Unmarshal([]byte(raw), &f) != nil {
		return
	}
	p.Subtitle, p.Episode, p.Categories, p.Rating, p.Year, p.StarRating = f.Subtitle, f.Episode, f.Categories, f.Rating, f.Year, f.StarRating
	p.Flags.Live, p.Flags.Premiere = f.Live, f.Premiere
}

// xmltvEpisode reads <episode-num system="xmltv_ns">: "season.episode.part",
// each 0-based and optionally "n/total"; "" parts are unknown.
func xmltvEpisode(v string) (season, number int, ok bool) {
	parts := strings.Split(strings.ReplaceAll(v, " ", ""), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, false
	}
	read := func(s string) int {
		s, _, _ = strings.Cut(s, "/")
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 9998 {
			return 0
		}
		return n + 1
	}
	season, number = read(parts[0]), read(parts[1])
	return season, number, season > 0 || number > 0
}

// xmltvYear reads <date>: YYYY, YYYYMM or YYYYMMDD, with an optional zone.
func xmltvYear(v string) int {
	v = strings.TrimSpace(v)
	if len(v) < 4 {
		return 0
	}
	n, err := strconv.Atoi(v[:4])
	if err != nil || n < 1850 || n > 2200 {
		return 0
	}
	return n
}

// xmltvFacts gathers one programme's facts; values a provider gets wrong are
// dropped rather than failing the whole guide.
func xmltvFacts(subtitles []string, episodes []struct {
	System string `xml:"system,attr"`
	Value  string `xml:",chardata"`
}, categories []string, date string, ratings []struct {
	System string `xml:"system,attr"`
	Value  string `xml:"value"`
}, stars []struct {
	Value string `xml:"value"`
}, live, premiere bool) string {
	f := programmeFacts{Live: live, Premiere: premiere, Year: xmltvYear(date)}
	if len(subtitles) > 0 {
		if v := strings.TrimSpace(subtitles[0]); validText(v, 512) {
			f.Subtitle = v
		}
	}
	var episode ProgrammeEpisode
	for _, ep := range episodes {
		v := strings.TrimSpace(ep.Value)
		switch ep.System {
		case "xmltv_ns":
			if season, number, ok := xmltvEpisode(v); ok && episode.Season == 0 && episode.Number == 0 {
				episode.Season, episode.Number = season, number
			}
		case "onscreen":
			if episode.Display == "" && validText(v, 64) {
				episode.Display = v
			}
		}
	}
	if episode != (ProgrammeEpisode{}) {
		f.Episode = &episode
	}
	seen := map[string]bool{}
	for _, c := range categories {
		c = strings.TrimSpace(c)
		if validText(c, 128) && !seen[strings.ToLower(c)] && len(f.Categories) < 16 {
			seen[strings.ToLower(c)] = true
			f.Categories = append(f.Categories, c)
		}
	}
	for _, r := range ratings {
		value, system := strings.TrimSpace(r.Value), strings.TrimSpace(r.System)
		if validText(value, 32) && (system == "" || validText(system, 64)) {
			f.Rating = &ProgrammeRating{System: system, Value: value}
			break
		}
	}
	if len(stars) > 0 {
		if v := strings.TrimSpace(stars[0].Value); validText(v, 16) {
			f.StarRating = v
		}
	}
	raw, _ := json.Marshal(f)
	return string(raw)
}

func factsOrEmpty(raw string) string {
	if raw == "" {
		return "{}"
	}
	return raw
}

// RestrictedProgrammeTitle stands in for a programme the viewer's profile may
// not see; Library Channels use the same words.
const RestrictedProgrammeTitle = "Not available on this profile"

// Restricted reduces p to its slot: identity, channel and times stay (the
// grid needs them); title, description, series and episode identity and every
// fact are withheld.
func (p *Programme) Restricted() {
	*p = Programme{ID: p.ID, ChannelID: p.ChannelID, Start: p.Start, End: p.End, Lineage: p.Lineage, Title: RestrictedProgrammeTitle, NewEvidence: "unknown", RecordingID: p.RecordingID, RecordingState: p.RecordingState}
}

// Admit applies a restriction predicate to p (nil admits everything).
func (p *Programme) Admit(allowed func(string) bool) {
	if allowed == nil {
		return
	}
	rating := ""
	if p.Rating != nil {
		rating = p.Rating.Value
	}
	if !allowed(rating) {
		p.Restricted()
	}
}

// GuideDays is Guide.Days: the whole days from now to the latest
// availableEnd of the listed sources, counting today.
func GuideDays(sources []GuideSource, now time.Time) int {
	var latest time.Time
	for _, s := range sources {
		if end, e := time.Parse(time.RFC3339Nano, s.AvailableEnd); e == nil && end.After(latest) {
			latest = end
		}
	}
	if !latest.After(now) {
		return 0
	}
	days := int(latest.Sub(now) / (24 * time.Hour))
	if latest.Sub(now)%(24*time.Hour) > 0 {
		days++
	}
	return min(days, 31)
}
