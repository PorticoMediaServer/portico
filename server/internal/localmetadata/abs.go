package localmetadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// absSeries is Audiobookshelf's "Name #sequence": a sequence is whatever
// follows the last " #", without spaces or another "#".
var absSeries = regexp.MustCompile(` #([^#\s]+)$`)

// parseABSMetadata reads the metadata.json Audiobookshelf keeps in a book's
// folder (its current flat form, and the older one nested under "metadata").
// title, authors, narrators, series, genres, published year/date, publisher,
// description, ISBN, ASIN, language and chapters are kept. subtitle, tags,
// explicit and abridged have no place in a Portico book yet and are left alone;
// so is anything personal.
func parseABSMetadata(raw []byte) (map[string]string, []assets.Chapter, error) {
	if len(raw) == 0 || len(raw) > 256<<10 {
		return nil, nil, errors.New("sidecar size")
	}
	var doc map[string]json.RawMessage
	d := json.NewDecoder(bytes.NewReader(raw))
	if e := d.Decode(&doc); e != nil {
		return nil, nil, e
	}
	var tail any
	if d.Decode(&tail) != io.EOF || len(doc) > 128 {
		return nil, nil, errors.New("invalid sidecar")
	}
	if nested, ok := doc["metadata"]; ok {
		var inner map[string]json.RawMessage
		if json.Unmarshal(nested, &inner) == nil && len(inner) <= 128 {
			for k, v := range inner {
				if _, own := doc[k]; !own {
					doc[k] = v
				}
			}
		}
	}
	text := func(key string) string {
		var s string
		if json.Unmarshal(doc[key], &s) == nil {
			return strings.TrimSpace(s)
		}
		var n json.Number
		if json.Unmarshal(doc[key], &n) == nil {
			return n.String()
		}
		return ""
	}
	list := func(key string) []string {
		var values []string
		if json.Unmarshal(doc[key], &values) != nil || len(values) > 128 {
			return nil
		}
		return uniqueAudioNames(values)
	}
	out := map[string]string{"album": text("title"), "publisher": text("publisher"), "description": boundedText(text("description"), 4096), "isbn": text("isbn"), "asin": text("asin"), "language": text("language")}
	if year := text("publishedYear"); len(year) == 4 {
		out["year"] = year
	}
	if date := text("publishedDate"); len(date) >= 4 && len(date) <= 32 {
		out["date"] = date
	}
	for _, spec := range []struct{ key, single, multi string }{{"authors", "author", "authors"}, {"narrators", "narrator", "narrators"}} {
		if names := list(spec.key); len(names) > 0 {
			out[spec.single] = strings.Join(names, "; ")
			out[spec.multi] = audioList(names)
		}
	}
	if genres := list("genres"); len(genres) > 0 {
		out["genres"] = audioList(genres)
	}
	series := []map[string]string{}
	for _, entry := range list("series") {
		name, position := entry, ""
		if m := absSeries.FindStringSubmatchIndex(entry); m != nil {
			name, position = strings.TrimSpace(entry[:m[0]]), entry[m[2]:m[3]]
		}
		if name != "" && len(series) < 64 {
			series = append(series, map[string]string{"name": name, "position": position})
		}
	}
	if len(series) > 0 {
		out["series"], out["series_position"] = series[0]["name"], series[0]["position"]
		encoded, _ := json.Marshal(series)
		out["series_list"] = string(encoded)
	}
	ids := []map[string]string{}
	for _, scheme := range []string{"isbn", "asin"} {
		if out[scheme] != "" {
			ids = append(ids, map[string]string{"scheme": scheme, "value": out[scheme]})
		}
	}
	if len(ids) > 0 {
		encoded, _ := json.Marshal(ids)
		out["identifiers"] = string(encoded)
	}
	for k, v := range out {
		if v == "" {
			delete(out, k)
		}
	}
	return out, absChapters(doc["chapters"]), nil
}

// absChapters are the book's chapters in seconds on the book's own timeline. A
// list with any entry Audiobookshelf itself would refuse is ignored whole.
func absChapters(raw json.RawMessage) []assets.Chapter {
	var entries []struct {
		Start *float64 `json:"start"`
		End   *float64 `json:"end"`
		Title *string  `json:"title"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &entries) != nil || len(entries) == 0 || len(entries) > 4096 {
		return nil
	}
	out := make([]assets.Chapter, 0, len(entries))
	for _, c := range entries {
		if c.Start == nil || c.End == nil || c.Title == nil || *c.Title == "" || math.IsNaN(*c.Start) || math.IsNaN(*c.End) {
			return nil
		}
		out = append(out, assets.Chapter{Title: assets.CleanAudioTag("title", *c.Title), Start: *c.Start, End: *c.End})
	}
	return out
}

// fitsFile is whether a book's chapter timeline is this file's: it ends where
// the file does. Chapters of a book spread over several files run past any one
// of them and are left to the files' own.
func fitsFile(chapters []assets.Chapter, duration float64) bool {
	if len(chapters) == 0 || duration <= 0 {
		return false
	}
	end := 0.0
	for _, c := range chapters {
		end = math.Max(end, math.Max(c.End, c.Start))
	}
	return math.Abs(end-duration) <= math.Max(2, duration*0.005)
}
