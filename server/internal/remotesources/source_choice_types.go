package remotesources

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

// SourceStartChoice names one explicit immutable definition. The enclosing
// command still carries the original source generation, read revision,
// operation id and complete read values; those values must match this choice.
type SourceStartChoice struct {
	Name                            string `json:"name"`
	DefinitionRevision              int64  `json:"definitionRevision"`
	ExpectedAdvancedRevision        int64  `json:"expectedAdvancedRevision"`
	ExpectedConfigurationGeneration string `json:"expectedConfigurationGeneration"`
}

// SourceChoiceDefinition contains product defaults, never measured effective
// limits or authority. A provider's inapplicable fields are preserved by apply.
type SourceChoiceDefinition struct {
	Name               string           `json:"name"`
	DefinitionRevision int64            `json:"definitionRevision"`
	Reading            SourceReadValues `json:"reading"`
	Advanced           SourceAdvanced   `json:"advanced"`
}

// SourceChoiceBaseline is emitted only for an exact coherent recorded source
// association and definition. CurrentAdvanced is read in the same snapshot as
// the enclosing current reading values. It is desired configuration, not an
// assertion that the cache/transport has applied. Counts derive solely from
// ChangedFields, with each applicable editable row present at most once.
type SourceChoiceBaseline struct {
	SourceID                string           `json:"sourceId"`
	Generation              string           `json:"generation"`
	ConfigurationGeneration string           `json:"configurationGeneration"`
	Name                    string           `json:"name"`
	DefinitionRevision      int64            `json:"definitionRevision"`
	ReadRevision            int64            `json:"readRevision"`
	AdvancedRevision        int64            `json:"advancedRevision"`
	Reading                 SourceReadValues `json:"reading"`
	Advanced                SourceAdvanced   `json:"advanced"`
	CurrentAdvanced         SourceAdvanced   `json:"currentAdvanced"`
	ApplicableFields        []string         `json:"applicableFields"`
	ChangedFields           []string         `json:"changedFields"`
}

// Keep this closed list aligned with the twenty-three authored Root57/64 rows.
// Display facts, credentials, identity and library/server policy are excluded.
var sourceChoiceWireFields = [...]string{
	"scanDepth", "connections", "requestsPerSecond", "bytesPerSecond",
	"scanIntervalSeconds", "watchForChanges", "scanOnStart", "fullRecheckDays", "doubleCheckFolders",
	"seekPreviews", "chapterImages", "waveforms", "loudness", "checksums", "introsMode",
	"cacheBytes", "cacheFloorBytes", "dirCacheSeconds", "readBufferBytes", "connectTimeoutSeconds", "idleTimeoutSeconds", "retries", "lowLevelRetries",
}

func sourceChoiceSchema(t reflect.Type) map[string]any {
	shape, _ := apikit.Schema(t)
	shape["additionalProperties"] = false
	props, _ := shape["properties"].(map[string]any)
	if props == nil {
		return shape
	}
	if field, ok := props["name"].(map[string]any); ok {
		field["enum"] = []string{"list", "once", "everything", "ask"}
	}
	for _, name := range []string{"definitionRevision", "readRevision", "advancedRevision"} {
		if field, ok := props[name].(map[string]any); ok {
			field["minimum"] = 1
			field["maximum"] = int64(9007199254740991)
		}
	}
	if field, ok := props["expectedAdvancedRevision"].(map[string]any); ok {
		field["minimum"] = 0
		field["maximum"] = int64(9007199254740991)
	}
	if field, ok := props["sourceId"].(map[string]any); ok {
		field["minLength"] = 1
		field["maxLength"] = 160
	}
	for _, name := range []string{"generation", "configurationGeneration", "expectedConfigurationGeneration"} {
		if field, ok := props[name].(map[string]any); ok {
			field["pattern"] = "^[1-9][0-9]{0,18}$"
		}
	}
	if _, ok := props["reading"]; ok {
		reading, _ := apikit.Schema(reflect.TypeFor[SourceReadValues]())
		if values, ok := reading["properties"].(map[string]any); ok {
			// Consent is not a preset value. The retained legacy field is inert
			// compatibility data in this definition, never an editable row.
			delete(values, "allowAdvancedMediaAnalysis")
			if legacy, ok := values["wholeFileReads"].(map[string]any); ok {
				legacy["const"] = false
			}
			if depth, ok := values["scanDepth"].(map[string]any); ok {
				depth["enum"] = []string{"file_list_only", "basic", "complete"}
			}
		}
		props["reading"] = reading
	}
	for _, name := range []string{"advanced", "currentAdvanced"} {
		if _, ok := props[name]; ok {
			advanced := (SourceAdvanced{}).JSONSchema()
			// Baselines are full values, even though ordinary legacy Advanced writes
			// may omit the newer fields and preserve their original values.
			advanced["required"] = append([]string(nil), sourceChoiceWireFields[4:]...)
			props[name] = advanced
		}
	}
	for _, name := range []string{"applicableFields", "changedFields"} {
		if _, ok := props[name]; ok {
			props[name] = map[string]any{"type": "array", "maxItems": len(sourceChoiceWireFields), "uniqueItems": true,
				"items": map[string]any{"type": "string", "enum": append([]string(nil), sourceChoiceWireFields[:]...)}}
		}
	}
	return shape
}
func (SourceStartChoice) JSONSchema() map[string]any {
	type wire SourceStartChoice
	return sourceChoiceSchema(reflect.TypeFor[wire]())
}
func (SourceChoiceDefinition) JSONSchema() map[string]any {
	type wire SourceChoiceDefinition
	return sourceChoiceSchema(reflect.TypeFor[wire]())
}
func (SourceChoiceBaseline) JSONSchema() map[string]any {
	type wire SourceChoiceBaseline
	return sourceChoiceSchema(reflect.TypeFor[wire]())
}

// The additive choice facts are bounded and optional on the existing document;
// an older/unknown definition cannot manufacture a legacy baseline.
func (SourceReadSettings) JSONSchema() map[string]any {
	type wire SourceReadSettings
	shape := readSettingsWireSchema[wire]()
	props, _ := shape["properties"].(map[string]any)
	if props == nil {
		return shape
	}
	props["sourceId"].(map[string]any)["minLength"] = 1
	props["generation"].(map[string]any)["minLength"] = 1
	props["revision"].(map[string]any)["minimum"] = 0
	if definitions, ok := props["choiceDefinitions"].(map[string]any); ok {
		definitions["minItems"] = 4
		definitions["maxItems"] = 4
	}
	if _, ok := props["choiceApplicableFields"]; ok {
		props["choiceApplicableFields"] = map[string]any{"type": "array", "minItems": 1, "maxItems": len(sourceChoiceWireFields), "uniqueItems": true,
			"items": map[string]any{"type": "string", "enum": append([]string(nil), sourceChoiceWireFields[:]...)}}
	}
	if field, ok := props["configurationGeneration"].(map[string]any); ok {
		field["pattern"] = "^[1-9][0-9]{0,18}$"
	}
	return shape
}
