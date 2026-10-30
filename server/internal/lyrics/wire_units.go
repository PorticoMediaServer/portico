package lyrics

import (
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"reflect"
)

type sourceWire Source

func (v Source) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(sourceWire(v), map[string]string{"startSeconds": "startMs", "duration": "durationMs", "sourceDuration": "sourceDurationMs"})
}
func (Source) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[sourceWire](), map[string]string{"startSeconds": "startMs", "duration": "durationMs", "sourceDuration": "sourceDurationMs"})
}
func (v *Source) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*sourceWire)(v), map[string]string{"startSeconds": "startMs", "duration": "durationMs", "sourceDuration": "sourceDurationMs"})
}
