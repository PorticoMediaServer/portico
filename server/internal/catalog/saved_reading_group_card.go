package catalog

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

type SavedReadingGroupLink struct {
	GroupKind ReadingSavedGroupKind `json:"groupKind"`
	SeriesID  string                `json:"seriesId"`
}
type SavedReadingGroupNavigation struct {
	View     string `json:"view"`
	EntityID string `json:"entityId"`
	Pivot    string `json:"pivot,omitempty"`
}

// This is a Saved reference alternative, not a ContentEntry/catalogue entity.
// Hidden references serialize only their own private id and removal command.
type SavedReadingGroupCard struct {
	ID           string                       `json:"id"`
	Kind         string                       `json:"kind"`
	Hidden       bool                         `json:"hidden"`
	Revision     int64                        `json:"revision"`
	RemoveMyList bool                         `json:"removeMyList"`
	LibraryID    string                       `json:"libraryId,omitempty"`
	ReadingGroup *SavedReadingGroupLink       `json:"readingGroup,omitempty"`
	Title        string                       `json:"title,omitempty"`
	Count        *int64                       `json:"count,omitempty"`
	PosterURL    string                       `json:"posterUrl,omitempty"`
	Navigation   *SavedReadingGroupNavigation `json:"navigation,omitempty"`
}

func (c SavedReadingGroupCard) MarshalJSON() ([]byte, error) {
	if c.Kind != "reading_group" || c.ID == "" || c.Revision < 1 || c.Revision > 9007199254740991 {
		return nil, fmt.Errorf("invalid private Reading group card")
	}
	if c.Hidden {
		return json.Marshal(struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Hidden   bool   `json:"hidden"`
			Revision int64  `json:"revision"`
			Remove   bool   `json:"removeMyList"`
		}{c.ID, c.Kind, true, c.Revision, c.RemoveMyList})
	}
	if c.ReadingGroup == nil || c.Navigation == nil || c.Count == nil || *c.Count < 1 || *c.Count > 9007199254740991 || c.Title == "" || c.LibraryID == "" {
		return nil, fmt.Errorf("invalid visible Reading group card")
	}
	if c.Navigation.View != "series" || c.Navigation.EntityID != c.ReadingGroup.SeriesID {
		return nil, fmt.Errorf("invalid Reading group navigation")
	}
	pivot := ""
	switch c.ReadingGroup.GroupKind {
	case ReadingSavedSeries:
	case ReadingSavedMagazine:
		pivot = "magazines"
	case ReadingSavedNewspaper:
		pivot = "newspapers"
	default:
		return nil, fmt.Errorf("invalid Reading group kind")
	}
	if c.Navigation.Pivot != pivot {
		return nil, fmt.Errorf("invalid Reading group pivot")
	}
	type plain SavedReadingGroupCard
	return json.Marshal(plain(c))
}

func (SavedReadingGroupCard) JSONSchema() map[string]any {
	text := func(min, max int) map[string]any {
		return map[string]any{"type": "string", "minLength": min, "maxLength": max}
	}
	series := map[string]any{"type": "string", "minLength": 43, "maxLength": 43, "pattern": "^[A-Za-z0-9_-]{43}$"}
	common := func(hidden bool) map[string]any {
		return map[string]any{"id": text(1, 128), "kind": map[string]any{"type": "string", "enum": []string{"reading_group"}}, "hidden": map[string]any{"type": "boolean", "enum": []bool{hidden}}, "revision": map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991}, "removeMyList": map[string]any{"type": "boolean"}}
	}
	hidden := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "kind", "hidden", "revision", "removeMyList"}, "properties": common(true)}
	properties := common(false)
	properties["libraryId"] = text(1, 128)
	properties["readingGroup"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"groupKind", "seriesId"}, "properties": map[string]any{"groupKind": (ReadingSavedGroupKind("")).JSONSchema(), "seriesId": series}}
	properties["title"] = text(1, 4096)
	properties["count"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991}
	properties["posterUrl"] = text(1, 4096)
	properties["navigation"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"view", "entityId"}, "properties": map[string]any{"view": map[string]any{"type": "string", "enum": []string{"series"}}, "entityId": series, "pivot": map[string]any{"type": "string", "enum": []string{"magazines", "newspapers"}}}}
	visible := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "kind", "hidden", "revision", "removeMyList", "libraryId", "readingGroup", "title", "count", "navigation"}, "properties": properties}
	return map[string]any{"oneOf": []any{hidden, visible}}
}

type SavedCard struct {
	Entry        *ContentEntry
	ReadingGroup *SavedReadingGroupCard
}

func (c SavedCard) MarshalJSON() ([]byte, error) {
	if c.Entry != nil && c.ReadingGroup == nil {
		return json.Marshal(c.Entry)
	}
	if c.ReadingGroup != nil && c.Entry == nil {
		return json.Marshal(c.ReadingGroup)
	}
	return nil, fmt.Errorf("invalid Saved card alternative")
}
func (SavedCard) JSONSchema() map[string]any {
	ordinary, err := apikit.Schema(reflect.TypeFor[ContentEntry]())
	if err != nil {
		panic(err)
	}
	// This is only the Saved wire union. The global ContentEntry vocabulary,
	// generic item effects, header entities and selection grants are unchanged.
	ordinary["properties"].(map[string]any)["kind"] = map[string]any{"type": "string", "enum": []string{"ebook", "comic", "movie", "show", "season", "episode", "artist", "album", "song", "book", "audiobook_file", "chapter", "collection", "category", "author", "book_series", "disc", "extra", "playlist"}}
	return map[string]any{"oneOf": []any{ordinary, (SavedReadingGroupCard{}).JSONSchema()}}
}
