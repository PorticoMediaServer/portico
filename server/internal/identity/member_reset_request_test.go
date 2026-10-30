package identity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemberResetRequestOriginalCodePresence(t *testing.T) {
	code := strings.Repeat("a", 43)
	for _, tc := range []struct {
		name  string
		raw   string
		ok    bool
		proof bool
	}{
		{"fresh omitted", `{"expectedRevision":7}`, true, false},
		{"explicit false", `{"expectedRevision":7,"turnOffTwoStep":false}`, true, false},
		{"replacement", `{"expectedRevision":7,"turnOffTwoStep":true,"originalResetCode":"` + code + `"}`, true, true},
		{"null", `{"expectedRevision":7,"originalResetCode":null}`, false, false},
		{"empty", `{"expectedRevision":7,"originalResetCode":""}`, false, false},
		{"wrong type", `{"expectedRevision":7,"originalResetCode":42}`, false, false},
		{"array", `{"expectedRevision":7,"originalResetCode":[]}`, false, false},
		{"malformed", `{"expectedRevision":7,"originalResetCode":"` + strings.Repeat("a", 42) + `!"}`, false, false},
		{"padded", `{"expectedRevision":7,"originalResetCode":"` + code + `="}`, false, false},
		{"space", `{"expectedRevision":7,"originalResetCode":" ` + code + `"}`, false, false},
		{"duplicate proof", `{"expectedRevision":7,"originalResetCode":"` + code + `","originalResetCode":null}`, false, false},
		{"duplicate revision", `{"expectedRevision":7,"expectedRevision":8}`, false, false},
		{"unknown", `{"expectedRevision":7,"other":true}`, false, false},
		{"null revision", `{"expectedRevision":null}`, false, false},
		{"fraction revision", `{"expectedRevision":7.5}`, false, false},
		{"missing revision", `{}`, false, false},
		{"null boolean", `{"expectedRevision":7,"turnOffTwoStep":null}`, false, false},
		{"trailing", `{"expectedRevision":7} {}`, false, false},
		{"not object", `[]`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got MemberResetRequest
			if err := json.Unmarshal([]byte(tc.raw), &got); (err == nil) != tc.ok {
				t.Fatalf("decode success=%t, want %t", err == nil, tc.ok)
			}
			if tc.ok && ((got.OriginalResetCode != nil) != tc.proof || tc.proof && *got.OriginalResetCode != code) {
				t.Fatal("replacement witness presence or value changed")
			}
		})
	}
}

func TestMemberResetRequestOriginalCodeSchema(t *testing.T) {
	shape := (MemberResetRequest{}).JSONSchema()
	properties := shape["properties"].(map[string]any)
	proof := properties["originalResetCode"].(map[string]any)
	if proof["type"] != "string" || proof["pattern"] != `^[A-Za-z0-9_-]{43}$` || proof["nullable"] == true {
		t.Fatal("replacement proof must be an exact non-null ASCII token")
	}
	if shape["additionalProperties"] != false {
		t.Fatal("request accepts undeclared keys")
	}
}
