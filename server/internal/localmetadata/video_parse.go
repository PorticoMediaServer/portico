package localmetadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"strconv"
	"strings"
)

type nfoNode struct {
	name, text string
	attrs      map[string]string
	children   []*nfoNode
}

func (n *nfoNode) values(name string) []*nfoNode {
	out := []*nfoNode{}
	for _, c := range n.children {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}
func (n *nfoNode) value(name string) string {
	for _, c := range n.children {
		if c.name == name {
			return strings.TrimSpace(c.text)
		}
	}
	return ""
}
func nfoNumber(s string, max int) (*int, error) {
	if s == "" {
		return nil, nil
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 0 || n > max {
		return nil, errors.New("invalid NFO number")
	}
	return &n, nil
}
func ParseVideoNFO(raw []byte, anime bool) ([]assets.VideoNFO, error) {
	if len(raw) == 0 || len(raw) > 256<<10 {
		return nil, errors.New("NFO exceeds budget")
	}
	if hint, ok := metadataprovider.ParseScreenHint(strings.TrimSpace(string(raw)), "movie"); ok {
		kind := map[string]string{"movie": "movie", "show": "show", "anime": "show", "title": "work"}[hint.Type]
		if kind != "" {
			sum := sha256.Sum256(raw)
			return []assets.VideoNFO{{Kind: kind, Digest: hex.EncodeToString(sum[:]), IDs: []metadataprovider.ScreenID{hint}}}, nil
		}
	}
	// Kodi permits a trailing scraper URL and older files contain bare ampersands.
	// Escape only bare ampersands; directives and external entities remain forbidden.
	xmlRaw := escapeNFOAmpersands(raw)
	d := xml.NewDecoder(bytes.NewReader(xmlRaw))
	d.Strict = true
	d.CharsetReader = localXMLCharset
	stack := []*nfoNode{}
	roots := []*nfoNode{}
	nodes := 0
	outside := ""
	for {
		token, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, errors.New("invalid NFO XML")
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil, errors.New("NFO declarations are not allowed")
		case xml.ProcInst:
			if t.Target != "xml" || nodes != 0 {
				return nil, errors.New("NFO processing instruction is not allowed")
			}
		case xml.StartElement:
			nodes++
			if nodes > 4096 || len(stack) >= 16 || len(t.Attr) > 16 {
				return nil, errors.New("NFO structure exceeds budget")
			}
			n := &nfoNode{name: strings.ToLower(t.Name.Local), attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[strings.ToLower(a.Name.Local)] = a.Value
			}
			if len(stack) == 0 {
				roots = append(roots, n)
				if len(roots) > 8 {
					return nil, errors.New("too many NFO records")
				}
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("invalid NFO nesting")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				n := stack[len(stack)-1]
				n.text += string(t)
				if len(n.text) > 65536 {
					return nil, errors.New("NFO field exceeds budget")
				}
			} else if strings.TrimSpace(string(t)) != "" && len(outside)+len(t) <= 2048 {
				outside += string(t)
			}
		}
	}
	if len(stack) != 0 || len(roots) == 0 {
		return nil, errors.New("incomplete NFO")
	}
	out := []assets.VideoNFO{}
	sum := sha256.Sum256(raw)
	for _, n := range roots {
		kind := map[string]string{"movie": "movie", "tvshow": "show", "episodedetails": "episode"}[n.name]
		if kind == "" {
			continue
		}
		v := assets.VideoNFO{Kind: kind, Digest: hex.EncodeToString(sum[:]), Title: n.value("title"), OriginalTitle: n.value("originaltitle"), SortTitle: n.value("sorttitle"), Overview: n.value("plot"), Tagline: n.value("tagline"), Date: n.value("premiered"), Edition: n.value("edition"), Certification: n.value("mpaa")}
		if v.Overview == "" {
			v.Overview = n.value("outline")
		}
		if v.Date == "" {
			v.Date = n.value("aired")
		}
		if v.Edition == "" {
			v.Edition = n.value("editiontitle")
		}
		year, e := nfoNumber(n.value("year"), 9999)
		if e != nil {
			v.Issues = append(v.Issues, "invalid_nfo_year")
		}
		if year != nil {
			v.Year = *year
		}
		if v.Season, e = nfoNumber(nfoNumericText(n.value("season")), 9999); e != nil {
			v.Issues = append(v.Issues, "invalid_nfo_season")
		}
		if v.Episode, e = nfoNumber(nfoNumericText(n.value("episode")), 1000000); e != nil {
			v.Issues = append(v.Issues, "invalid_nfo_episode")
		}
		if v.Absolute, e = nfoNumber(nfoNumericText(n.value("absolute_number")), 1000000); e != nil {
			v.Issues = append(v.Issues, "invalid_nfo_absolute_number")
		}
		if v.AdvisoryRuntimeMinutes, e = nfoNumber(nfoNumericText(n.value("runtime")), 10080); e != nil {
			v.Issues = append(v.Issues, "invalid_nfo_runtime")
		}
		if len(v.Title) > 2048 || len(v.OriginalTitle) > 2048 || len(v.SortTitle) > 2048 || len(v.Edition) > 512 || len(v.Tagline) > 4096 || len(v.Certification) > 128 {
			v.Issues = append(v.Issues, "skipped_nfo_field")
			if len(v.Title) > 2048 {
				v.Title = ""
			}
			if len(v.OriginalTitle) > 2048 {
				v.OriginalTitle = ""
			}
			if len(v.SortTitle) > 2048 {
				v.SortTitle = ""
			}
			if len(v.Edition) > 512 {
				v.Edition = ""
			}
			if len(v.Tagline) > 4096 {
				v.Tagline = ""
			}
			if len(v.Certification) > 128 {
				v.Certification = ""
			}
		}
		for _, g := range n.values("genre") {
			if len(v.Genres) >= 64 || len(g.text) > 200 {
				v.Issues = appendNFOIssue(v.Issues, "skipped_nfo_genre")
				continue
			}
			v.Genres = append(v.Genres, strings.TrimSpace(g.text))
		}
		for _, g := range n.values("studio") {
			if len(v.Studios) >= 64 || len(g.text) > 200 {
				v.Issues = appendNFOIssue(v.Issues, "skipped_nfo_studio")
				continue
			}
			v.Studios = append(v.Studios, strings.TrimSpace(g.text))
		}
		for _, set := range n.values("set") {
			v.Collection = set.value("name")
			if v.Collection == "" {
				v.Collection = strings.TrimSpace(set.text)
			}
			if len(v.Collection) > 2048 {
				v.Collection = ""
				v.Issues = appendNFOIssue(v.Issues, "skipped_nfo_collection")
			}
			break
		}
		ids := map[string]string{}
		put := func(provider, id string) error {
			provider = strings.ToLower(strings.TrimSpace(provider))
			id = strings.TrimSpace(id)
			if provider != "tmdb" && provider != "tvdb" && provider != "anilist" && provider != "imdb" {
				return nil
			}
			typ := kind
			if provider == "imdb" {
				typ = "title"
			}
			if provider == "anilist" {
				if !anime || kind == "episode" {
					v.Issues = appendNFOIssue(v.Issues, "invalid_nfo_identifier")
					return nil
				}
				typ = "anime"
			}
			value := metadataprovider.ScreenID{Provider: provider, Type: typ, ID: id}
			if !metadataprovider.ValidScreenID(value) {
				v.Issues = appendNFOIssue(v.Issues, "invalid_nfo_identifier")
				return nil
			}
			for _, old := range v.IDs {
				if old == value {
					return nil
				}
			}
			if len(v.IDs) >= 16 {
				v.Issues = appendNFOIssue(v.Issues, "skipped_nfo_identifiers")
				return nil
			}
			if prior := ids[provider]; prior != "" {
				if prior != id {
					v.Issues = appendNFOIssue(v.Issues, "conflicting_nfo_identifiers")
					v.IDs = append(v.IDs, value)
					return nil
				}
				return nil
			}
			ids[provider] = id
			v.IDs = append(v.IDs, value)
			return nil
		}
		if hint, ok := metadataprovider.ParseScreenHint(strings.TrimSpace(outside), kind); ok && (hint.Type == kind || hint.Type == "title" || hint.Type == "anime" && kind == "show") {
			_ = put(hint.Provider, hint.ID)
		}
		for _, id := range n.values("uniqueid") {
			if e = put(id.attrs["type"], id.text); e != nil {
				return nil, e
			}
		}
		for _, p := range []string{"tmdb", "tvdb", "anilist", "imdb"} {
			if id := n.value(p + "id"); id != "" {
				if e = put(p, id); e != nil {
					return nil, e
				}
			}
		}
		if id := n.value("id"); id != "" {
			if hint, ok := metadataprovider.ParseScreenHint(id, kind); ok {
				_ = put(hint.Provider, hint.ID)
			}
		}
		for _, actor := range n.values("actor") {
			if len(v.Credits) >= 100 {
				v.Issues = appendNFOIssue(v.Issues, "skipped_nfo_credit")
				continue
			}
			name, role := actor.value("name"), actor.value("role")
			if name == "" {
				continue
			}
			if len(name) > 300 || len(role) > 500 {
				v.Issues = appendNFOIssue(v.Issues, "skipped_nfo_credit")
				continue
			}
			ordinal := len(v.Credits)
			if order, e := nfoNumber(actor.value("order"), 10000); e == nil && order != nil {
				ordinal = *order
			}
			v.Credits = append(v.Credits, metadataprovider.ScreenCredit{Name: name, Role: role, Department: "Acting", Ordinal: ordinal})
			if thumb := actor.value("thumb"); thumb != "" && len(thumb) <= 2048 {
				v.ActorThumbnails = append(v.ActorThumbnails, assets.NFOActorThumbnail{Name: name, URL: thumb})
			}
		}
		for _, spec := range []struct{ tag, department, role string }{{"director", "Directing", "Director"}, {"credits", "Writing", "Writer"}} {
			for _, node := range n.values(spec.tag) {
				name := strings.TrimSpace(node.text)
				if name == "" || len(name) > 300 || len(v.Credits) >= 100 {
					continue
				}
				v.Credits = append(v.Credits, metadataprovider.ScreenCredit{Name: name, Department: spec.department, Role: spec.role, Ordinal: len(v.Credits)})
			}
		}
		for _, node := range n.values("tag") {
			name := strings.TrimSpace(node.text)
			if name != "" && len(name) <= 200 && len(v.Tags) < 64 {
				v.Tags = append(v.Tags, name)
			}
		}
		for _, node := range n.values("country") {
			name := strings.TrimSpace(node.text)
			if name != "" && len(name) <= 200 && len(v.Countries) < 64 {
				v.Countries = append(v.Countries, name)
			}
		}
		v.Status = n.value("status")
		if len(v.Status) > 64 {
			v.Status = ""
		}
		v.Trailer = n.value("trailer")
		if len(v.Trailer) > 2048 {
			v.Trailer = ""
		}
		v.DisplaySeason, _ = nfoNumber(n.value("displayseason"), 9999)
		v.DisplayEpisode, _ = nfoNumber(n.value("displayepisode"), 1000000)
		v.DateAdded = n.value("dateadded")
		if len(v.DateAdded) > 64 {
			v.DateAdded = ""
		}
		v.LockData = strings.EqualFold(n.value("lockdata"), "true")
		for _, set := range n.values("set") {
			v.CollectionOverview = set.value("overview")
			break
		}
		rating, votes := n.value("rating"), n.value("votes")
		scale := 10
		for _, container := range n.values("ratings") {
			for _, node := range container.values("rating") {
				if rating == "" || node.attrs["default"] == "true" {
					rating = node.value("value")
					votes = node.value("votes")
					if m, e := strconv.Atoi(node.attrs["max"]); e == nil && m > 0 && m <= 100 {
						scale = m
					}
				}
			}
		}
		if value, e := strconv.ParseFloat(rating, 64); e == nil && value >= 0 && value <= float64(scale) {
			count, _ := strconv.Atoi(strings.ReplaceAll(votes, ",", ""))
			v.Rating = &metadataprovider.ScreenRating{Value: value, Scale: scale, Votes: max(0, count)}
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, errors.New("no supported NFO records")
	}
	return out, nil
}

func escapeNFOAmpersands(raw []byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(raw); i++ {
		if raw[i] == '&' {
			end := i + 1
			for end < len(raw) && end-i <= 32 && raw[end] != ';' && raw[end] != '<' && raw[end] != '&' && raw[end] != ' ' && raw[end] != '\n' {
				end++
			}
			if end >= len(raw) || raw[end] != ';' {
				out.WriteString("&amp;")
				continue
			}
		}
		out.WriteByte(raw[i])
	}
	return out.Bytes()
}
func nfoNumericText(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 2 && (strings.EqualFold(fields[1], "min") || strings.EqualFold(fields[1], "minutes")) {
		return fields[0]
	}
	return s
}
func appendNFOIssue(issues []string, issue string) []string {
	for _, s := range issues {
		if s == issue {
			return issues
		}
	}
	if len(issues) < 16 {
		return append(issues, issue)
	}
	return issues
}
