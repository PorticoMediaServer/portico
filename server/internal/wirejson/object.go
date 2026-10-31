// Package wirejson parses the JSON that crosses Portico's transports: the closed
// objects used by owner handoff, and Hosted's plain responses, which read open.
package wirejson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var ErrObject = errors.New("invalid request object")

// Object rejects unknown, duplicate and case-aliased keys, non-objects and
// trailing values. Nested objects remain raw so their own transport can apply
// its exact key set. Errors never include request values.
func Object(raw []byte, names ...string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > 32<<10 {
		return nil, ErrObject
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, e := d.Token(); e != nil || token != json.Delim('{') {
		return nil, ErrObject
	}
	fields := make(map[string]json.RawMessage, len(names))
	for d.More() {
		token, e := d.Token()
		key, ok := token.(string)
		if e != nil || !ok || !allowed[key] || fields[key] != nil {
			return nil, ErrObject
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, ErrObject
		}
		fields[key] = value
	}
	if token, e := d.Token(); e != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return nil, ErrObject
	}
	return fields, nil
}
