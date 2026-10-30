package localmetadata

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"strings"
	"unicode/utf8"
)

type audioNode struct {
	name     string
	attrs    map[string]string
	text     string
	children []*audioNode
	partial  bool
}

func parseAudioXML(raw []byte) (*audioNode, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return nil, errors.New("sidecar size")
	}
	d := xml.NewDecoder(bytes.NewReader(raw))
	d.CharsetReader = localXMLCharset
	stack := []*audioNode{}
	var root *audioNode
	nodes := 0
	for {
		token, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil, errors.New("XML directives are not supported")
		case xml.StartElement:
			nodes++
			if nodes > 1024 || len(stack) >= 16 || len(t.Attr) > 32 {
				return nil, errors.New("XML budget")
			}
			n := &audioNode{name: strings.ToLower(t.Name.Local), attrs: map[string]string{}}
			for _, a := range t.Attr {
				if len(a.Value) > 4096 {
					return nil, errors.New("XML attribute budget")
				}
				n.attrs[strings.ToLower(a.Name.Local)] = a.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("multiple XML roots")
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("invalid XML")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				n := stack[len(stack)-1]
				if len(n.text)+len(t) > 4096 {
					root.partial = true
					room := 4096 - len(n.text)
					kept := string(t[:room])
					for len(kept) > 0 && !utf8.ValidString(kept) {
						kept = kept[:len(kept)-1]
					}
					n.text += kept
				} else {
					n.text += string(t)
				}
			} else if strings.TrimSpace(string(t)) != "" {
				return nil, errors.New("trailing XML text")
			}
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, errors.New("incomplete XML")
	}
	return root, nil
}
func (n *audioNode) all(name string) []*audioNode {
	out := []*audioNode{}
	if n.name == name {
		out = append(out, n)
	}
	for _, c := range n.children {
		out = append(out, c.all(name)...)
	}
	return out
}
func (n *audioNode) first(name string) string {
	v := n.all(name)
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[0].text)
}
func audioList(values []string) string { raw, _ := json.Marshal(values); return string(raw) }
func uniqueAudioNames(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] && len(out) < 128 {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out
}
func parseOPF(raw []byte) (map[string]string, error) {
	root, e := parseAudioXML(raw)
	if e != nil {
		return nil, e
	}
	if root.name != "package" && root.name != "metadata" {
		return nil, errors.New("not OPF")
	}
	meta := root
	if root.name == "package" {
		m := root.all("metadata")
		if len(m) != 1 {
			return nil, errors.New("OPF metadata")
		}
		meta = m[0]
	}
	out := map[string]string{"album": meta.first("title"), "language": meta.first("language"), "publisher": meta.first("publisher"), "date": meta.first("date"), "description": meta.first("description")}
	if root.partial {
		out["_partial"] = "true"
	}
	roles := map[string]string{}
	positions := map[string]string{}
	collections := map[string]string{}
	for _, m := range meta.all("meta") {
		text := strings.TrimSpace(m.text)
		ref := strings.TrimPrefix(m.attrs["refines"], "#")
		switch m.attrs["property"] {
		case "role":
			roles[ref] = text
		case "belongs-to-collection":
			collections[m.attrs["id"]] = text
		case "group-position":
			positions[ref] = text
		}
		switch m.attrs["name"] {
		case "calibre:series":
			out["series"] = m.attrs["content"]
		case "calibre:series_index":
			out["series_position"] = m.attrs["content"]
		case "edition":
			out["edition"] = m.attrs["content"]
		}
	}
	authors, narrators := []string{}, []string{}
	for _, c := range append(meta.all("creator"), meta.all("contributor")...) {
		role := c.attrs["role"]
		if v := roles[c.attrs["id"]]; v != "" {
			role = v
		}
		name := strings.TrimSpace(c.text)
		switch role {
		case "nrt":
			narrators = append(narrators, name)
		case "aut":
			authors = append(authors, name)
		case "":
			if c.name == "creator" {
				authors = append(authors, name)
			}
		}
	}
	authors = uniqueAudioNames(authors)
	narrators = uniqueAudioNames(narrators)
	if len(authors) > 0 {
		out["author"] = strings.Join(authors, "; ")
		out["authors"] = audioList(authors)
	}
	if len(narrators) > 0 {
		out["narrator"] = strings.Join(narrators, "; ")
		out["narrators"] = audioList(narrators)
	}
	// Preserve all collection associations, not an arbitrary map iteration winner.
	series := []map[string]string{}
	for _, m := range meta.all("meta") {
		if m.attrs["property"] == "belongs-to-collection" {
			series = append(series, map[string]string{"name": collections[m.attrs["id"]], "position": positions[m.attrs["id"]]})
		}
	}
	if len(series) > 0 {
		out["series"] = series[0]["name"]
		out["series_position"] = series[0]["position"]
		v, _ := json.Marshal(series)
		out["series_list"] = string(v)
	}
	ids := []map[string]string{}
	for _, i := range meta.all("identifier") {
		v := strings.TrimSpace(i.text)
		scheme := strings.ToLower(i.attrs["scheme"])
		if strings.HasPrefix(strings.ToLower(v), "urn:isbn:") {
			scheme = "isbn"
			v = v[len("urn:isbn:"):]
		}
		if scheme == "" {
			scheme = "local"
		}
		if len(v) <= 1024 && len(scheme) <= 64 && v != "" {
			ids = append(ids, map[string]string{"scheme": scheme, "value": v})
			if scheme == "isbn" || scheme == "asin" {
				out[scheme] = v
			}
		}
	}
	if len(ids) > 0 {
		v, _ := json.Marshal(ids)
		out["identifiers"] = string(v)
	}
	return out, nil
}
func parseMusicNFO(raw []byte, kind string) (map[string]string, error) {
	root, e := parseAudioXML(raw)
	if e != nil {
		return nil, e
	}
	valid := kind == "music" && (root.name == "album" || root.name == "song" || root.name == "artist") || kind == "audiobook" && (root.name == "book" || root.name == "audiobook")
	if !valid {
		return nil, errors.New("wrong music metadata kind")
	}
	out := map[string]string{}
	if root.partial {
		out["_partial"] = "true"
	}
	mapping := map[string]string{"title": "title", "album": "album", "artist": "artist", "albumartist": "album_artist", "year": "year", "releasedate": "date", "author": "author", "narrator": "narrator", "series": "series", "seriesposition": "series_position", "publisher": "publisher", "language": "language", "edition": "edition", "isbn": "isbn", "asin": "asin", "plot": "description", "track": "track", "disc": "disc", "barcode": "barcode", "cataloguenumber": "catalognumber", "musicbrainzalbumid": "musicbrainz_albumid", "musicbrainzreleasegroupid": "musicbrainz_releasegroupid", "musicbrainztrackid": "musicbrainz_trackid", "musicbrainzreleasetrackid": "musicbrainz_releasetrackid"}
	for tag, key := range mapping {
		out[key] = root.first(tag)
	}
	if root.name == "album" || root.name == "book" || root.name == "audiobook" {
		out["album"] = root.first("title")
		delete(out, "title")
	}
	for _, spec := range []struct{ tag, single, multi string }{{"author", "author", "authors"}, {"narrator", "narrator", "narrators"}, {"artist", "artist", "artists"}, {"albumartist", "album_artist", "album_artists"}} {
		names := []string{}
		for _, n := range root.all(spec.tag) {
			v := strings.TrimSpace(n.text)
			if v == "" {
				v = n.first("name")
			}
			names = append(names, v)
		}
		names = uniqueAudioNames(names)
		if len(names) > 0 {
			out[spec.single] = strings.Join(names, "; ")
			out[spec.multi] = audioList(names)
		}
	}
	return out, nil
}
func parseAudioJSON(raw []byte) (map[string]string, error) {
	// Unknown descriptive keys are tolerated; personal state is never imported.
	var v map[string]json.RawMessage
	d := json.NewDecoder(bytes.NewReader(raw))
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	var tail any
	if d.Decode(&tail) != io.EOF || len(v) > 128 {
		return nil, errors.New("invalid sidecar")
	}
	aliases := map[string]string{"albumArtist": "album_artist", "discNumber": "disc", "trackNumber": "track", "seriesIndex": "series_position", "seriesPosition": "series_position", "seriesList": "series_list", "musicBrainzRecordingId": "musicbrainz_trackid", "musicBrainzReleaseId": "musicbrainz_albumid", "musicBrainzReleaseTrackId": "musicbrainz_releasetrackid", "musicBrainzReleaseGroupId": "musicbrainz_releasegroupid"}
	out := map[string]string{}
	for k, raw := range v {
		key := assets.AudioTagName(k)
		if alias := aliases[k]; alias != "" {
			key = alias
		}
		if key == "" {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) != nil {
			var names []string
			if (key == "authors" || key == "narrators" || key == "artists" || key == "album_artists") && json.Unmarshal(raw, &names) == nil {
				text = audioList(uniqueAudioNames(names))
			} else if key == "identifiers" || key == "series_list" {
				var entries []map[string]string
				if json.Unmarshal(raw, &entries) != nil || len(entries) > 64 {
					continue
				}
				valid := true
				for _, entry := range entries {
					if len(entry) > 2 {
						valid = false
						break
					}
					for k, v := range entry {
						if !assets.ValidAudioTag("title", v) && v != "" {
							valid = false
						}
						if key == "identifiers" && (k != "scheme" && k != "value") || key == "series_list" && (k != "name" && k != "position") {
							valid = false
						}
					}
				}
				if !valid {
					continue
				}
				encoded, _ := json.Marshal(entries)
				text = string(encoded)
			} else if key == "track" || key == "disc" || key == "year" || key == "series_position" || key == "total_tracks" || key == "total_discs" {
				var number json.Number
				if json.Unmarshal(raw, &number) != nil {
					continue
				}
				text = number.String()
			} else {
				continue
			}
		}
		out[key] = text
	}
	for multi, single := range map[string]string{"authors": "author", "narrators": "narrator", "artists": "artist", "album_artists": "album_artist"} {
		if out[single] == "" {
			var names []string
			if json.Unmarshal([]byte(out[multi]), &names) == nil {
				out[single] = strings.Join(uniqueAudioNames(names), "; ")
			}
		}
	}
	return out, nil
}
