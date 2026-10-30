package catalog

import (
	"encoding/json"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

type SavedSection struct {
	ContentSection
	Cards []SavedCard `json:"-"`
}

func (s SavedSection) MarshalJSON() ([]byte, error) {
	type section ContentSection
	base, err := json.Marshal(section(s.ContentSection))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(base, &fields); err != nil {
		return nil, err
	}
	entries, err := json.Marshal(s.Cards)
	if err != nil {
		return nil, err
	}
	fields["entries"] = entries
	return json.Marshal(fields)
}
func (SavedSection) JSONSchema() map[string]any {
	schema, err := apikit.Schema(reflect.TypeFor[ContentSection]())
	if err != nil {
		panic(err)
	}
	schema["properties"].(map[string]any)["entries"] = map[string]any{"type": "array", "maxItems": 100, "items": (SavedCard{}).JSONSchema()}
	return schema
}

type SavedContentEnvelope struct {
	ContentEnvelope
	SavedRevision int64          `json:"savedRevision,omitempty"`
	SavedSections []SavedSection `json:"-"`
}

func (s SavedContentEnvelope) MarshalJSON() ([]byte, error) {
	type envelope ContentEnvelope
	base, err := json.Marshal(envelope(s.ContentEnvelope))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(base, &fields); err != nil {
		return nil, err
	}
	if s.SavedSections != nil {
		sections, err := json.Marshal(s.SavedSections)
		if err != nil {
			return nil, err
		}
		fields["sections"] = sections
	}
	if s.SavedRevision != 0 {
		revision, err := json.Marshal(s.SavedRevision)
		if err != nil {
			return nil, err
		}
		fields["savedRevision"] = revision
	}
	return json.Marshal(fields)
}
func (SavedContentEnvelope) JSONSchema() map[string]any {
	schema, err := apikit.Schema(reflect.TypeFor[ContentEnvelope]())
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	properties["sections"] = map[string]any{"type": "array", "maxItems": 100, "items": (SavedSection{}).JSONSchema()}
	properties["savedRevision"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991}
	return schema
}
