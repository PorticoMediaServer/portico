package serverreturn

import (
	"encoding/json"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/wirejson"
)

// SetupExchange is a distinct transport, never a code-less ordinary Exchange. It names no
// return registration: setup finishes at claim install, before one exists (O-163).
// It grants no authority: the handler must capture the installed server token
// and the store must compare its exact live tuple with the frozen owner row.
type SetupExchange struct {
	TransactionID   string `json:"transactionId"`
	State           string `json:"state"`
	Verifier        string `json:"verifier"`
	InstallationID  string `json:"installationId"`
	ServerChallenge string `json:"serverChallenge"`
	Purpose         string `json:"purpose"`
}

func (q SetupExchange) Valid() bool {
	return identifier.MatchString(q.TransactionID) && secret.MatchString(q.State) && secret.MatchString(q.Verifier) && secret.MatchString(q.InstallationID) && secret.MatchString(q.ServerChallenge) && q.Purpose == "setup"
}
func (SetupExchange) String() string   { return "[setup exchange redacted]" }
func (SetupExchange) GoString() string { return "[setup exchange redacted]" }

type setupExchangeShape SetupExchange

func (q *SetupExchange) UnmarshalJSON(raw []byte) error {
	*q = SetupExchange{}
	fields, e := wirejson.Object(raw, "transactionId", "state", "verifier", "installationId", "serverChallenge", "purpose")
	var next setupExchangeShape
	if e != nil || len(fields) != 6 || json.Unmarshal(raw, &next) != nil || !SetupExchange(next).Valid() {
		return ErrInvalid
	}
	*q = SetupExchange(next)
	return nil
}

func (SetupExchange) JSONSchema() map[string]any {
	shape, e := apikit.Schema(reflect.TypeFor[setupExchangeShape]())
	if e != nil {
		panic(e)
	}
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["purpose"] = map[string]any{"type": "string", "const": "setup"}
	for _, field := range []string{"state", "verifier", "installationId", "serverChallenge"} {
		props[field] = map[string]any{"type": "string", "minLength": 32, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"}
	}
	for _, field := range []string{"transactionId"} {
		props[field] = map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"}
	}
	return shape
}
