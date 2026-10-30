package serverreturn

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

type Exchange struct {
	Code            string `json:"code"`
	TransactionID   string `json:"transactionId"`
	Verifier        string `json:"verifier"`
	RegistrationID  string `json:"registrationId"`
	ServerChallenge string `json:"serverChallenge"`
	InstallationID  string `json:"installationId"`
	Purpose         string `json:"purpose"`
}

func (q Exchange) Valid() bool {
	return secret.MatchString(q.Code) && identifier.MatchString(q.TransactionID) && secret.MatchString(q.Verifier) && identifier.MatchString(q.RegistrationID) && secret.MatchString(q.ServerChallenge) && secret.MatchString(q.InstallationID) && Purpose(q.Purpose)
}
func (Exchange) String() string     { return "[server return redacted]" }
func (q Exchange) GoString() string { return q.String() }

type exchangeShape Exchange

func (Exchange) JSONSchema() map[string]any {
	shape, e := apikit.Schema(reflect.TypeFor[exchangeShape]())
	if e != nil {
		panic(e)
	}
	props := shape["properties"].(map[string]any)
	props["purpose"] = map[string]any{"type": "string", "enum": []string{"setup", "switch", "change-owner", "sign-in"}}
	for _, field := range []string{"code", "verifier", "serverChallenge", "installationId"} {
		props[field] = map[string]any{"type": "string", "minLength": 32, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"}
	}
	return shape
}
