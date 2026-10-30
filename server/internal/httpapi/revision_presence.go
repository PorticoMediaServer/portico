package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
)

var errRevisionRequired = errors.New("revision required")

// Opted-in legacy families retain their numeric CAS contract, including an
// explicitly supplied zero. Absence and null must not silently become zero.
func requireRevisionJSON(raw []byte, value any) error {
	typ := reflect.TypeOf(value)
	if typ == nil || typ.Kind() != reflect.Pointer || typ.Elem().Kind() != reflect.Struct {
		return nil
	}
	field, ok := typ.Elem().FieldByName("ExpectedRevision")
	if !ok || field.Type.Kind() != reflect.Int64 || strings.Split(field.Tag.Get("json"), ",")[0] != "expectedRevision" {
		return nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return errStrictJSON
	}
	revision, ok := body["expectedRevision"]
	if !ok || bytes.Equal(bytes.TrimSpace(revision), []byte("null")) {
		return errRevisionRequired
	}
	return nil
}

func decodeLegacyRevision(w http.ResponseWriter, r *http.Request, value any) error {
	var raw json.RawMessage
	if err := decode(w, r, &raw); err != nil {
		return errStrictJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errStrictJSON
	}
	return requireRevisionJSON(raw, value)
}

func revisionFailure(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, errRevisionRequired) {
		return false
	}
	policyError(w, "revision_required")
	return true
}
