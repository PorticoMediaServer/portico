package mediaanalysis

import (
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"reflect"
)

type trickplaySetWire TrickplaySet

func (v TrickplaySet) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(trickplaySetWire(v), map[string]string{"intervalSeconds": "intervalMs", "durationSeconds": "durationMs", "sourceOffsetSeconds": "sourceOffsetMs"})
}
func (TrickplaySet) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[trickplaySetWire](), map[string]string{"intervalSeconds": "intervalMs", "durationSeconds": "durationMs", "sourceOffsetSeconds": "sourceOffsetMs"})
}
func (v *TrickplaySet) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*trickplaySetWire)(v), map[string]string{"intervalSeconds": "intervalMs", "durationSeconds": "durationMs", "sourceOffsetSeconds": "sourceOffsetMs"})
}
