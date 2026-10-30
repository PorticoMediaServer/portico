package catalog

import (
	"encoding/json"
	"fmt"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"reflect"
)

type itemWire Item

func (v Item) MarshalJSON() ([]byte, error) {
	ms := math.Round(v.Duration * 1000)
	if !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms >= 0 && ms <= 9007199254740991 {
		return apiwire.MarshalDurations(itemWire(v), map[string]string{"duration": "durationMs", "progressSeconds": "progressMs"})
	}
	// The accepted physical scalar is retained in storage. JSON cannot express
	// it in the millisecond contract; report unknown instead of zero/truncation.
	v.Duration = 0
	raw, e := apiwire.MarshalDurations(itemWire(v), map[string]string{"duration": "durationMs", "progressSeconds": "progressMs"})
	if e != nil {
		return nil, e
	}
	var object map[string]json.RawMessage
	if e = json.Unmarshal(raw, &object); e != nil {
		return nil, e
	}
	delete(object, "durationMs")
	object["durationState"] = json.RawMessage(`"unrepresentable"`)
	return json.Marshal(object)
}
func (Item) JSONSchema() map[string]any {
	schema := apiwire.DurationSchema(reflect.TypeFor[itemWire](), map[string]string{"duration": "durationMs", "progressSeconds": "progressMs"})
	// One object: durationMs is present when representable, otherwise absent
	// beside durationState "unrepresentable".
	schema["properties"].(map[string]any)["durationState"] = map[string]any{"type": "string", "enum": []string{"unrepresentable"}}
	required := []string{}
	for _, key := range schema["required"].([]string) {
		if key != "durationMs" {
			required = append(required, key)
		}
	}
	schema["required"] = required
	return schema
}

type contentEntryWire ContentEntry

func (ContentEntry) JSONSchema() map[string]any {
	kind := reflect.TypeFor[contentEntryWire]()
	fields := []reflect.StructField{}
	for i := 0; i < kind.NumField(); i++ {
		f := kind.Field(i)
		if f.Name != "Media" && f.Name != "Hidden" {
			fields = append(fields, f)
		}
	}
	leaf := apiwire.DurationSchema(reflect.StructOf(fields), map[string]string{"duration": "durationMs", "progressSeconds": "progressMs"})
	leafProperties := leaf["properties"].(map[string]any)
	leafProperties["userRating"] = map[string]any{"type": "integer", "nullable": true, "minimum": 0, "maximum": 10}
	properties := map[string]any{}
	for key, value := range leafProperties {
		properties[key] = value
	}
	properties["hidden"] = map[string]any{"type": "boolean", "nullable": true}
	properties["media"] = leaf
	// Playlist occurrences carry id/kind/hidden/media instead of a title.
	// Kind-specific requirements are enforced by the shared content parser.
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "kind"}, "properties": properties}
}

type contentPlaybackWire ContentPlayback

func (v ContentPlayback) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(contentPlaybackWire(v), map[string]string{"startSeconds": "startMs"})
}
func (ContentPlayback) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[contentPlaybackWire](), map[string]string{"startSeconds": "startMs"})
}

type personalStateWire PersonalState

func (v PersonalState) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(personalStateWire(v), map[string]string{"progressSeconds": "progressMs"})
}
func (PersonalState) JSONSchema() map[string]any {
	schema := apiwire.DurationSchema(reflect.TypeFor[personalStateWire](), map[string]string{"progressSeconds": "progressMs"})
	schema["properties"].(map[string]any)["rating"] = map[string]any{"type": "integer", "nullable": true, "minimum": 0, "maximum": 10}
	return schema
}
func (v *PersonalState) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*personalStateWire)(v), map[string]string{"progressSeconds": "progressMs"})
}

type personalMutationWire PersonalMutation

func (v PersonalMutation) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(personalMutationWire(v), map[string]string{"progressSeconds": "progressMs"})
}
func (PersonalMutation) JSONSchema() map[string]any {
	schema := apiwire.DurationSchema(reflect.TypeFor[personalMutationWire](), map[string]string{"progressSeconds": "progressMs"})
	schema["properties"].(map[string]any)["rating"] = map[string]any{"type": "integer", "nullable": true, "minimum": 0, "maximum": 10}
	return schema
}
func (v *PersonalMutation) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*personalMutationWire)(v), map[string]string{"progressSeconds": "progressMs"})
}

type titleFileWire TitleFile

func (v TitleFile) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(titleFileWire(v), map[string]string{"duration": "durationMs"})
}
func (TitleFile) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[titleFileWire](), map[string]string{"duration": "durationMs"})
}

type bookResumeWire BookResume

func (v BookResume) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(bookResumeWire(v), map[string]string{"positionSeconds": "positionMs"})
}
func (BookResume) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[bookResumeWire](), map[string]string{"positionSeconds": "positionMs"})
}

type bookChapterWire BookChapter

func (v BookChapter) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(bookChapterWire(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
func (BookChapter) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[bookChapterWire](), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}

type playHistoryEntryWire PlayHistoryEntry

func (v PlayHistoryEntry) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(playHistoryEntryWire(v), map[string]string{"positionSeconds": "positionMs", "durationSeconds": "durationMs"})
}
func (PlayHistoryEntry) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[playHistoryEntryWire](), map[string]string{"positionSeconds": "positionMs", "durationSeconds": "durationMs"})
}

type historyEntryWire HistoryEntry

func (v HistoryEntry) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(historyEntryWire(v), map[string]string{"positionSeconds": "positionMs"})
}
func (HistoryEntry) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[historyEntryWire](), map[string]string{"positionSeconds": "positionMs"})
}

type playSummaryWire PlaySummary

func (v PlaySummary) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(playSummaryWire(v), nil)
}
func (PlaySummary) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[playSummaryWire](), nil)
}

func (v *Item) UnmarshalJSON(raw []byte) error {
	// An item read is tagged variant "item" (R-09b); a Go reader of that reply
	// accepts its own tag and nothing else in its place.
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		if tag, ok := object["variant"]; ok {
			if string(tag) != `"item"` {
				return fmt.Errorf("item variant %s", tag)
			}
			delete(object, "variant")
			if raw, err = json.Marshal(object); err != nil {
				return err
			}
		}
	}
	return apiwire.UnmarshalDurations(raw, (*itemWire)(v), map[string]string{"duration": "durationMs", "progressSeconds": "progressMs"})
}

func (v *ContentEntry) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*contentEntryWire)(v), map[string]string{"duration": "durationMs", "progressSeconds": "progressMs"})
}

func (v *ContentPlayback) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*contentPlaybackWire)(v), map[string]string{"startSeconds": "startMs"})
}

func (v *TitleFile) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*titleFileWire)(v), map[string]string{"duration": "durationMs"})
}

func (v *BookResume) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*bookResumeWire)(v), map[string]string{"positionSeconds": "positionMs"})
}

func (v *BookChapter) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*bookChapterWire)(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}

func (v *PlayHistoryEntry) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*playHistoryEntryWire)(v), map[string]string{"positionSeconds": "positionMs", "durationSeconds": "durationMs"})
}

func (v *HistoryEntry) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*historyEntryWire)(v), map[string]string{"positionSeconds": "positionMs"})
}

func (v *PlaySummary) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*playSummaryWire)(v), nil)
}

type titleRowWire TitleRow

func (v TitleRow) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(titleRowWire(v), map[string]string{"durationSeconds": "durationMs"})
}
func (TitleRow) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[titleRowWire](), map[string]string{"durationSeconds": "durationMs"})
}
func (v *TitleRow) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*titleRowWire)(v), map[string]string{"durationSeconds": "durationMs"})
}

type librarySourceWire LibrarySource

func (v LibrarySource) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(librarySourceWire(v), map[string]string{"intervalSeconds": "intervalMs"})
}
func (LibrarySource) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[librarySourceWire](), map[string]string{"intervalSeconds": "intervalMs"})
}
func (v *LibrarySource) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*librarySourceWire)(v), map[string]string{"intervalSeconds": "intervalMs"})
}

type sourceSettingsWire SourceSettings

func (v SourceSettings) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(sourceSettingsWire(v), map[string]string{"intervalSeconds": "intervalMs"})
}
func (SourceSettings) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[sourceSettingsWire](), map[string]string{"intervalSeconds": "intervalMs"})
}
func (v *SourceSettings) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*sourceSettingsWire)(v), map[string]string{"intervalSeconds": "intervalMs"})
}

type trickplaySettingsWire TrickplaySettings

func (v TrickplaySettings) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(trickplaySettingsWire(v), map[string]string{"intervalSeconds": "intervalMs"})
}
func (TrickplaySettings) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[trickplaySettingsWire](), map[string]string{"intervalSeconds": "intervalMs"})
}
func (v *TrickplaySettings) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*trickplaySettingsWire)(v), map[string]string{"intervalSeconds": "intervalMs"})
}
