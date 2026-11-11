// Package live brings in Live TV: the sources the owner adds (a playlist, an Xtream sign-in,
// an HDHomeRun tuner, or a guide alone), their channels, and their listings into guide.db.
// Only internal/db runs SQL; this package reads what sources send and decides what comes in.
package live

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/contentrating"
)

// What one source may bring in. What is beyond is left out and the source's report says so.
const (
	MaxChannels   = 50_000
	MaxProgrammes = 5_000_000
)

// SourceReport is what a refresh or a preview brought in and left out, with a few examples why.
type SourceReport struct {
	Channels          int      `json:"channels"`
	SkippedChannels   int      `json:"skippedChannels"`
	Programmes        int      `json:"programmes"`
	SkippedProgrammes int      `json:"skippedProgrammes"`
	Groups            []string `json:"groups,omitempty"`
	Examples          []string `json:"examples,omitempty"`
	ChannelsWithGuide int      `json:"channelsWithGuide"`
	groupsSeen        map[string]bool
	examplesSeen      map[string]bool
}

func (r *SourceReport) note(reason string) {
	if r.examplesSeen == nil {
		r.examplesSeen = map[string]bool{}
	}
	if len(r.Examples) < 10 && !r.examplesSeen[reason] {
		r.examplesSeen[reason] = true
		r.Examples = append(r.Examples, reason)
	}
}

func (r *SourceReport) group(name string) {
	if name == "" {
		return
	}
	if r.groupsSeen == nil {
		r.groupsSeen = map[string]bool{}
	}
	if !r.groupsSeen[name] && len(r.Groups) < 500 {
		r.groupsSeen[name] = true
		r.Groups = append(r.Groups, name)
	}
}

// SentChannel is one channel a source sends. (Channel, in the contract, is a channel of the
// guide.)
type SentChannel struct {
	Key      string // the source's own name for it, stable across refreshes
	Name     string
	Number   string // as sent, "5.1" or "" for none
	Group    string
	GuideKey string // the guide's channel id, "" for none
	Locator  string // the stream's address; it never reaches an app
	Logo     string
}

// Programme is one programme of a guide.
type Programme struct {
	GuideKey     string
	StartsAt     time.Time
	EndsAt       time.Time
	Key          string // the guide's own programme id, or channel and start
	SeriesKey    string
	Title        string
	EpisodeTitle string
	Season       *int
	Episode      *int
	Summary      string
	Rating       string
	RatingAge    *int
	Flags        int
	Image        string
}

// Programme flags, as the guide marks them.
const (
	FlagNew      = 1
	FlagLive     = 2
	FlagPremiere = 4
	FlagRepeat   = 8
)

func validText(v string, max int) bool {
	return v != "" && len(v) <= max && utf8.ValidString(v) && !strings.ContainsFunc(v, unicode.IsControl)
}

// validLocator accepts an http or https address with a host and no sign-in in it.
func validLocator(v string) bool {
	if !validText(v, 8192) {
		return false
	}
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

var attribute = regexp.MustCompile(`([a-zA-Z0-9_-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s,]+))`)

// playlistSeparator is the comma that ends #EXTINF's attributes, outside quotes.
func playlistSeparator(line string) int {
	var quote rune
	for i, r := range line {
		if r == '"' || r == '\'' {
			if quote == 0 {
				quote = r
			} else if quote == r {
				quote = 0
			}
		}
		if r == ',' && quote == 0 {
			return i
		}
	}
	return -1
}

// ParsePlaylist reads an M3U playlist and calls fn with each usable channel. A line it cannot
// use is counted in the report and never loses the rest; a document that is not a playlist
// at all is an error.
func ParsePlaylist(r io.Reader, report *SourceReport, fn func(SentChannel) error) error {
	in := bufio.NewReaderSize(r, 64<<10)
	first, pending := true, false
	var ch SentChannel
	seen := map[string]bool{}
	skip := func(reason string) { report.SkippedChannels++; report.note(reason) }
	for {
		raw, long, err := line(in)
		text := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
		switch {
		case long:
			skip("A channel's line is too long.")
			pending = false
		case text == "":
		case first:
			first = false
			if text != "#EXTM3U" && !strings.HasPrefix(text, "#EXTM3U ") {
				return fmt.Errorf("this is not an M3U playlist")
			}
		case strings.HasPrefix(text, "#EXTINF:"):
			if pending {
				skip("A channel has no stream address.")
			}
			pending = false
			comma := playlistSeparator(text)
			if comma < 0 {
				skip("A channel's description could not be read.")
				break
			}
			ch = SentChannel{Name: strings.TrimSpace(text[comma+1:])}
			for _, m := range attribute.FindAllStringSubmatch(text[:comma], -1) {
				value := m[2] + m[3] + m[4]
				switch strings.ToLower(m[1]) {
				case "tvg-id":
					ch.Key, ch.GuideKey = value, value
				case "tvg-name":
					if ch.Name == "" {
						ch.Name = value
					}
				case "tvg-chno":
					ch.Number = value
				case "group-title":
					ch.Group = value
				case "tvg-logo":
					if validLocator(value) {
						ch.Logo = value
					}
				}
			}
			if !validText(ch.Name, 256) || ch.Key != "" && !validText(ch.Key, 256) || ch.Number != "" && !validText(ch.Number, 32) ||
				ch.Group != "" && !validText(ch.Group, 120) {
				skip("A channel's name, number or group could not be read.")
				break
			}
			pending = true
		case strings.HasPrefix(text, "#"):
		case !pending || !validLocator(text):
			skip("A channel's stream address is not an http or https address.")
			pending = false
		default:
			pending = false
			ch.Locator = text
			// HD and SD feeds, or regional ones, often share a guide id: the name tells them apart.
			if ch.Key == "" {
				// The name with the stream's host and path, never its query: many playlists add a
				// token there that changes at every download.
				u, _ := url.Parse(text)
				ch.Key = streamKey(ch.Name + "@" + u.Hostname() + u.Path)
			} else {
				ch.Key += "|" + ch.Name
			}
			if seen[ch.Key] {
				skip("A channel appears twice.")
				break
			}
			if len(seen) >= MaxChannels {
				skip(fmt.Sprintf("Only %d channels come in from one source.", MaxChannels))
				break
			}
			seen[ch.Key] = true
			report.group(ch.Group)
			if err := fn(ch); err != nil {
				return err
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if first {
		return fmt.Errorf("the playlist is empty")
	}
	if pending {
		skip("A channel has no stream address.")
	}
	return nil
}

// line reads one line, dropping a hostile long one in pieces so the next can be read.
func line(r *bufio.Reader) (string, bool, error) {
	var out []byte
	long := false
	for {
		part, err := r.ReadSlice('\n')
		if !long {
			if len(out)+len(part) > 16384 {
				long, out = true, nil
			} else {
				out = append(out, part...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return string(out), long, err
	}
}

// ChannelNumber reads "5", "5.1" or "5-1" as major * 1000 + minor, the stored number.
func ChannelNumber(text string) (int, bool) {
	major, minor, dotted := strings.Cut(strings.ReplaceAll(text, "-", "."), ".")
	m, err := strconv.Atoi(major)
	if err != nil || m < 0 || m > 99_999 {
		return 0, false
	}
	n := 0
	if dotted {
		if n, err = strconv.Atoi(minor); err != nil || n < 0 || n > 999 {
			return 0, false
		}
	}
	return m*1000 + n, true
}

// parseXMLTVTime reads the precision XMLTV allows, and an omitted zone as UTC.
func parseXMLTVTime(raw string) (time.Time, error) {
	parts := strings.Fields(raw)
	if len(parts) < 1 || len(parts) > 2 {
		return time.Time{}, fmt.Errorf("bad time")
	}
	stamp := parts[0]
	if len(stamp) < 4 || len(stamp) > 14 || len(stamp)%2 != 0 || strings.ContainsFunc(stamp, func(r rune) bool { return r < '0' || r > '9' }) {
		return time.Time{}, fmt.Errorf("bad time")
	}
	stamp += "00000101000000"[len(stamp):]
	zone := "+0000"
	if len(parts) == 2 {
		zone = parts[1]
	}
	return time.Parse("20060102150405 -0700", stamp+" "+zone)
}

// GuideChannel is a guide's channel entry: its id and the names it goes by.
type GuideChannel struct {
	Key   string
	Names []string
	Icon  string
}

// ParseGuide reads an XMLTV guide and calls channel and programme with what it finds, one at
// a time, never holding the whole guide. Entities are never expanded and a DOCTYPE may name
// only the tv document.
func ParseGuide(r io.Reader, report *SourceReport, channel func(GuideChannel) error, programme func(Programme) error) error {
	budget := &elementBudget{r: r}
	dec := xml.NewDecoder(budget)
	dec.CharsetReader = charset
	depth, root := 0, false
	count := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if errors.Is(err, errElementTooLarge) {
			return err
		}
		if err != nil {
			return fmt.Errorf("the guide could not be read: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			if root || !harmlessDoctype(t) {
				return fmt.Errorf("the guide declares something it may not")
			}
		case xml.StartElement:
			depth++
			if depth == 1 {
				if t.Name.Local != "tv" {
					return fmt.Errorf("this is not an XMLTV guide")
				}
				root = true
				continue
			}
			budget.left = maxElement // each channel and programme is read within its own budget
			switch t.Name.Local {
			case "channel":
				var row struct {
					ID    string   `xml:"id,attr"`
					Names []string `xml:"display-name"`
					Icons []struct {
						Src string `xml:"src,attr"`
					} `xml:"icon"`
				}
				if err := dec.DecodeElement(&row, &t); err != nil {
					return fmt.Errorf("the guide could not be read: %w", err)
				}
				depth--
				if !validText(row.ID, 256) {
					continue
				}
				gc := GuideChannel{Key: row.ID, Names: row.Names}
				for _, icon := range row.Icons {
					if validLocator(icon.Src) {
						gc.Icon = icon.Src
						break
					}
				}
				if err := channel(gc); err != nil {
					return err
				}
			case "programme":
				p, ok, err := decodeProgramme(dec, &t)
				depth--
				if err != nil {
					return err
				}
				if !ok {
					report.SkippedProgrammes++
					report.note("A programme's times or title could not be read.")
					continue
				}
				if count >= MaxProgrammes {
					report.SkippedProgrammes++
					report.note(fmt.Sprintf("Only %d programmes come in from one guide.", MaxProgrammes))
					continue
				}
				count++
				if err := programme(p); err != nil {
					return err
				}
			default:
				if err := dec.Skip(); err != nil {
					return fmt.Errorf("the guide could not be read: %w", err)
				}
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	if !root {
		return fmt.Errorf("this is not an XMLTV guide")
	}
	return nil
}

// maxElement is the most one channel or programme of a guide may take. A real one is a few
// kilobytes; the decoder holds what it reads of an element, so this bounds its memory too.
const maxElement = 1 << 20

var errElementTooLarge = errors.New("a programme or channel in the guide is larger than 1 MB")

// elementBudget fails a read once the element being read has taken more than its budget.
// Between elements, before the first one, the budget is the whole guide's.
type elementBudget struct {
	r    io.Reader
	left int64
	open bool
}

func (b *elementBudget) Read(p []byte) (int, error) {
	if !b.open {
		b.open, b.left = true, maxElement
	}
	if b.left <= 0 {
		return 0, errElementTooLarge
	}
	n, err := b.r.Read(p[:min(int64(len(p)), b.left)])
	b.left -= int64(n)
	return n, err
}

func decodeProgramme(dec *xml.Decoder, t *xml.StartElement) (Programme, bool, error) {
	var row struct {
		Channel  string   `xml:"channel,attr"`
		Start    string   `xml:"start,attr"`
		Stop     string   `xml:"stop,attr"`
		ID       string   `xml:"id,attr"`
		Title    []string `xml:"title"`
		SubTitle []string `xml:"sub-title"`
		Desc     []string `xml:"desc"`
		Icons    []struct {
			Src string `xml:"src,attr"`
		} `xml:"icon"`
		Episodes []struct {
			System string `xml:"system,attr"`
			Value  string `xml:",chardata"`
		} `xml:"episode-num"`
		Series   string    `xml:"series-id"`
		New      *struct{} `xml:"new"`
		Repeat   *struct{} `xml:"previously-shown"`
		Live     *struct{} `xml:"live"`
		Premiere *struct{} `xml:"premiere"`
		Ratings  []struct {
			Value string `xml:"value"`
		} `xml:"rating"`
	}
	if err := dec.DecodeElement(&row, t); err != nil {
		if errors.Is(err, errElementTooLarge) {
			return Programme{}, false, err
		}
		return Programme{}, false, fmt.Errorf("the guide could not be read: %w", err)
	}
	start, errStart := parseXMLTVTime(row.Start)
	end, errEnd := parseXMLTVTime(row.Stop)
	if errStart != nil || errEnd != nil || !end.After(start) || end.Sub(start) > 24*time.Hour || len(row.Title) == 0 ||
		!validText(row.Channel, 256) {
		return Programme{}, false, nil
	}
	p := Programme{GuideKey: row.Channel, StartsAt: start.UTC(), EndsAt: end.UTC(), Title: strings.Join(strings.Fields(row.Title[0]), " ")}
	if !validText(p.Title, 512) {
		return Programme{}, false, nil
	}
	p.Key = row.Channel + "@" + strconv.FormatInt(start.Unix(), 10)
	if validText(row.ID, 256) {
		p.Key = "id:" + row.ID
	}
	if len(row.SubTitle) > 0 && validText(row.SubTitle[0], 512) {
		p.EpisodeTitle = row.SubTitle[0]
	}
	if len(row.Desc) > 0 {
		if d := strings.Join(strings.Fields(row.Desc[0]), " "); validText(d, 4096) {
			p.Summary = d
		}
	}
	if validText(row.Series, 256) {
		p.SeriesKey = row.Series
	}
	for _, e := range row.Episodes {
		value := strings.TrimSpace(e.Value)
		switch e.System {
		case "xmltv_ns": // "season.episode.part", counting from 0
			parts := strings.Split(value, ".")
			if len(parts) >= 2 {
				if s, err := strconv.Atoi(strings.TrimSpace(strings.Split(parts[0], "/")[0])); err == nil {
					s++
					p.Season = &s
				}
				if n, err := strconv.Atoi(strings.TrimSpace(strings.Split(parts[1], "/")[0])); err == nil {
					n++
					p.Episode = &n
				}
			}
		case "dd_progid":
			if p.SeriesKey == "" && len(value) > 10 && strings.HasPrefix(value, "EP") {
				p.SeriesKey = value[:10]
			}
		}
	}
	for _, icon := range row.Icons {
		if len(icon.Src) <= 2048 && validLocator(icon.Src) {
			p.Image = icon.Src
			break
		}
	}
	if len(row.Ratings) > 0 {
		p.Rating = strings.TrimSpace(row.Ratings[0].Value)
		if age, ok := contentrating.Age(p.Rating); ok {
			p.RatingAge = &age
		}
	}
	switch {
	case row.New != nil && row.Repeat == nil:
		p.Flags |= FlagNew
	case row.Repeat != nil:
		p.Flags |= FlagRepeat
	}
	if row.Live != nil {
		p.Flags |= FlagLive
	}
	if row.Premiere != nil {
		p.Flags |= FlagPremiere
	}
	return p, true, nil
}

// harmlessDoctype accepts only "DOCTYPE tv" with an optional SYSTEM or PUBLIC literal, so no
// entity can be declared.
func harmlessDoctype(d xml.Directive) bool {
	text := strings.Join(strings.Fields(string(d)), " ")
	if len(text) > 512 || strings.ContainsAny(text, "<>[]%&") {
		return false
	}
	rest, ok := strings.CutPrefix(text, "DOCTYPE tv")
	if !ok {
		return false
	}
	rest = strings.TrimSpace(rest)
	return rest == "" || strings.HasPrefix(rest, "SYSTEM ") || strings.HasPrefix(rest, "PUBLIC ")
}

type latinReader struct {
	r       *bufio.Reader
	pending []byte
}

func (l *latinReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(l.pending) > 0 {
			k := copy(p[n:], l.pending)
			n += k
			l.pending = l.pending[k:]
			continue
		}
		b, err := l.r.ReadByte()
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		l.pending = []byte(string(rune(b)))
	}
	return n, nil
}

func charset(name string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(name) {
	case "utf-8", "utf8":
		return r, nil
	case "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		return &latinReader{r: bufio.NewReader(r)}, nil
	}
	return nil, fmt.Errorf("the guide's encoding %q is not one this server reads", name)
}
