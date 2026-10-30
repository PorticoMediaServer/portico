package metadata

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

var screenIMDbPattern = regexp.MustCompile(`(?i)[\[{]imdb(?:id)?[-=: ](tt[0-9]{7,10})[\]}]`)
var screenIDPattern = regexp.MustCompile(`(?i)[\[{](tmdb|tvdb|anilist)(?:id)?[-=: ]([1-9][0-9]{0,14})[\]}]`)

func intString(v int64) string { return strconv.FormatInt(v, 10) }

// Normalisation is for comparison only; search queries retain the owner's text.
func normalizedScreenName(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "&", " and ")
	var out strings.Builder
	for _, r := range norm.NFKD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if translit, ok := screenTransliteration[r]; ok {
			out.WriteString(translit)
		} else if unicode.IsLetter(r) || unicode.IsNumber(r) {
			out.WriteRune(r)
		} else {
			out.WriteByte(' ')
		}
	}
	words := strings.Fields(out.String())
	if len(words) > 1 && screenArticles[words[0]] {
		words = words[1:]
	}
	if len(words) > 1 && screenArticles[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	for i, word := range words {
		if word == "i" && i == len(words)-1 && len(words) > 1 {
			words[i] = "1"
			continue
		}
		if n, ok := screenRomanNumbers[word]; ok {
			words[i] = n
		}
	}
	return strings.Join(words, " ")
}

var screenArticles = map[string]bool{"the": true, "a": true, "an": true, "le": true, "la": true, "les": true, "el": true, "los": true, "las": true, "der": true, "die": true, "das": true}
var screenRomanNumbers = map[string]string{"ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6", "vii": "7", "viii": "8", "ix": "9", "x": "10", "xi": "11", "xii": "12"}

// Provider romaji/translated aliases handle scripts without a deterministic
// letter transliteration. These common Cyrillic/Greek spellings add evidence.
var screenTransliteration = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ж': "zh", 'з': "z", 'и': "i", 'й': "i", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya", 'і': "i", 'ї': "i", 'є': "ye", 'ґ': "g",
	'α': "a", 'β': "v", 'γ': "g", 'δ': "d", 'ε': "e", 'ζ': "z", 'η': "i", 'θ': "th", 'ι': "i", 'κ': "k", 'λ': "l", 'μ': "m", 'ν': "n", 'ξ': "x", 'ο': "o", 'π': "p", 'ρ': "r", 'σ': "s", 'ς': "s", 'τ': "t", 'υ': "y", 'φ': "f", 'χ': "ch", 'ψ': "ps", 'ω': "o",
}

// Search and inventory consume one name reader; no second year grammar.
func screenFilename(s string) (title string, year int, edition string) {
	r := naming.Read(s, "anime")
	title, year = r.PreferredTitle()
	if len(r.Editions) > 0 {
		edition = r.Editions[0].Value
	}
	return
}
func screenName(s string, released int) (title string, year int, edition string) {
	r := naming.Read(s, "movie")
	title, year = r.PreferredTitle()
	if released > 0 {
		year = released
		title = s
		if len(r.Titles) > 0 {
			title = r.Titles[0].Value
		}
		for _, e := range r.Years {
			if e.Value != released && e.Path == s {
				title = s
				break
			}
		}
	}
	if len(r.Editions) > 0 {
		edition = r.Editions[0].Value
	}
	return
}
func screenQueries(b screenBase) ([]string, int, []metadataprovider.ScreenID) {
	out := []string{}
	seen := map[string]bool{}
	ids := []metadataprovider.ScreenID{}
	idseen := map[metadataprovider.ScreenID]bool{}
	year := b.Year
	fileWins := false
	fileNames := map[string]bool{}
	fileProviders := map[string]bool{}
	for _, e := range b.Evidence {
		if (e.Source == string(naming.FileName) || e.Source == string(naming.EmbeddedMetadata)) && e.Confidence >= .8 {
			fileWins = true
			fileNames[normalizedScreenName(e.Title)] = true
			for _, id := range e.IDs {
				fileProviders[id.Provider] = true
			}
		}
	}
	for _, evidence := range b.Evidence[:min(len(b.Evidence), 16)] {
		if year == 0 && evidence.Year > 0 {
			year = evidence.Year
		}
	}
	add := func(s string) {
		s = strings.TrimSpace(s)
		key := normalizedScreenName(s)
		if len(s) > 0 && len(s) <= 512 && screenQuerySafe(s) && !seen[key] && len(out) < 4 {
			out = append(out, s)
			seen[key] = true
		}
	}
	addID := func(id metadataprovider.ScreenID) {
		if metadataprovider.ValidScreenID(id) && !idseen[id] && len(ids) < 16 {
			ids = append(ids, id)
			idseen[id] = true
		}
	}
	if b.Query != "" {
		if hint, ok := metadataprovider.ParseScreenHint(b.Query, b.ItemKind); ok {
			addID(hint)
		} else {
			add(b.Query)
		}
		return out, year, ids
	}
	for _, evidence := range b.Evidence[:min(len(b.Evidence), 16)] {
		if !fileWins || (evidence.Source == string(naming.FileName) || evidence.Source == string(naming.EmbeddedMetadata)) || fileNames[normalizedScreenName(evidence.Title)] {
			add(evidence.Title)
		}
		for _, id := range evidence.IDs {
			if evidence.Source != string(naming.FileName) && evidence.Source != string(naming.EmbeddedMetadata) && fileWins && (fileProviders[id.Provider] || !fileNames[normalizedScreenName(evidence.Title)]) {
				continue
			}
			addID(id)
		}
	}
	for _, id := range b.Existing {
		addID(id)
	}
	for _, n := range b.NFO {
		add(n.Title)
		add(n.OriginalTitle)
		if n.Year > 0 {
			if year == 0 {
				year = n.Year
			}
		}
		for _, id := range n.IDs {
			addID(id)
		}
	}
	for i, name := range append([]string{b.Title}, b.Names...) {
		released := 0
		if i == 0 {
			released = b.Year
		}
		title, y, _ := screenName(name, released)
		if year == 0 && y > 0 {
			year = y
		}
		add(title)
		for _, m := range screenIMDbPattern.FindAllStringSubmatch(name, 8) {
			addID(metadataprovider.ScreenID{Provider: "imdb", Type: "title", ID: strings.ToLower(m[1])})
		}
		for _, m := range screenIDPattern.FindAllStringSubmatch(name, 8) {
			kind := b.ItemKind
			if kind == "episode" {
				continue
			}
			if strings.EqualFold(m[1], "anilist") {
				kind = "anime"
			}
			addID(metadataprovider.ScreenID{Provider: strings.ToLower(m[1]), Type: kind, ID: m[2]})
		}
	}
	return out, year, ids
}

type screenCandidate struct {
	DetailsVerified bool                          `json:"-"`
	Key             string                        `json:"key"`
	Record          metadataprovider.ScreenRecord `json:"record"`
	Confidence      float64                       `json:"confidence"`
	Reasons         []string                      `json:"reasons"`
	StrongSignals   int                           `json:"strongSignals"`
	Contradiction   bool                          `json:"contradiction"`
	SourceKind      string                        `json:"sourceKind"`
	ObservedAt      string                        `json:"observedAt"`
	SourceURL       string                        `json:"sourceUrl"`
	Attribution     string                        `json:"attribution"`
	InputDigest     string                        `json:"-"`
	QueryDigest     string                        `json:"-"`
}

func nameSimilarity(a, b string) float64 {
	if a == b && a != "" {
		return 1
	}
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 || len(br) == 0 || len(ar) > 512 || len(br) > 512 {
		return 0
	}
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, x := range ar {
		cur := make([]int, len(br)+1)
		cur[0] = i + 1
		for j, y := range br {
			cost := 1
			if x == y {
				cost = 0
			}
			cur[j+1] = min(cur[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev = cur
	}
	return 1 - float64(prev[len(br)])/float64(max(len(ar), len(br)))
}
func scoreScreen(r metadataprovider.ScreenRecord, names []string, year int, exact bool) screenCandidate {
	c := screenCandidate{Record: r, Reasons: []string{}, SourceKind: "provider_query", SourceURL: metadataprovider.ScreenSourceURL(r.Identity), Attribution: metadataprovider.ScreenAttribution(r.Identity.Provider)}
	if exact {
		c.DetailsVerified = true
		c.Confidence = 1
		c.StrongSignals = 2
		c.Reasons = []string{"validated_typed_identifier"}
		c.SourceKind = "typed_identifier"
		return c
	}
	best := 0.0
	for _, q := range names {
		for _, name := range append([]string{r.Title, r.OriginalTitle}, r.Aliases...) {
			best = math.Max(best, nameSimilarity(normalizedScreenName(q), normalizedScreenName(name)))
		}
	}
	if best == 1 {
		c.Confidence = .88
		c.StrongSignals++
		c.Reasons = append(c.Reasons, "exact_title_or_alias")
	} else if best >= .8 {
		c.Confidence = .78 + .5*(best-.8)
		c.StrongSignals++
		c.Reasons = append(c.Reasons, "close_title_or_alias")
	} else {
		c.Confidence = .5 * best
		c.Reasons = append(c.Reasons, "weak_title_similarity")
	}
	if year > 0 && r.Year > 0 {
		if year == r.Year {
			c.Confidence += .10
			c.StrongSignals++
			c.Reasons = append(c.Reasons, "release_year_agrees")
		} else if abs(year-r.Year) == 1 {
			c.Confidence += .05
			c.Reasons = append(c.Reasons, "release_year_near")
		} else {
			c.Confidence -= .25
			c.Contradiction = true
			c.Reasons = append(c.Reasons, "release_year_conflicts")
		}
	} else {
		c.Reasons = append(c.Reasons, "release_year_unknown")
	}
	c.Confidence = math.Round(min(.99, max(0, c.Confidence))*1000) / 1000
	return c
}
func sameScreenIdentity(a, b metadataprovider.ScreenRecord) bool {
	if a.Identity == b.Identity {
		return true
	}
	for _, id := range a.Crosswalk {
		if id == b.Identity {
			return true
		}
	}
	for _, id := range b.Crosswalk {
		if id == a.Identity {
			return true
		}
	}
	return false
}
func screenWinner(cs []screenCandidate) (int, string) {
	if len(cs) == 0 {
		return -1, "unmatched"
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Confidence == cs[j].Confidence {
			return screenDigest(cs[i].Record.Identity) < screenDigest(cs[j].Record.Identity)
		}
		return cs[i].Confidence > cs[j].Confidence
	})
	top := cs[0]
	if !top.DetailsVerified || top.Contradiction || top.StrongSignals < 1 || top.Confidence < .78 {
		return -1, "needs_selection"
	}
	for _, r := range cs[1:] {
		if !sameScreenIdentity(top.Record, r.Record) && top.Confidence-r.Confidence < .07 {
			return -1, "needs_selection"
		}
	}
	return 0, "matched"
}

func screenQuerySafe(s string) bool {
	if strings.Contains(s, "://") || strings.ContainsAny(s, "\\\r\n\x00") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// A remux/edition's many sidecars must agree; a first-row year is not authority.
func screenLocalYearConflict(b screenBase) bool {
	years := map[int]bool{}
	if b.Year > 0 {
		years[b.Year] = true
	}
	for _, n := range b.NFO {
		if n.Year > 0 {
			years[n.Year] = true
		}
	}
	for i, name := range append([]string{b.Title}, b.Names...) {
		released := 0
		if i == 0 {
			released = b.Year
		}
		_, y, _ := screenName(name, released)
		if y > 0 {
			years[y] = true
		}
	}
	return len(years) > 1
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// MatchEvidence is the bounded input naming can supply: prefer file/embedded
// titles, then folder titles. Provider IDs are exact, editions are descriptive.
type MatchEvidence struct {
	Confidence float64                     `json:"confidence"`
	Title      string                      `json:"title"`
	Source     string                      `json:"source"`
	Year       int                         `json:"year,omitempty"`
	IDs        []metadataprovider.ScreenID `json:"ids,omitempty"`
	Edition    string                      `json:"edition,omitempty"`
}

func scoreScreenEvidence(r metadataprovider.ScreenRecord, names []string, year int, exact bool, b screenBase) screenCandidate {
	c := scoreScreen(r, names, year, exact)
	if exact {
		return c
	}
	expected := b.ItemKind
	if r.Identity.Type == expected || b.LibraryKind == "anime" && r.Identity.Type == "anime" {
		c.Reasons = append(c.Reasons, "media_kind_agrees")
	} else {
		c.Contradiction = true
		c.Reasons = append(c.Reasons, "media_kind_conflicts")
	}
	if b.RuntimeMinutes > 0 && r.AdvisoryRuntimeMinutes != nil && *r.AdvisoryRuntimeMinutes > 0 {
		difference := math.Abs(b.RuntimeMinutes - float64(*r.AdvisoryRuntimeMinutes))
		if difference <= max(5, b.RuntimeMinutes*.08) {
			c.Confidence = min(.99, c.Confidence+.06)
			c.Reasons = append(c.Reasons, "runtime_agrees")
		} else if difference > max(20, b.RuntimeMinutes*.3) {
			if b.ItemKind == "movie" {
				c.Confidence = max(0, c.Confidence-.12)
			}
			c.Reasons = append(c.Reasons, "runtime_differs")
		}
	}
	c.Confidence = math.Round(c.Confidence*1000) / 1000
	return c
}

func screenSearchSafe(query string) bool {
	if _, ok := metadataprovider.ParseScreenHint(query, "movie"); ok {
		return true
	}
	return screenQuerySafe(query)
}
