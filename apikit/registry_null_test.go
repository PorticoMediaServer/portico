package apikit

import (
	"encoding/json"
	"strings"
	"testing"
)

type nullProbeInner struct {
	List []string `json:"list"`
}

type nullProbeRaw struct{}

func (nullProbeRaw) MarshalJSON() ([]byte, error) { return []byte(`{"x":null}`), nil }

// An always-sent pointer is the contract's one intended null ("required and
// nullable"); a nil list, map or interface is an accident the guard refuses,
// wherever it is, and it says where.
func TestResponsesRefuseAccidentalNullsOnly(t *testing.T) {
	eta := int64(5)
	for _, c := range []struct {
		name string
		v    any
		bad  string
	}{
		{"nil pointer is a declared null", struct {
			ETA *int64 `json:"etaSeconds"`
		}{}, ""},
		{"set pointer", struct {
			ETA *int64 `json:"etaSeconds"`
		}{&eta}, ""},
		{"omitted empty list", struct {
			List []string `json:"list,omitempty"`
		}{}, ""},
		{"skipped field", struct {
			List []string `json:"-"`
		}{}, ""},
		{"nil list", struct {
			Items []nullProbeInner `json:"items"`
		}{}, "response.items"},
		{"nested nil list", struct {
			Items []nullProbeInner `json:"items"`
		}{[]nullProbeInner{{List: []string{}}, {}}}, "response.items[1].list"},
		{"nil map", struct {
			M map[string]int `json:"m"`
		}{}, "response.m"},
		{"nil interface", struct {
			V any `json:"v"`
		}{}, "response.v"},
		{"embedded", struct {
			nullProbeInner
		}{}, "response.list"},
		{"marshaler producing null", struct {
			R nullProbeRaw `json:"r"`
		}{}, "response.r"},
	} {
		_, err := marshalResponse(c.v)
		switch {
		case c.bad == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.bad != "" && (err == nil || !strings.Contains(err.Error(), "response contains null at "+c.bad)):
			t.Errorf("%s: want a null at %s, got %v", c.name, c.bad, err)
		}
	}
	if raw, err := marshalResponse(struct {
		ETA *int64 `json:"etaSeconds"`
	}{}); err != nil || string(raw) != `{"etaSeconds":null}` || !json.Valid(raw) {
		t.Fatalf("declared null: %s %v", raw, err)
	}
}
