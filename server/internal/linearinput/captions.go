package linearinput

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CaptionRendition is a selected source's declared in-band CEA service. It
// contains no source URI, credential, arbitrary attribute, or fetch target.
type CaptionRendition struct {
	Name, Language, InstreamID string
	Default, Autoselect        bool
}
type CaptionDeclarations struct {
	GroupID    string
	Bandwidth  int64
	Renditions []CaptionRendition
}

func captionText(s string, max int) bool {
	if s == "" || len(s) > max || !utf8.ValidString(s) || strings.ContainsAny(s, "\"\\") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func captionService(id string) bool {
	if len(id) == 3 && strings.HasPrefix(id, "CC") {
		return id[2] >= '1' && id[2] <= '4'
	}
	if !strings.HasPrefix(id, "SERVICE") {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "SERVICE"))
	return err == nil && n >= 1 && n <= 63 && id == "SERVICE"+strconv.Itoa(n)
}

// Valid applies the closed in-band rendition vocabulary before emission.
func (r CaptionRendition) Valid() bool {
	return captionService(r.InstreamID) && captionText(r.Name, 256) && (r.Language == "" || captionText(r.Language, 63)) && (!r.Default || r.Autoselect)
}

// Valid checks the source group and every in-band declaration before emission.
func (c CaptionDeclarations) Valid() bool {
	if !captionText(c.GroupID, 256) || c.GroupID == "NONE" || c.Bandwidth <= 0 || c.Bandwidth > 1_000_000_000_000 || len(c.Renditions) == 0 || len(c.Renditions) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, r := range c.Renditions {
		if !r.Valid() || seen[r.InstreamID] {
			return false
		}
		seen[r.InstreamID] = true
	}
	return true
}

// selectedCaptions runs after variant selection. Missing/invalid declarations
// remain unknown; a CC1 track or bandwidth is never guessed from video presence.
func selectedCaptions(lines []string) (CaptionDeclarations, bool) {
	out := CaptionDeclarations{Renditions: []CaptionRendition{}}
	group := ""
	master := false
	for _, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			continue
		}
		master = true
		attrs, err := parseAttributes(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
		if err != nil {
			return out, master
		}
		group = attribute(attrs, "CLOSED-CAPTIONS")
		bits, err := strconv.ParseInt(attribute(attrs, "BANDWIDTH"), 10, 64)
		if err == nil && bits > 0 && bits <= 1_000_000_000_000 {
			out.Bandwidth = bits
		}
		break
	}
	if group == "NONE" || !captionText(group, 256) {
		return out, master
	}
	out.GroupID = group
	seen := map[string]bool{}
	for _, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-MEDIA:") {
			continue
		}
		attrs, err := parseAttributes(strings.TrimPrefix(line, "#EXT-X-MEDIA:"))
		if err != nil {
			continue
		}
		if attribute(attrs, "TYPE") != "CLOSED-CAPTIONS" || attribute(attrs, "GROUP-ID") != group || attribute(attrs, "URI") != "" {
			continue
		}
		id, name, language := attribute(attrs, "INSTREAM-ID"), attribute(attrs, "NAME"), attribute(attrs, "LANGUAGE")
		if !captionService(id) || !captionText(name, 256) || seen[id] || len(out.Renditions) >= 64 {
			continue
		}
		if !captionText(language, 63) {
			language = ""
		}
		rendition := CaptionRendition{Name: name, Language: language, InstreamID: id, Default: attribute(attrs, "DEFAULT") == "YES", Autoselect: attribute(attrs, "AUTOSELECT") == "YES"}
		if !rendition.Valid() {
			continue
		}
		seen[id] = true
		out.Renditions = append(out.Renditions, rendition)
	}
	return out, master
}

// Captions returns a copy of the metadata observed through this reader's
// private gateway. Media-playlist refreshes cannot erase a master declaration.
func (g *Gateway) Captions() CaptionDeclarations {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := g.captions
	out.Renditions = append([]CaptionRendition(nil), out.Renditions...)
	return out
}
