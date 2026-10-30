package livechannels

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var attribute = regexp.MustCompile(`([a-zA-Z0-9_-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s,]+))`)

func validText(v string, max int) bool {
	return v != "" && len(v) <= max && utf8.ValidString(v) && !strings.ContainsFunc(v, unicode.IsControl)
}
func validLocator(v string) bool {
	if !validText(v, 8192) {
		return false
	}
	u, e := url.Parse(v)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

// Parse uploaded data only. Advertised logo URLs are recorded for the
// background importer; parsing never fetches them.
func parse(in SourceInput) (out parsedSource, result error) {
	out = parsedSource{diagnostics: ImportDiagnostics{Examples: []string{}}}
	defer func() {
		if result != nil {
			out.close()
		}
	}()
	if !validText(in.Name, 120) || len(in.Playlist) == 0 || in.TunerCount < 0 || in.TunerCount > 256 || in.ExpectedRevision < 0 {
		return out, ErrInvalid
	}
	limits := in.Limits.effective()
	if limits.Bytes > 1<<30 || limits.Channels > 100000 || limits.Programmes > 10000000 || len(in.Playlist)+len(in.Guide) > limits.Bytes {
		return out, ErrInvalid
	}
	scan := bufio.NewReaderSize(strings.NewReader(strings.TrimPrefix(in.Playlist, "\ufeff")), 16384)
	first, pending := true, false
	var ch parsedChannel
	keys := map[string]bool{}
	byKey := map[string]parsedChannel{}
	groups, enabled := map[string]bool{}, map[string]bool{}
	for _, v := range in.EnabledGroups {
		groups[v] = true
	}
	for _, v := range in.EnabledChannels {
		enabled[v] = true
	}
	skip := func(reason string) { out.diagnostics.SkippedChannels++; out.diagnostics.note(reason) }
	for {
		raw, long, err := playlistLine(scan)
		if long {
			skip("live.import.channelLineTooLong")
			pending = false
			if err == io.EOF {
				break
			}
			continue
		}
		line := strings.TrimSpace(raw)
		if line != "" {
			if first {
				first = false
				if line != "#EXTM3U" && !strings.HasPrefix(line, "#EXTM3U ") {
					return out, ErrInvalid
				}
			} else if strings.HasPrefix(line, "#EXTINF:") {
				if pending {
					skip("live.import.channelAddressMissing")
				}
				pending = false
				comma := playlistSeparator(line)
				if comma < 0 {
					skip("live.import.channelDescriptionInvalid")
				} else {
					ch = parsedChannel{name: strings.TrimSpace(line[comma+1:])}
					for _, m := range attribute.FindAllStringSubmatch(line[:comma], -1) {
						value := m[2]
						if m[3] != "" {
							value = m[3]
						}
						if m[4] != "" {
							value = m[4]
						}
						switch strings.ToLower(m[1]) {
						case "tvg-id":
							ch.key = value
							ch.guideKey = value
						case "tvg-name":
							if ch.name == "" {
								ch.name = value
							}
						case "tvg-chno":
							ch.number = value
							ch.sourceNumber = value
						case "group-title":
							ch.group = value
						case "tvg-country":
							ch.country = value
						case "tvg-logo":
							if validLocator(value) {
								ch.logo = value
							}
						case "catchup":
							ch.catchup = value
						case "catchup-source":
							ch.catchupSource = value
						case "catchup-days", "timeshift":
							ch.catchupDays = value
						}
					}
					if !validText(ch.name, 256) || ch.key != "" && !validText(ch.key, 256) || ch.number != "" && !validText(ch.number, 32) || ch.group != "" && !validText(ch.group, 120) {
						skip("live.import.channelDescriptionInvalid")
					} else {
						pending = true
					}
				}
			} else if !strings.HasPrefix(line, "#") {
				if !pending || !validLocator(line) {
					skip("live.import.channelAddressInvalid")
					pending = false
				} else {
					pending = false
					ch.locator = line

					if ch.key == "" {
						u, _ := url.Parse(line)
						ch.key = "fallback-" + id(ch.name, u.Hostname(), u.Path)
					}
					// A selected duplicate variant retains its identity even when
					// the first provider row with that ID was not selected.
					variant := "variant-" + id(ch.key, ch.name, ch.number, ch.group)
					if len(enabled) > 0 && !enabled[ch.key] && enabled[variant] {
						ch.key = variant
					}
					if old, ok := byKey[ch.key]; ok {
						if old == ch {
							continue
						}
						ch.key = "variant-" + id(ch.key, ch.name, ch.number, ch.group)
						if keys[ch.key] {
							skip("live.import.channelRepeated")
							continue
						}
					}

					if len(out.choices) >= limits.Channels {
						skip("live.import.channelLimit")
						continue
					}
					keys[ch.key] = true
					byKey[ch.key] = ch
					out.choices = append(out.choices, ch)
					if in.configuration != nil && (!filterMatch(ch.group, in.configuration.Filters.Categories) || !filterMatch(ch.country, in.configuration.Filters.Countries) || !filterMatch(ch.name, in.configuration.Filters.Keywords)) {
						continue
					}
					if len(groups) > 0 && !groups[ch.group] || len(enabled) > 0 && !enabled[ch.key] {
						continue
					}

					out.channels = append(out.channels, ch)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, ErrInvalid
		}
	}
	if pending {
		skip("live.import.channelAddressMissing")
	}
	if len(out.channels) == 0 {
		return out, ErrInvalid
	}
	channelGuide := map[string]string{}
	for _, ch := range out.channels {
		channelGuide[ch.key] = ch.guideKey
		if ch.guideKey == "" {
			channelGuide[ch.key] = ch.key
		}
	}
	mapped := map[string]bool{}
	for _, m := range in.Mappings {
		if !keys[m.ChannelKey] {
			continue
		}
		if mapped[m.ChannelKey] || !validText(m.GuideKey, 256) {
			return out, ErrInvalid
		}
		mapped[m.ChannelKey] = true
		channelGuide[m.ChannelKey] = m.GuideKey
	}
	guideLookup := indexGuideChannels(out.channels, mapped)
	mapping := map[string][]string{}
	for _, ch := range out.channels {
		mapping[channelGuide[ch.key]] = append(mapping[channelGuide[ch.key]], ch.key)
	}
	knownMappings := map[string]map[string]bool{}
	aliasCounts := map[string]int{}
	for guideID, channels := range mapping {
		knownMappings[guideID] = map[string]bool{}
		for _, channel := range channels {
			knownMappings[guideID][channel] = true
			aliasCounts[channel]++
		}
	}
	if in.guideFile == "" && strings.TrimSpace(in.Guide) == "" {
		applyConfiguration(&out, in.configuration, in.guideDays)
		return out, nil
	}
	reader, e := in.openGuide()
	if e != nil {
		return out, e
	}
	defer reader.Close()
	digest := sha256.New()
	dec := newGuideDecoder(io.TeeReader(reader, digest))
	depth := 0
	root := false
	closed := false
	declared := false
	seen := map[string]parsedProgramme{}
	icons := map[string]bool{}
	for {
		if in.parseContext != nil {
			if e := in.parseContext.Err(); e != nil {
				return out, e
			}
		}
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return out, ErrInvalid
		}
		switch t := tok.(type) {
		case xml.Directive:
			if root || declared || !harmlessGuideDoctype(t) {
				return out, ErrInvalid
			}
			declared = true
		case xml.StartElement:
			if closed {
				return out, ErrInvalid
			}
			depth++
			if depth == 1 {
				if t.Name.Local != "tv" {
					return out, ErrInvalid
				}
				root = true
				continue
			}
			if depth != 2 {
				return out, ErrInvalid
			}
			if t.Name.Local == "channel" {
				var row struct {
					ID    string   `xml:"id,attr"`
					Names []string `xml:"display-name"`
					Icons []struct {
						Src string `xml:"src,attr"`
					} `xml:"icon"`
				}
				if e = dec.DecodeElement(&row, &t); e != nil {
					return out, ErrInvalid
				}
				depth--
				if !validText(row.ID, 256) {
					continue
				}
				extraMatches := false
				for _, i := range guideLookup.match(row.ID, row.Names) {
					ch := &out.channels[i]
					if !knownMappings[row.ID][ch.key] {
						if aliasCounts[ch.key] >= maxGuideAliases {
							extraMatches = true
							continue
						}
						if knownMappings[row.ID] == nil {
							knownMappings[row.ID] = map[string]bool{}
						}
						knownMappings[row.ID][ch.key] = true
						aliasCounts[ch.key]++
						mapping[row.ID] = append(mapping[row.ID], ch.key)
					}
					if ch.logo == "" {
						for _, icon := range row.Icons {
							if validLocator(icon.Src) {
								ch.logo = icon.Src
								break
							}
						}
					}
				}
				if extraMatches {
					out.diagnostics.SkippedChannels++
					out.diagnostics.note("live.import.guideMatchesLimited")
				}
				continue
			}
			if t.Name.Local != "programme" {
				if e = dec.Skip(); e != nil {
					return out, ErrInvalid
				}
				depth--
				continue
			}
			var row struct {
				Channel     string   `xml:"channel,attr"`
				Start       string   `xml:"start,attr"`
				Stop        string   `xml:"stop,attr"`
				ID          string   `xml:"id,attr"`
				Title       []string `xml:"title"`
				Description []string `xml:"desc"`
				Icons       []struct {
					Src string `xml:"src,attr"`
				} `xml:"icon"`
				Episodes []struct {
					System string `xml:"system,attr"`
					Value  string `xml:",chardata"`
				} `xml:"episode-num"`
				Series          string    `xml:"series-id"`
				New             *struct{} `xml:"new"`
				PreviouslyShown *struct{} `xml:"previously-shown"`
				SubTitle        []string  `xml:"sub-title"`
				Categories      []string  `xml:"category"`
				Date            string    `xml:"date"`
				Ratings         []struct {
					System string `xml:"system,attr"`
					Value  string `xml:"value"`
				} `xml:"rating"`
				StarRatings []struct {
					Value string `xml:"value"`
				} `xml:"star-rating"`
				Live     *struct{} `xml:"live"`
				Premiere *struct{} `xml:"premiere"`
			}
			if e = dec.DecodeElement(&row, &t); e != nil {
				return out, ErrInvalid
			}
			depth--
			channelKeys := mapping[row.Channel]
			start, e1 := parseXMLTVTime(row.Start)
			end, e2 := parseXMLTVTime(row.Stop)
			if row.Stop == "" {
				e2 = nil
			}
			for i := range row.Title {
				row.Title[i] = strings.Join(strings.Fields(row.Title[i]), " ")
			}
			if e1 != nil || e2 != nil || !end.IsZero() && (!end.After(start) || end.Sub(start) > 7*24*time.Hour) || len(row.Title) == 0 || !validText(row.Title[0], 512) {
				out.diagnostics.SkippedProgrammes++
				out.diagnostics.note("live.import.programmeInvalid")
				continue
			}
			// Validate the provider document before filtering its rows. An invalid
			// timestamp in an unmapped row is not a successful guide refresh.
			if !validText(row.Channel, 256) {
				out.diagnostics.SkippedProgrammes++
				continue
			}
			// The provider's programme thumbnail is the first <icon> with a
			// usable locator. An invalid icon is ignored, never a parse error.
			// At most 5000 distinct icon URLs are kept per generation; further
			// new URLs are dropped while repeats of a kept URL are retained.
			icon := ""
			for _, ic := range row.Icons {
				if len(ic.Src) <= 2048 && validLocator(ic.Src) {
					icon = ic.Src
					break
				}
			}
			if icon != "" && !icons[icon] {
				if len(icons) >= 5000 {
					icon = ""
				} else {
					icons[icon] = true
				}
			}
			for _, channelKey := range channelKeys {
				p := parsedProgramme{channel: channelKey, title: row.Title[0], start: start.UTC(), end: end.UTC(), lineage: "interval-only", newEvidence: "unknown", icon: icon}
				if row.Series != "" {
					if validText(row.Series, 256) {
						p.series = row.Series
					}
				}
				for _, ep := range row.Episodes {
					if ep.System == "dd_progid" && validText(strings.TrimSpace(ep.Value), 256) {
						p.episode = strings.TrimSpace(ep.Value)
						if p.series == "" && len(p.episode) > 10 && strings.HasPrefix(p.episode, "EP") && !strings.HasSuffix(p.episode, "0000") {
							p.series = p.episode[:10]
						}
					}
				}
				if row.New != nil && row.PreviouslyShown != nil {
					out.diagnostics.AdjustedProgrammes++
					row.New = nil
				}
				if row.New != nil {
					p.newEvidence = "new"
				}
				if row.PreviouslyShown != nil {
					p.newEvidence = "repeat"
				}
				if len(row.Description) > 0 {
					row.Description[0] = strings.Join(strings.Fields(row.Description[0]), " ")
				}
				if len(row.Description) > 0 && validText(row.Description[0], 4096) {
					p.description = row.Description[0]
				}
				p.facts = xmltvFacts(row.SubTitle, row.Episodes, row.Categories, row.Date, row.Ratings, row.StarRatings, row.Live != nil, row.Premiere != nil)
				p.key = channelKey + "\x00" + p.start.Format(time.RFC3339)
				if row.ID != "" {
					if validText(row.ID, 256) {
						p.key = channelKey + "\x00id:" + row.ID
						p.lineage = "provider-id"
					}
				}
				if out.spool == nil && len(out.programmes) >= programmeMemoryRows {
					if e = out.spill(); e != nil {
						return out, e
					}
					seen = nil
				}
				if out.spool == nil {
					if old, ok := seen[p.key]; ok {
						if old != p {
							out.diagnostics.SkippedProgrammes++
							out.diagnostics.note("live.import.programmeRepeated")
						}
						continue
					}
				}
				if out.programmeCount() >= limits.Programmes {
					out.diagnostics.SkippedProgrammes++
					continue
				}
				if out.spool != nil {
					kept, e := out.spool.add(p)
					if e != nil {
						return out, e
					}
					if !kept {
						out.diagnostics.SkippedProgrammes++
					}
				} else {
					seen[p.key] = p
					out.programmes = append(out.programmes, p)
				}
			}
		case xml.EndElement:
			depth--
			if depth == 0 {
				closed = true
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return out, ErrInvalid
			}
		}
	}
	if in.guideFile != "" && hex.EncodeToString(digest.Sum(nil)) != in.guideDigest {
		return out, ErrConflict
	}
	if !root || !closed || depth != 0 {
		return out, ErrInvalid
	}
	if out.spool != nil {
		if e := out.normaliseSpool(in.guideDays); e != nil {
			return out, e
		}
		applyConfiguration(&out, in.configuration, 0)
		return out, nil
	}
	sort.Slice(out.programmes, func(i, j int) bool {
		a, b := out.programmes[i], out.programmes[j]
		if a.channel != b.channel {
			return a.channel < b.channel
		}
		return a.start.Before(b.start)
	})
	kept := out.programmes[:0]
	for i, p := range out.programmes {
		if p.end.IsZero() {
			if i+1 < len(out.programmes) && out.programmes[i+1].channel == p.channel && out.programmes[i+1].start.After(p.start) && out.programmes[i+1].start.Sub(p.start) <= 7*24*time.Hour {
				p.end = out.programmes[i+1].start
				out.diagnostics.AdjustedProgrammes++
			} else {
				out.diagnostics.SkippedProgrammes++
				out.diagnostics.note("live.import.programmeEndMissing")
				continue
			}
		}
		if len(kept) > 0 {
			a := &kept[len(kept)-1]
			if a.channel == p.channel && a.end.After(p.start) {
				if !p.start.After(a.start) {
					out.diagnostics.SkippedProgrammes++
					continue
				}
				a.end = p.start
				out.diagnostics.AdjustedProgrammes++
			}
		}
		kept = append(kept, p)
	}
	out.programmes = kept
	applyConfiguration(&out, in.configuration, in.guideDays)
	return out, nil
}
func PreviewSource(in SourceInput) (Preview, error) {
	data, e := parse(in)
	if e != nil {
		return Preview{}, e
	}
	defer data.close()
	return previewParsed(data)
}
func previewParsed(data parsedSource) (Preview, error) {
	p := Preview{Diagnostics: data.diagnostics, Channels: len(data.channels), Programmes: data.programmeCount(), Names: []string{}}
	for _, c := range data.channels[:min(100, len(data.channels))] {
		p.Names = append(p.Names, c.name)
	}
	ranges, e := data.ranges()
	if e != nil {
		return p, e
	}
	for _, v := range ranges {
		if p.AvailableStart == "" || v[0] < p.AvailableStart {
			p.AvailableStart = v[0]
		}
		if v[1] > p.AvailableEnd {
			p.AvailableEnd = v[1]
		}
	}
	return p, nil
}

// harmlessGuideDoctype reports whether a directive is the plain document type
// declaration standard XMLTV files carry: <!DOCTYPE tv>, optionally naming the
// DTD with SYSTEM "…" or PUBLIC "…" "…". It declares nothing. The named DTD is
// never fetched or read. Anything with an internal subset, which is where
// entities are declared, and every other directive is refused; entity
// references themselves are refused by the decoder, which expands none.
func harmlessGuideDoctype(directive xml.Directive) bool {
	if len(directive) > 512 {
		return false
	}
	rest, ok := strings.CutPrefix(string(directive), "DOCTYPE")
	if !ok {
		return false
	}
	space := func() bool {
		trimmed := strings.TrimLeft(rest, " \t\r\n")
		skipped := len(trimmed) != len(rest)
		rest = trimmed
		return skipped
	}
	literal := func() bool {
		if rest == "" || rest[0] != '"' && rest[0] != '\'' {
			return false
		}
		end := strings.IndexByte(rest[1:], rest[0])
		if end < 0 || strings.ContainsAny(rest[1:1+end], "<>[]%&") {
			return false
		}
		rest = rest[end+2:]
		return true
	}
	if !space() {
		return false
	}
	if rest, ok = strings.CutPrefix(rest, "tv"); !ok {
		return false
	}
	if !space() {
		return rest == ""
	}
	switch {
	case rest == "":
		return true
	case strings.HasPrefix(rest, "SYSTEM"):
		rest = rest[len("SYSTEM"):]
		if !space() || !literal() {
			return false
		}
	case strings.HasPrefix(rest, "PUBLIC"):
		rest = rest[len("PUBLIC"):]
		if !space() || !literal() || !space() || !literal() {
			return false
		}
	default:
		return false
	}
	space()
	return rest == ""
}

// parseXMLTVTime accepts the precision XMLTV declares and treats an omitted zone as UTC.
func parseXMLTVTime(raw string) (time.Time, error) {
	parts := strings.Fields(raw)
	if len(parts) < 1 || len(parts) > 2 {
		return time.Time{}, ErrInvalid
	}
	stamp := parts[0]
	if len(stamp) < 4 || len(stamp) > 14 || len(stamp)%2 != 0 {
		return time.Time{}, ErrInvalid
	}
	for _, r := range stamp {
		if r < '0' || r > '9' {
			return time.Time{}, ErrInvalid
		}
	}
	defaults := "00000101000000"
	stamp += defaults[len(stamp):]
	zone := "+0000"
	if len(parts) == 2 {
		zone = parts[1]
	}
	return time.Parse("20060102150405 -0700", stamp+" "+zone)
}

// guideTokens enforces bounds while programmes and unknown elements are decoded,
// without a second whole-document pass or an in-memory XML tree.
type guideTokens struct {
	decoder             *xml.Decoder
	depth, nodes, roots int
	declared            bool
	recordStart         int64
}

func (g *guideTokens) Token() (xml.Token, error) {
	before := g.decoder.InputOffset()
	t, e := g.decoder.Token()
	if e != nil {
		if e == io.EOF && (g.depth != 0 || g.roots != 1) {
			return nil, ErrInvalid
		}
		return t, e
	}
	g.nodes++
	if g.nodes > 100000000 {
		return nil, ErrInvalid
	}
	switch v := t.(type) {
	case xml.Directive:
		if g.roots != 0 || g.declared || !harmlessGuideDoctype(v) {
			return nil, ErrInvalid
		}
		g.declared = true
	case xml.StartElement:
		if g.depth == 0 {
			g.roots++
			if g.roots != 1 || v.Name.Local != "tv" {
				return nil, ErrInvalid
			}
		}
		g.depth++
		if g.depth == 2 {
			g.recordStart = before
		}
		if g.depth > 32 || len(v.Attr) > 64 {
			return nil, ErrInvalid
		}
		attrs := map[xml.Name]bool{}
		for _, a := range v.Attr {
			if attrs[a.Name] {
				return nil, ErrInvalid
			}
			attrs[a.Name] = true
		}
	case xml.EndElement:
		g.depth--
		if g.depth < 0 {
			return nil, ErrInvalid
		}
	case xml.CharData:
		if g.depth == 0 && strings.TrimSpace(string(v)) != "" {
			return nil, ErrInvalid
		}
	}
	if g.recordStart != 0 && g.decoder.InputOffset()-g.recordStart > maxGuideRecordBytes {
		return nil, ErrInvalid
	}
	if g.depth < 2 {
		g.recordStart = 0
	}
	return t, nil
}
func newGuideDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(&guideByteReader{reader: r})
	d.CharsetReader = guideCharset
	return xml.NewTokenDecoder(&guideTokens{decoder: d})
}
func validateGuideXML(raw string) error {
	d := newGuideDecoder(strings.NewReader(raw))
	for {
		_, e := d.Token()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return ErrInvalid
		}
	}
}

type latinReader struct {
	r       *bufio.Reader
	pending []byte
	windows bool
}

func (r *latinReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(r.pending) > 0 {
			k := copy(p[n:], r.pending)
			n += k
			r.pending = r.pending[k:]
			continue
		}
		b, e := r.r.ReadByte()
		if e != nil {
			if n > 0 {
				return n, nil
			}
			return 0, e
		}
		v := rune(b)
		if r.windows && b >= 128 && b < 160 {
			v = []rune("€\u0081‚ƒ„…†‡ˆ‰Š‹Œ\u008dŽ\u008f\u0090‘’“”•–—˜™š›œ\u009džŸ")[b-128]
		}
		r.pending = []byte(string(v))
	}
	return n, nil
}
func guideCharset(name string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(name) {
	case "iso-8859-1", "latin1", "latin-1":
		return &latinReader{r: bufio.NewReader(r)}, nil
	case "windows-1252", "cp1252":
		return &latinReader{r: bufio.NewReader(r), windows: true}, nil
	case "utf-8", "utf8":
		return r, nil
	}
	return nil, fmt.Errorf("unsupported guide encoding: %s", strconv.Quote(name))
}

// Discard a hostile long line in bounded fragments, then resume the next row.
func playlistLine(r *bufio.Reader) (string, bool, error) {
	var out []byte
	long := false
	for {
		part, e := r.ReadSlice('\n')
		if !long {
			if len(out)+len(part) > 16384 {
				long = true
				out = nil
			} else {
				out = append(out, part...)
			}
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		return string(out), long, e
	}
}

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
