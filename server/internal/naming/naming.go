// Package naming reads library-relative paths without touching files, metadata,
// providers or the catalogue. Confidence is a heuristic strength, not a match
// probability. Consumers retain alternatives until provider or tag evidence is
// available. Paths may use either separator on every host.
package naming

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	MaxPathBytes    = 16384
	MaxDepth        = 128
	MaxEpisodeLinks = 8
)

type Source string

const (
	FileName          Source = "file_name"
	DirectoryName     Source = "folder_name"
	ParentFolder      Source = "parent_folder"
	GrandparentFolder Source = "grandparent_folder"
	AncestorFolder    Source = "ancestor_folder"
)

type Evidence[T any] struct {
	Value  T
	Source Source
	// Path is the relative path of the component that supplied the evidence.
	Path       string
	Confidence float64
}

type ProviderID struct{ Provider, ID string }
type Extra struct{ Kind, ParentPath string }
type Disc struct{ Format, ParentPath string }
type Reading struct {
	Titles          []Evidence[string]
	Years           []Evidence[int]
	Seasons         []Evidence[int]
	Episodes        []Evidence[[]int]
	AbsoluteNumbers []Evidence[int]
	AirDates        []Evidence[string]
	Parts           []Evidence[int]
	Editions        []Evidence[string]
	Versions        []Evidence[string]
	ProviderIDs     []Evidence[ProviderID]
	Extras          []Evidence[Extra]
	Discs           []Evidence[Disc]
	TrackNumbers    []Evidence[int]
	DiscNumbers     []Evidence[int]
	Artists         []Evidence[string]
	Albums          []Evidence[string]
	// Diagnostics describe only evidence that was skipped. Other fields remain
	// usable. An invalid range never silently becomes its first episode.
	Diagnostics []string
}

func ev[T any](value T, src Source, p string, confidence float64) Evidence[T] {
	return Evidence[T]{value, src, p, confidence}
}
func slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }
func source(distance int) Source {
	switch distance {
	case 0:
		return FileName
	case 1:
		return ParentFolder
	case 2:
		return GrandparentFolder
	default:
		return AncestorFolder
	}
}
func clean(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = strings.TrimSpace(strings.Trim(s, " -"))
	for _, pair := range []string{"[]", "()", "{}"} {
		if strings.HasPrefix(s, pair[:1]) && !strings.Contains(s, pair[1:]) {
			s = strings.TrimPrefix(s, pair[:1])
		}
		if strings.HasSuffix(s, pair[1:]) && !strings.Contains(s, pair[:1]) {
			s = strings.TrimSuffix(s, pair[1:])
		}
	}
	return norm.NFC.String(strings.Join(strings.Fields(s), " "))
}
func same(a, b string) bool {
	key := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, norm.NFC.String(s))
	}
	return key(a) == key(b)
}

var idToken = regexp.MustCompile(`(?i)\{(imdb|tmdb|tvdb|anilist)(?:id)?[-=: ](tt[0-9]{5,12}|[1-9][0-9]{0,14})\}|\[(imdb|tmdb|tvdb|anilist)(?:id)?[-=: ](tt[0-9]{5,12}|[1-9][0-9]{0,14})\]`)
var imdbToken = regexp.MustCompile(`(?i)\btt[0-9]{5,12}\b`)
var editionToken = regexp.MustCompile(`(?i)\{edition-([^}]{1,128})\}|\b(director'?s[ ._-]+cut|extended(?:[ ._-]+(?:cut|edition))?|theatrical[ ._-]+(?:cut|edition)|unrated|remastered|special[ ._-]+edition|final[ ._-]+cut)\b`)
var technicalToken = regexp.MustCompile(`(?i)(?:^|[ ._\[(-])(?:[0-9]{3,4}[pi]|[248]k|[0-9]{3,4}x[0-9]{3,4}|blu[ ._-]?ray|bd[ ._-]?rip|br[ ._-]?rip|web[ ._-]?(?:dl|rip)|hdtv|pdtv|dvd[ ._-]?rip|remux|x26[45]|h[ ._-]?26[45]|hevc|avc|av1|xvid|divx|aac|ac3|eac3|dts(?:hd)?|truehd|ddp?|hdr10?\+?|sdr|dolby[ ._-]?vision|dv|atmos|proper|repack|multi|dual[ ._-]?audio|10[ ._-]?bit|8[ ._-]?bit)(?:\b|_)`)
var hashToken = regexp.MustCompile(`(?i)\[[a-f0-9]{8,64}\]`)
var bracketYear = regexp.MustCompile(`[\[(]((?:18|19|20)[0-9]{2})(?:-[0-9]{4})?[\])]`)
var bareYear = regexp.MustCompile(`((?:18|19|20)[0-9]{2})`)
var partToken = regexp.MustCompile(`(?i)(?:^|[ ._-])(?:pt|part|cd|disc|disk|dvd)[ ._-]*([0-9]{1,3})$`)
var genericName = regexp.MustCompile(`(?i)^(?:movie|film|video|title|untitled|file|main[ ._-]?feature|vts[ ._0-9]+|[0-9]+|[a-f0-9]{8,64})$`)
var seasonToken = regexp.MustCompile(`(?i)^(?:season|series|saison|staffel|temporada|s)[ ._-]*([0-9]{1,4})(?:[ ._-]*\([0-9]{4}\))?$`)
var discToken = regexp.MustCompile(`(?i)^(?:disc|disk|cd|vol|volume|part|act|digital[ ._-]+media)[ ._-]*([0-9]{1,3})(?:[ ._()-]+.*)?$`)

func SeasonFolder(s string) (int, bool) {
	if strings.EqualFold(strings.TrimSpace(s), "specials") {
		return 0, true
	}
	m := seasonToken.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}
func DiscFolder(s string) (int, bool) {
	m := discToken.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, n > 0
}

// Read returns evidence ordered from file to increasingly distant folders.
// kind is movie, tv, anime, music or audiobook. It is necessary context for bare
// episode numbers; "01" in an ordinary movie library is not an episode.
func Read(relative, kind string) Reading {
	var r Reading
	if len(relative) > MaxPathBytes || strings.IndexByte(relative, 0) >= 0 {
		r.Diagnostics = append(r.Diagnostics, "path_budget_exceeded")
		return r
	}
	p := path.Clean(slash(relative))
	parts := strings.Split(p, "/")
	if len(parts) > MaxDepth {
		r.Diagnostics = append(r.Diagnostics, "path_depth_exceeded")
		return r
	}
	if p == "." || p == "/" || p == ".." || strings.HasPrefix(p, "../") {
		r.Diagnostics = append(r.Diagnostics, "invalid_relative_path")
		return r
	}
	// Absolute paths are accepted by legacy display adapters, but callers doing
	// identification must supply library-relative paths to avoid root evidence.
	name := strings.TrimSuffix(parts[len(parts)-1], path.Ext(parts[len(parts)-1]))
	episodic := kind == "tv" || kind == "anime"
	folderSeason := -1
	showIndex := -1
	for i := len(parts) - 2; i >= 0; i-- {
		component := parts[i]
		src := source(len(parts) - 1 - i)
		cp := strings.Join(parts[:i+1], "/")
		if component == "" {
			continue
		}
		if s, ok := SeasonFolder(component); episodic && ok {
			r.Seasons = append(r.Seasons, ev(s, src, cp, .9))
			if folderSeason < 0 {
				folderSeason = s
			}
			continue
		}
		if n, ok := DiscFolder(component); ok {
			r.DiscNumbers = append(r.DiscNumbers, ev(n, src, cp, .9))
			continue
		}
		if strings.EqualFold(component, "BDMV") || strings.EqualFold(component, "VIDEO_TS") {
			r.Discs = append(r.Discs, ev(Disc{strings.ToUpper(component), strings.Join(parts[:i], "/")}, src, cp, 1))
			continue
		}
		if (strings.Contains(strings.ToUpper(p), "/BDMV/") || strings.Contains(strings.ToUpper(p), "/VIDEO_TS/")) && (strings.EqualFold(component, "STREAM") || strings.EqualFold(component, "BACKUP")) {
			continue
		}
		if _, ok := extraFolder(component, episodic); ok && i > 0 {
			continue
		}
		if showIndex < 0 {
			showIndex = i
		}
	}
	titleInput := name
	if strings.EqualFold(name, "BDMV") || strings.EqualFold(name, "VIDEO_TS") {
		r.Discs = DiscLayout(p)
		titleInput = ""
	}
	if episodic {
		titleInput = readEpisode(&r, name, p, kind, folderSeason, showIndex >= 0)
	}
	if kind == "music" || kind == "audiobook" {
		titleInput = readAudio(&r, name, p, parts, kind)
	}
	readComponent(&r, titleInput, name, FileName, p, .9)
	// Read the nearest title folder, plus one farther candidate for badly named
	// files. Never let category folders displace usable file evidence.
	count := 0
	for i := showIndex; i >= 0 && count < 2; i-- {
		if _, ok := SeasonFolder(parts[i]); ok {
			continue
		}
		if _, ok := DiscFolder(parts[i]); ok {
			continue
		}
		if parts[i] == "" {
			continue
		}
		cp := strings.Join(parts[:i+1], "/")
		strength := .8
		if count > 0 {
			strength = .5
		}
		readComponent(&r, parts[i], parts[i], source(len(parts)-1-i), cp, strength)
		count++
	}
	if kind == "movie" {
		readVersionLabel(&r, name, p)
	}
	r.Extras = ReadExtras(p, kind)
	return r
}

func readComponent(r *Reading, input, raw string, src Source, p string, strength float64) {
	for _, m := range idToken.FindAllStringSubmatch(raw, 16) {
		provider, id := m[1], m[2]
		if provider == "" {
			provider, id = m[3], m[4]
		}
		provider, id = strings.ToLower(provider), strings.ToLower(id)
		if provider == "imdb" && !strings.HasPrefix(id, "tt") || provider != "imdb" && strings.HasPrefix(id, "tt") {
			continue
		}
		r.ProviderIDs = append(r.ProviderIDs, ev(ProviderID{provider, id}, src, p, .99))
	}
	for _, id := range imdbToken.FindAllString(raw, 16) {
		found := false
		for _, e := range r.ProviderIDs {
			if e.Path == p && e.Value.ID == strings.ToLower(id) {
				found = true
			}
		}
		if !found {
			r.ProviderIDs = append(r.ProviderIDs, ev(ProviderID{"imdb", strings.ToLower(id)}, src, p, .95))
		}
	}
	for _, m := range editionToken.FindAllStringSubmatch(raw, 8) {
		edition := m[1]
		if edition == "" {
			edition = m[2]
		}
		if label := clean(edition); label != "" {
			r.Editions = append(r.Editions, ev(label, src, p, .95))
		}
	}
	hasPart := false
	partInput := raw
	if loc := technicalToken.FindStringIndex(partInput); loc != nil {
		partInput = strings.TrimRight(partInput[:loc[0]], " ._-")
	}
	if m := partToken.FindStringSubmatch(partInput); m != nil && isPartSuffix(partInput) {
		n, _ := strconv.Atoi(m[1])
		if n > 0 {
			hasPart = true
			r.Parts = append(r.Parts, ev(n, src, p, .9))
		}
	}
	s := idToken.ReplaceAllString(input, " ")
	s = imdbToken.ReplaceAllString(s, " ")

	s = hashToken.ReplaceAllString(s, " ")
	// The explicit year nearest the title wins over later encode dates. A bare
	// year before that bracket is title text, unless release junk separates it.
	cut, year, yearStrength := -1, 0, 0.0
	tech := technicalToken.FindStringIndex(s)
	if m := bracketYear.FindStringSubmatchIndex(s); m != nil && (tech == nil || m[0] < tech[0]) {
		cut = m[0]
		year, _ = strconv.Atoi(s[m[2]:m[3]])
		yearStrength = .98
	} else {
		var matches [][]int
		for _, m := range bareYear.FindAllStringSubmatchIndex(s, -1) {
			start, end := m[2], m[3]
			if start == 0 || !boundary(s, start-1) || !boundary(s, end) {
				continue
			}
			if end < len(s) && (s[end] == '-' || s[end] == '.') && dateToken.MatchString(s[max(0, start-1):]) {
				continue
			}
			if tech != nil && start > tech[0] {
				continue
			}
			matches = append(matches, m)
		}
		for i, m := range matches {
			start, end := m[2], m[3]
			if i+1 < len(matches) && clean(s[end:matches[i+1][2]]) == "" && !strings.Contains(s[end:matches[i+1][2]], "-") {
				continue
			}
			after := strings.Trim(s[end:], " ._-")
			strong := tech != nil || editionToken.MatchString(after) || strings.Contains(s[max(0, start-3):start], " - ") || i > 0 && after == "" && !strings.Contains(s[matches[i-1][3]:start], "-")
			if strong {
				cut = start
				year, _ = strconv.Atoi(s[start:end])
				yearStrength = .85
				break
			} else if after == "" {
				year, _ = strconv.Atoi(s[start:end])
				yearStrength = .45
			}
		}
	}
	if year > 0 {
		r.Years = append(r.Years, ev(year, src, p, yearStrength))
	}

	alternate := ""
	if cut >= 0 && yearStrength == .85 {
		alternate = s
		if loc := technicalToken.FindStringIndex(alternate); loc != nil {
			alternate = alternate[:loc[0]]
		}
		alternate = editionToken.ReplaceAllString(alternate, " ")
		if hasPart {
			alternate = partToken.ReplaceAllString(alternate, "")
		}
		alternate = clean(alternate)
	}
	if cut >= 0 {
		s = s[:cut]
	}
	if loc := technicalToken.FindStringIndex(s); loc != nil {
		if src == FileName {
			r.Versions = append(r.Versions, ev(clean(s[loc[0]:]), src, p, .85))
		}
		s = s[:loc[0]]
	} else if src == FileName && cut >= 0 {
		if loc := technicalToken.FindStringIndex(input); loc != nil {
			r.Versions = append(r.Versions, ev(clean(input[loc[0]:]), src, p, .85))
		}
	}
	s = editionToken.ReplaceAllString(s, " ")
	if hasPart {
		s = partToken.ReplaceAllString(s, "")
	}
	for _, suffix := range extraSuffixes {
		if strings.HasSuffix(strings.ToLower(s), suffix.suffix) {
			s = s[:len(s)-len(suffix.suffix)]
			break
		}
	}
	title := clean(s)
	if title != "" {
		if genericName.MatchString(title) && yearStrength < .6 && !idToken.MatchString(raw) && !imdbToken.MatchString(raw) && !(len(title) == 4 && bareYear.MatchString(title)) {
			strength = .2
		}
		r.Titles = append(r.Titles, ev(title, src, p, strength))
	}

	if alternate != "" && alternate != title {
		r.Titles = append(r.Titles, ev(alternate, src, p, .65))
	}
	// A low-confidence stripped alternative helps matching without mutilating
	// titles such as Blade Runner 2049, Space 1999 or Summer of 1984.
	if cut < 0 && yearStrength == .45 {
		m := bareYear.FindStringSubmatchIndex(s)
		if m != nil && m[2] > 0 {
			if t := clean(s[:m[0]]); t != "" {
				r.Titles = append(r.Titles, ev(t, src, p, .4))
			}
		}
	}
}

// PreferredTitle is the minimal display adapter for existing callers. A
// matcher should inspect all evidence. A confident file wins every folder
// conflict; generic files fall back to a meaningful folder.
func (r Reading) PreferredTitle() (string, int) {
	var title Evidence[string]
	for _, e := range r.Titles {
		if title.Value == "" || e.Confidence > title.Confidence {
			title = e
		}
		if e.Source == FileName && e.Confidence >= .8 {
			break
		}
	}
	year := 0
	for _, e := range r.Years {
		if e.Confidence < .6 {
			continue
		}
		if e.Source == title.Source {
			return title.Value, e.Value
		}
		if e.Path == title.Path {
			year = e.Value
			break
		}
		for _, t := range r.Titles {
			if t.Path == e.Path && same(t.Value, title.Value) {
				year = e.Value
				break
			}
		}
		if year != 0 {
			break
		}
	}
	return title.Value, year
}

// A descriptive episode title ending in "Part 1" is title text. A standalone
// stack suffix, or one separated by a dash after that title, is a part hint.
func isPartSuffix(raw string) bool {
	loc := partToken.FindStringIndex(raw)
	if loc == nil {
		return false
	}
	markers := episodeToken.FindAllStringIndex(raw, 32)
	for _, marker := range markers {
		if validMarker(raw, marker) && marker[1] <= loc[0] {
			prefix := strings.TrimSpace(raw[marker[1]:loc[0]])
			return clean(prefix) == "" || strings.HasSuffix(prefix, "-")
		}
	}
	return true
}

// Named versions need folder corroboration when there is no explicit year;
// otherwise a hyphen is as likely to be part of the real film title.
func readVersionLabel(r *Reading, name, p string) {
	if len(r.Versions) > 0 {
		return
	}
	at := strings.LastIndex(name, " - ")
	if at < 0 {
		return
	}
	label := strings.TrimSpace(name[at+3:])
	if label == "" || partToken.MatchString(label) || editionToken.MatchString(label) {
		return
	}
	for _, suffix := range extraSuffixes {
		if strings.HasSuffix(strings.ToLower(name), suffix.suffix) {
			return
		}
	}
	var prefix Reading
	readComponent(&prefix, name[:at], name[:at], FileName, p, .9)
	title, year := prefix.PreferredTitle()
	supported := year > 0
	for _, e := range r.Titles {
		if e.Source != FileName && same(title, e.Value) {
			supported = true
		}
	}
	if !supported {
		return
	}
	r.Versions = append(r.Versions, ev(clean(label), FileName, p, .8))
	for i := range r.Titles {
		if r.Titles[i].Source == FileName && r.Titles[i].Path == p && same(r.Titles[i].Value, clean(name)) {
			r.Titles[i].Value = title
		}
	}
}

// DiscLayout identifies the disc root even when the input is the directory
// itself. The identity lane uses ParentPath to group its streams, never each
// individual VOB/M2TS as an independent film.
func DiscLayout(relative string) []Evidence[Disc] {
	if len(relative) > MaxPathBytes || strings.IndexByte(relative, 0) >= 0 {
		return nil
	}
	p := path.Clean(slash(relative))
	parts := strings.Split(p, "/")
	if len(parts) > MaxDepth || p == ".." || strings.HasPrefix(p, "../") {
		return nil
	}
	for i, part := range parts {
		if strings.EqualFold(part, "BDMV") || strings.EqualFold(part, "VIDEO_TS") {
			src := source(len(parts) - 1 - i)
			if i == len(parts)-1 {
				src = DirectoryName
			}
			return []Evidence[Disc]{ev(Disc{strings.ToUpper(part), strings.Join(parts[:i], "/")}, src, strings.Join(parts[:i+1], "/"), 1)}
		}
	}
	return nil
}
