package metadata

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/language"
)

// The editable field registry. One target kind (plus, for items, the item kind)
// selects the fields a client may edit, the control to render, and the bounds
// the server enforces. Clients render from this list; they never hard-code it.
type RepairFieldSpec struct {
	Field     string   `json:"field"`
	Label     string   `json:"label"`
	Group     string   `json:"group"`
	Type      string   `json:"type"`
	MaxLength int      `json:"maxLength,omitempty"`
	Min       *int     `json:"min,omitempty"`
	Max       *int     `json:"max,omitempty"`
	Allowed   []string `json:"allowed,omitempty"`
	Bulk      bool     `json:"bulk"`
}

const (
	listEntries   = 64
	listEntryRune = 128
	textLimit     = 300
	longTextLimit = 20000
)

func intPtr(v int) *int { return &v }

func textSpec(field, label, group string, bulk bool) RepairFieldSpec {
	return RepairFieldSpec{Field: field, Label: label, Group: group, Type: "text", MaxLength: textLimit, Bulk: bulk}
}
func multilineSpec(field, label string) RepairFieldSpec {
	return RepairFieldSpec{Field: field, Label: label, Group: "text", Type: "multiline", MaxLength: longTextLimit, Bulk: false}
}
func integerSpec(field, label string, low, high int, bulk bool) RepairFieldSpec {
	return RepairFieldSpec{Field: field, Label: label, Group: "numbers", Type: "integer", Min: intPtr(low), Max: intPtr(high), Bulk: bulk}
}
func listSpec(field, label string) RepairFieldSpec {
	return RepairFieldSpec{Field: field, Label: label, Group: "general", Type: "list", MaxLength: listEntryRune, Max: intPtr(listEntries), Bulk: true}
}
func dateSpec(field, label string, bulk bool) RepairFieldSpec {
	return RepairFieldSpec{Field: field, Label: label, Group: "general", Type: "date", MaxLength: 10, Bulk: bulk}
}

var yearSpec = integerSpec("year", "Year", 0, 9999, true)

// repairSchema publishes the editable fields for one target. itemKind is the
// items.kind column and is ignored for every other target kind.
func repairSchema(kind, itemKind string) []RepairFieldSpec {
	switch kind {
	case "item":
		switch itemKind {
		case "ebook", "comic":
			return []RepairFieldSpec{textSpec("title", "Title", "general", false), multilineSpec("description", "Description"), yearSpec, readingAttributionSpec("author", "Author"), readingAttributionSpec("series", "Series"), readingAttributionSpec("seriesNumber", "Series number"), {Field: "publicationType", Label: "web.metadata.field.publicationType", Group: "general", Type: "enum", Allowed: []string{"", "book", "magazine", "newspaper"}, Bulk: true}}
		case "movie":
			return []RepairFieldSpec{
				textSpec("title", "Title", "general", false),
				textSpec("sortTitle", "Sort title", "general", false),
				textSpec("metadataLanguage", "Metadata language", "general", true),
				textSpec("originalTitle", "Original title", "general", false),
				textSpec("edition", "Edition", "general", true),
				textSpec("tagline", "Tagline", "text", false),
				multilineSpec("description", "Description"),
				yearSpec,
				dateSpec("releaseDate", "Release date", false),
				textSpec("contentRating", "Content rating", "general", true),
				textSpec("studio", "Studio", "general", true),
				textSpec("country", "Country", "general", true),
				listSpec("tags", "Tags"),
				listSpec("labels", "Labels"),
			}
		case "episode":
			return []RepairFieldSpec{
				textSpec("title", "Title", "general", false),
				textSpec("sortTitle", "Sort title", "general", false),
				textSpec("metadataLanguage", "Metadata language", "general", true),
				multilineSpec("description", "Description"),
				yearSpec,
				dateSpec("releaseDate", "Air date", false),
				textSpec("contentRating", "Content rating", "general", true),
				textSpec("network", "Network", "general", true),
				integerSpec("seasonNumber", "Season number", 0, 9999, false),
				integerSpec("episodeNumber", "Episode number", 1, 9999, false),
				listSpec("tags", "Tags"),
				listSpec("labels", "Labels"),
			}
		case "song":
			return []RepairFieldSpec{
				textSpec("title", "Title", "general", false),
				textSpec("sortTitle", "Sort title", "general", false),
				textSpec("metadataLanguage", "Metadata language", "general", true),
				multilineSpec("description", "Description"),
				yearSpec,
				integerSpec("trackNumber", "Track number", 0, 9999, false),
				integerSpec("discNumber", "Disc number", 0, 999, false),
				listSpec("tags", "Tags"),
				listSpec("labels", "Labels"),
			}
		case "audiobook_file":
			return []RepairFieldSpec{
				textSpec("title", "Title", "general", false),
				multilineSpec("description", "Description"),
				integerSpec("partNumber", "Part number", 0, 9999, false),
				listSpec("tags", "Tags"),
				listSpec("labels", "Labels"),
			}
		}
		return []RepairFieldSpec{textSpec("title", "Title", "general", false), multilineSpec("description", "Description"), yearSpec}
	case "show":
		return []RepairFieldSpec{
			textSpec("title", "Title", "general", false),
			textSpec("sortTitle", "Sort title", "general", false),
			textSpec("metadataLanguage", "Metadata language", "general", true),
			textSpec("originalTitle", "Original title", "general", false),
			textSpec("tagline", "Tagline", "text", false),
			multilineSpec("description", "Description"),
			yearSpec,
			textSpec("contentRating", "Content rating", "general", true),
			textSpec("network", "Network", "general", true),
			textSpec("studio", "Studio", "general", true),
			textSpec("country", "Country", "general", true),
			listSpec("tags", "Tags"),
			listSpec("labels", "Labels"),
		}
	case "season":
		return []RepairFieldSpec{
			integerSpec("number", "Season number", 0, 9999, false),
			textSpec("title", "Title", "general", false),
			multilineSpec("description", "Description"),
		}
	case "album":
		return []RepairFieldSpec{
			textSpec("title", "Title", "general", false),
			textSpec("sortTitle", "Sort title", "general", false),
			textSpec("metadataLanguage", "Metadata language", "general", true),
			multilineSpec("description", "Description"),
			yearSpec,
			textSpec("label", "Record label", "general", true),
			listSpec("tags", "Tags"),
		}
	case "artist":
		return []RepairFieldSpec{
			textSpec("title", "Name", "general", false),
			textSpec("sortTitle", "Sort name", "general", false),
			textSpec("metadataLanguage", "Metadata language", "general", true),
			multilineSpec("description", "Description"),
			listSpec("tags", "Tags"),
		}
	case "book":
		return []RepairFieldSpec{
			textSpec("title", "Title", "general", false),
			textSpec("sortTitle", "Sort title", "general", false),
			textSpec("metadataLanguage", "Metadata language", "general", true),
			multilineSpec("description", "Description"),
			textSpec("author", "Author", "general", true),
			textSpec("narrator", "Narrator", "general", true),
			textSpec("series", "Series", "general", true),
			integerSpec("seriesIndex", "Series index", 0, 9999, false),
			listSpec("tags", "Tags"),
			listSpec("labels", "Labels"),
		}
	}
	return nil
}

func schemaIndex(specs []RepairFieldSpec) map[string]RepairFieldSpec {
	out := make(map[string]RepairFieldSpec, len(specs))
	for _, s := range specs {
		out[s.Field] = s
	}
	return out
}

// artworkRolesFor publishes the roles the artwork store accepts for a target, so
// a client knows which upload and selection surfaces to offer.
func artworkRolesFor(kind, itemKind string) []string {
	if kind == "item" && readingRepairKind(itemKind) {
		return []string{"cover"}
	}
	switch kind {
	case "item":
		switch itemKind {
		case "movie":
			return []string{"poster", "backdrop", "logo", "thumbnail"}
		case "episode":
			return []string{"still", "thumbnail"}
		case "song", "audiobook_file":
			return []string{"cover"}
		}
		return []string{"poster", "backdrop", "thumbnail"}
	case "show":
		return []string{"poster", "backdrop", "logo", "banner", "thumbnail"}
	case "season":
		return []string{"poster", "thumbnail"}
	case "album":
		return []string{"cover", "backdrop"}
	case "artist":
		return []string{"portrait", "backdrop", "logo", "square"}
	case "book":
		return []string{"cover", "backdrop"}
	}
	return []string{}
}

// normalizeList canonicalizes a list field. Entries are trimmed, empty entries
// dropped, duplicates removed case-insensitively and order preserved.
func normalizeList(values []string) ([]string, bool) {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > listEntryRune || strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return nil, false
		}
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
		if len(out) > listEntries {
			return nil, false
		}
	}
	return out, true
}

func encodeList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(raw)
}

// decodeList reads a stored list. A scanner or provider may have written a plain
// string into the column; that is read as a single entry rather than rejected.
func decodeList(stored string) []string {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return []string{}
	}
	if strings.HasPrefix(stored, "[") {
		var values []string
		if json.Unmarshal([]byte(stored), &values) == nil {
			out, ok := normalizeList(values)
			if ok {
				return out
			}
			return []string{}
		}
	}
	out, ok := normalizeList(strings.Split(stored, ","))
	if !ok {
		return []string{}
	}
	return out
}

func validISODate(v string) bool {
	if len(v) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", v)
	return err == nil
}

// validFieldValue enforces the registry's own bounds. An empty string always
// clears a field; an empty integer clears it rather than becoming zero.
func validFieldValue(kind string, spec RepairFieldSpec, value string) bool {
	required := spec.Field == "title" && kind != "season"
	if !utf8.ValidString(value) {
		return false
	}
	if value == "" {
		return !required
	}
	if spec.Field == "metadataLanguage" {
		_, err := language.Parse(value)
		return err == nil
	}
	switch spec.Type {
	case "integer", "number":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return false
		}
		// A canonical decimal keeps the stored text and the column in agreement.
		if strconv.Itoa(n) != value {
			return false
		}
		if spec.Min != nil && n < *spec.Min {
			return false
		}
		return spec.Max == nil || n <= *spec.Max
	case "date":
		return validISODate(value)
	case "enum":
		for _, a := range spec.Allowed {
			if a == value {
				return true
			}
		}
		return false
	case "list":
		var values []string
		if json.Unmarshal([]byte(value), &values) != nil {
			return false
		}
		normalized, ok := normalizeList(values)
		return ok && encodeList(normalized) == value
	case "multiline":
		if utf8.RuneCountInString(value) > spec.MaxLength {
			return false
		}
		return strings.IndexFunc(value, func(r rune) bool {
			return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r'
		}) < 0
	}
	limit := spec.MaxLength
	if limit == 0 {
		limit = textLimit
	}
	if utf8.RuneCountInString(value) > limit || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	return !required || strings.TrimSpace(value) != ""
}

func repairSchemaTabs(schema []RepairFieldSpec) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, field := range schema {
		if !seen[field.Group] {
			seen[field.Group] = true
			out = append(out, field.Group)
		}
	}
	return out
}
