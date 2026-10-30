// Package contract records real handler responses for generated decoder checks.
package contract

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
)

type Fixture struct {
	Schema   string          `json:"schema"`
	Method   string          `json:"method"`
	Path     string          `json:"path"`
	Auth     string          `json:"auth,omitempty"`
	Request  json.RawMessage `json:"request,omitempty"`
	Status   int             `json:"status"`
	Response json.RawMessage `json:"response"`
}

func Record(path, schema, method, route string, request []byte, response *httptest.ResponseRecorder, auth ...string) error {
	if response.Code < 200 || response.Code >= 300 || !json.Valid(response.Body.Bytes()) {
		return errors.New("fixture requires a successful JSON handler response")
	}
	fixture := Fixture{Schema: schema, Method: method, Path: route, Request: request, Status: response.Code, Response: response.Body.Bytes()}
	if len(auth) > 0 {
		fixture.Auth = auth[0]
	}
	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0644)
}
