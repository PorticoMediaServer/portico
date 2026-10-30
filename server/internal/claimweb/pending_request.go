package claimweb

import (
	"encoding/json"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/wirejson"
)

// PendingRequest is only the pending-registration transport. Events and the
// original Signed/Request wire formats remain unchanged. Runtime registration
// must verify and atomically freeze both complete envelopes before handoff.
type PendingRequest struct {
	Payload          string  `json:"payload"`
	Signature        string  `json:"signature"`
	SetupStart       *Signed `json:"setupStart,omitempty"`
	OwnerStart       *Signed `json:"ownerStart,omitempty"`
	OwnerSwitchCount *Signed `json:"ownerSwitchCount,omitempty"`
}

func (q PendingRequest) Original() Signed { return Signed{Payload: q.Payload, Signature: q.Signature} }
func (PendingRequest) String() string     { return "[pending claim redacted]" }
func (PendingRequest) GoString() string   { return "[pending claim redacted]" }

func (q *PendingRequest) UnmarshalJSON(raw []byte) error {
	*q = PendingRequest{}
	fields, e := wirejson.Object(raw, "payload", "signature", "ownerStart", "ownerSwitchCount", "setupStart")
	if e != nil {
		return ErrInvalid
	}
	original, e := pendingSigned(fields)
	if e != nil {
		return e
	}
	next := PendingRequest{Payload: original.Payload, Signature: original.Signature}
	if rawStart, ok := fields["ownerStart"]; ok {
		startFields, e := wirejson.Object(rawStart, "payload", "signature")
		if e != nil {
			return ErrInvalid
		}
		start, e := pendingSigned(startFields)
		if e != nil {
			return e
		}
		next.OwnerStart = &start
	}
	if rawCount, ok := fields["ownerSwitchCount"]; ok {
		if next.OwnerStart == nil {
			return ErrInvalid
		}
		countFields, e := wirejson.Object(rawCount, "payload", "signature")
		if e != nil {
			return ErrInvalid
		}
		count, e := pendingSigned(countFields)
		if e != nil {
			return e
		}
		next.OwnerSwitchCount = &count
	}
	if rawStart, ok := fields["setupStart"]; ok {
		if next.OwnerStart != nil || next.OwnerSwitchCount != nil {
			return ErrInvalid
		}
		startFields, e := wirejson.Object(rawStart, "payload", "signature")
		if e != nil {
			return ErrInvalid
		}
		start, e := pendingSigned(startFields)
		if e != nil {
			return e
		}
		next.SetupStart = &start
	}
	*q = next
	return nil
}

func pendingSigned(fields map[string]json.RawMessage) (Signed, error) {
	var s Signed
	if json.Unmarshal(fields["payload"], &s.Payload) != nil || s.Payload == "" || json.Unmarshal(fields["signature"], &s.Signature) != nil || s.Signature == "" {
		return Signed{}, ErrInvalid
	}
	return s, nil
}

// JSONSchema distinguishes an absent start from a required complete envelope;
// null must not be normalized into the ordinary pending branch.
func (PendingRequest) JSONSchema() map[string]any {
	signed, e := apikit.Schema(reflect.TypeFor[Signed]())
	if e != nil {
		panic(e)
	}
	signed["additionalProperties"] = false
	for _, field := range []string{"payload", "signature"} {
		signed["properties"].(map[string]any)[field] = map[string]any{"type": "string", "minLength": 1}
	}
	props := map[string]any{}
	for name, field := range signed["properties"].(map[string]any) {
		props[name] = field
	}
	props["ownerStart"] = signed
	props["ownerSwitchCount"] = signed
	owner := map[string]any{"type": "object", "properties": props, "required": []string{"payload", "signature", "ownerStart"}, "additionalProperties": false}
	setupProps := map[string]any{"payload": signed["properties"].(map[string]any)["payload"], "signature": signed["properties"].(map[string]any)["signature"], "setupStart": signed}
	setup := map[string]any{"type": "object", "properties": setupProps, "required": []string{"payload", "signature", "setupStart"}, "additionalProperties": false}
	return map[string]any{"oneOf": []any{signed, owner, setup}}
}
