package segmentmarkers

import (
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"reflect"
)

type markerWire Marker

func (v Marker) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(markerWire(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
func (Marker) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[markerWire](), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
func (v *Marker) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*markerWire)(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
