package preparedmedia

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"reflect"
)

type factsWire Facts

func (v Facts) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(factsWire(v), map[string]string{"duration": "durationMs"})
}
func (Facts) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[factsWire](), map[string]string{"duration": "durationMs"})
}
func (v *Facts) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*factsWire)(v), map[string]string{"duration": "durationMs"})
}

// Saved facts keep the engine's seconds; only the published form is in
// milliseconds, so stored versions read the same after the wire changed.
type storedFacts Facts

func (v Facts) EncodeStored() []byte {
	raw, _ := json.Marshal(storedFacts(v))
	return raw
}
func (v *Facts) DecodeStored(raw []byte) error { return json.Unmarshal(raw, (*storedFacts)(v)) }
