// Package apiwire defines units at the HTTP boundary. Persistence and media
// engines keep their own native units; every public instant is RFC 3339 UTC.
package apiwire

import (
	"encoding/json"
	"fmt"
	"time"
)

// Instant stores Unix milliseconds internally and exchanges RFC 3339 UTC.
// The empty string represents an absent optional instant, never an epoch date.
type Instant int64

func (v Instant) MarshalJSON() ([]byte, error) {
	if v == 0 {
		return json.Marshal("")
	}
	return json.Marshal(time.UnixMilli(int64(v)).UTC().Format(time.RFC3339Nano))
}
func (v *Instant) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return err
	}
	if text == "" {
		*v = 0
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	*v = Instant(t.UnixMilli())
	return nil
}
func (Instant) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "description": "RFC 3339 UTC instant; empty when absent."}
}
