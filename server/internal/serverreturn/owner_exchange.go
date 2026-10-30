package serverreturn

import (
	"encoding/json"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/wirejson"
)

// OwnerExchange is a distinct transport, never a code-less ordinary Exchange.
// It grants no authority: the handler must capture the installed server token
// and the store must compare its exact live tuple with the frozen owner row.
type OwnerExchange struct {
	TransactionID   string `json:"transactionId"`
	State           string `json:"state"`
	Verifier        string `json:"verifier"`
	RegistrationID  string `json:"registrationId"`
	InstallationID  string `json:"installationId"`
	ServerChallenge string `json:"serverChallenge"`
	Purpose         string `json:"purpose"`
}

func (q OwnerExchange) Valid() bool {
	return identifier.MatchString(q.TransactionID) && secret.MatchString(q.State) && secret.MatchString(q.Verifier) && identifier.MatchString(q.RegistrationID) && secret.MatchString(q.InstallationID) && secret.MatchString(q.ServerChallenge) && q.Purpose == "switch"
}
func (OwnerExchange) String() string   { return "[owner exchange redacted]" }
func (OwnerExchange) GoString() string { return "[owner exchange redacted]" }

type ownerExchangeShape OwnerExchange

func (q *OwnerExchange) UnmarshalJSON(raw []byte) error {
	*q = OwnerExchange{}
	fields, e := wirejson.Object(raw, "transactionId", "state", "verifier", "registrationId", "installationId", "serverChallenge", "purpose")
	var next ownerExchangeShape
	if e != nil || len(fields) != 7 || json.Unmarshal(raw, &next) != nil || !OwnerExchange(next).Valid() {
		return ErrInvalid
	}
	*q = OwnerExchange(next)
	return nil
}

func (OwnerExchange) JSONSchema() map[string]any {
	shape, e := apikit.Schema(reflect.TypeFor[ownerExchangeShape]())
	if e != nil {
		panic(e)
	}
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["purpose"] = map[string]any{"type": "string", "const": "switch"}
	for _, field := range []string{"state", "verifier", "installationId", "serverChallenge"} {
		props[field] = map[string]any{"type": "string", "minLength": 32, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"}
	}
	for _, field := range []string{"transactionId", "registrationId"} {
		props[field] = map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"}
	}
	return shape
}

// ExchangeRequest holds exactly one flat wire variant. Selecting Owner here
// only selects a parser, never an authentication policy or a callback branch.
// Normal Exchange's code requirement and auth/cookie refusal remain mandatory.
type ExchangeRequest struct {
	Setup      *SetupExchange `json:"-"`
	Registered *Exchange      `json:"-"`
	Owner      *OwnerExchange `json:"-"`
}

func (ExchangeRequest) String() string   { return "[server return request redacted]" }
func (ExchangeRequest) GoString() string { return "[server return request redacted]" }

func (q *ExchangeRequest) UnmarshalJSON(raw []byte) error {
	*q = ExchangeRequest{}
	fields, e := wirejson.Object(raw, "transactionId", "code", "state", "verifier", "registrationId", "installationId", "serverChallenge", "purpose")
	if e != nil {
		return ErrInvalid
	}
	if _, hasState := fields["state"]; hasState {
		var purpose string
		if json.Unmarshal(fields["purpose"], &purpose) != nil {
			return ErrInvalid
		}
		// Setup names no registration (six fields, O-163); its own decoder is exact.
		if purpose == "setup" {
			var setup SetupExchange
			if json.Unmarshal(raw, &setup) != nil {
				return ErrInvalid
			}
			q.Setup = &setup
			return nil
		}
		var owner OwnerExchange
		if len(fields) != 7 || json.Unmarshal(raw, &owner) != nil {
			return ErrInvalid
		}
		q.Owner = &owner
		return nil
	}
	var normal Exchange
	if len(fields) != 7 || json.Unmarshal(raw, &normal) != nil || !normal.Valid() {
		return ErrInvalid
	}
	q.Registered = &normal
	return nil
}

func (q ExchangeRequest) MarshalJSON() ([]byte, error) {
	variants := 0
	if q.Owner != nil {
		variants++
	}
	if q.Setup != nil {
		variants++
	}
	if q.Registered != nil {
		variants++
	}
	if variants != 1 {
		return nil, ErrInvalid
	}
	if q.Setup != nil {
		if !q.Setup.Valid() {
			return nil, ErrInvalid
		}
		return json.Marshal(q.Setup)
	}
	if q.Owner != nil {
		if !q.Owner.Valid() {
			return nil, ErrInvalid
		}
		return json.Marshal(q.Owner)
	}
	if !q.Registered.Valid() {
		return nil, ErrInvalid
	}
	return json.Marshal(q.Registered)
}

func (ExchangeRequest) JSONSchema() map[string]any {
	normal := (Exchange{}).JSONSchema()
	normal["additionalProperties"] = false
	return map[string]any{"oneOf": []any{normal, (OwnerExchange{}).JSONSchema(), (SetupExchange{}).JSONSchema()}}
}
