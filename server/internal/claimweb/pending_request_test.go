package claimweb

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestPendingRequestRetainsBothSignedProofs(t *testing.T) {
	key, claim, start, now := ownerStartFixture(t)
	original, signedStart := signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start)
	for _, owner := range []*Signed{nil, &signedStart} {
		in := PendingRequest{Payload: original.Payload, Signature: original.Signature, OwnerStart: owner}
		raw, e := json.Marshal(in)
		var got PendingRequest
		if e != nil || json.Unmarshal(raw, &got) != nil || got.Original() != original {
			t.Fatal("original signed claim was not retained", e)
		}
		if owner == nil {
			legacy, _ := json.Marshal(original)
			if string(raw) != string(legacy) || got.OwnerStart != nil {
				t.Fatal("ordinary pending bytes changed")
			}
		} else if got.OwnerStart == nil || *got.OwnerStart != signedStart {
			t.Fatal("full owner-start proof was not retained")
		} else if _, _, e := VerifyOwnerStart(got.Original(), *got.OwnerStart, claim.Audience, now); e != nil {
			t.Fatal("retained proof pair does not verify", e)
		}
		if fmt.Sprintf("%v %#v", in, in) != "[pending claim redacted] [pending claim redacted]" {
			t.Fatal("pending proof leaked through formatting")
		}
	}
}

func TestPendingRequestRejectsAmbiguousProofTransport(t *testing.T) {
	base := `"payload":"claim","signature":"signature"`
	cases := []string{
		`null`, `[]`, `{}`, `{` + base + `} {}`,
		`{` + base + `,"payload":"replacement"}`,
		`{` + base + `,"Payload":"replacement"}`,
		`{` + base + `,"claimHash":"digest-only"}`,
		`{` + base + `,"ownerStart":null}`,
		`{` + base + `,"setupStart":null}`,
		`{` + base + `,"setupStart":{}}`,
		`{` + base + `,"setupStart":{"payload":"start"}}`,
		`{` + base + `,"setupStart":{"payload":"start","signature":"signature"},"ownerStart":{"payload":"owner","signature":"signature"}}`,
		`{` + base + `,"setupStart":{"payload":"start","signature":"signature"},"ownerSwitchCount":{"payload":"count","signature":"signature"}}`,
		`{` + base + `,"ownerStart":{}}`,
		`{` + base + `,"ownerStart":{"payload":"start"}}`,
		`{` + base + `,"ownerStart":{"payload":null,"signature":"signature"}}`,
		`{` + base + `,"ownerStart":{"payload":"start","signature":"signature","signature":"other"}}`,
		`{` + base + `,"ownerStart":{"payload":"start","signature":"signature","unknown":true}}`,
		`{"payload":null,"signature":"signature"}`,
		`{"payload":"claim","signature":12}`,
		`{"payload":"` + strings.Repeat("a", 32<<10) + `","signature":"signature"}`,
	}
	for i, raw := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			q := PendingRequest{Payload: "old", Signature: "old", OwnerStart: &Signed{Payload: "old", Signature: "old"}, SetupStart: &Signed{Payload: "old", Signature: "old"}}
			if q.UnmarshalJSON([]byte(raw)) == nil {
				t.Fatal("ambiguous proof transport accepted")
			}
			if q.Payload != "" || q.Signature != "" || q.OwnerStart != nil || q.SetupStart != nil {
				t.Fatal("failed decode retained predecessor proof")
			}
		})
	}
}

func TestPendingRequestSchemaRequiresNonNullFullStart(t *testing.T) {
	variants := (PendingRequest{}).JSONSchema()["oneOf"].([]any)
	if len(variants) != 3 {
		t.Fatal("ordinary, owner and setup pending variants must stay distinct")
	}
	required := func(shape map[string]any) string {
		keys := append([]string{}, shape["required"].([]string)...)
		sort.Strings(keys)
		return strings.Join(keys, ",")
	}
	envelope := func(shape map[string]any) {
		t.Helper()
		props := shape["properties"].(map[string]any)
		if shape["type"] != "object" || shape["additionalProperties"] != false || required(shape) != "payload,signature" || len(props) != 2 {
			t.Fatal("start must be a full closed nonnull signed envelope")
		}
		for _, key := range []string{"payload", "signature"} {
			field := props[key].(map[string]any)
			if field["type"] != "string" || field["minLength"] != 1 {
				t.Fatal("empty or null signed envelope scalar admitted")
			}
		}
	}
	for i, value := range variants {
		shape := value.(map[string]any)
		props := shape["properties"].(map[string]any)
		if shape["type"] != "object" || shape["additionalProperties"] != false {
			t.Fatal("pending variant is not closed")
		}
		switch i {
		case 0:
			envelope(shape)
		case 1:
			if required(shape) != "ownerStart,payload,signature" || len(props) != 4 || props["setupStart"] != nil {
				t.Fatal("owner pending schema is not disjoint")
			}
			envelope(props["ownerStart"].(map[string]any))
			envelope(props["ownerSwitchCount"].(map[string]any))
		case 2:
			if required(shape) != "payload,setupStart,signature" || len(props) != 3 || props["ownerStart"] != nil || props["ownerSwitchCount"] != nil {
				t.Fatal("setup pending schema is not disjoint")
			}
			envelope(props["setupStart"].(map[string]any))
		}
	}
}

func TestPendingCountIsOwnerOnlyAndRetainsCompleteEnvelope(t *testing.T) {
	key, claim, start, count, _ := ownerCountFixture(t)
	original, signedStart, signedCount := signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start), signOwnerFixture(t, key, count)
	in := PendingRequest{Payload: original.Payload, Signature: original.Signature, OwnerStart: &signedStart, OwnerSwitchCount: &signedCount}
	raw, e := json.Marshal(in)
	var got PendingRequest
	if e != nil || json.Unmarshal(raw, &got) != nil || got.OwnerSwitchCount == nil || *got.OwnerSwitchCount != signedCount || got.OwnerStart == nil || *got.OwnerStart != signedStart {
		t.Fatal("count proof was not retained with its full parents")
	}
	base := `"payload":"claim","signature":"signature"`
	owner := `,"ownerStart":{"payload":"start","signature":"signature"}`
	for _, wire := range []string{
		`{` + base + `,"ownerSwitchCount":{"payload":"count","signature":"signature"}}`,
		`{` + base + owner + `,"ownerSwitchCount":null}`,
		`{` + base + owner + `,"ownerSwitchCount":{}}`,
		`{` + base + owner + `,"ownerSwitchCount":{"payload":"count"}}`,
		`{` + base + owner + `,"ownerSwitchCount":{"payload":"count","signature":"signature","unknown":true}}`,
		`{` + base + owner + `,"ownerSwitchCount":{"payload":"count","signature":"signature","payload":"other"}}`,
		`{` + base + owner + `,"ownerSwitchCount":{"payload":"count","signature":"signature"},"ownerSwitchCount":{"payload":"other","signature":"signature"}}`,
	} {
		got = in
		if json.Unmarshal([]byte(wire), &got) == nil || got.Payload != "" || got.OwnerStart != nil || got.OwnerSwitchCount != nil {
			t.Fatal("ambiguous or non-owner count accepted or predecessor retained")
		}
	}
	variants := (PendingRequest{}).JSONSchema()["oneOf"].([]any)
	if _, ok := variants[0].(map[string]any)["properties"].(map[string]any)["ownerSwitchCount"]; ok {
		t.Fatal("ordinary pending schema accepts owner count")
	}
	countSchema := variants[1].(map[string]any)["properties"].(map[string]any)["ownerSwitchCount"].(map[string]any)
	if countSchema["type"] != "object" || countSchema["additionalProperties"] != false {
		t.Fatal("optional count is not a complete nonnullable envelope")
	}
}

func TestPendingSetupIsDistinctAndRejectsMixedStarts(t *testing.T) {
	signed := Signed{Payload: "payload", Signature: "signature"}
	in := PendingRequest{Payload: "original", Signature: "signature", SetupStart: &signed}
	raw, err := json.Marshal(in)
	var got PendingRequest
	if err != nil || json.Unmarshal(raw, &got) != nil || got.SetupStart == nil || *got.SetupStart != signed || got.OwnerStart != nil {
		t.Fatal("setup start lost its envelope")
	}
	for _, extra := range []string{`,"ownerStart":{"payload":"p","signature":"s"}`, `,"ownerSwitchCount":{"payload":"p","signature":"s"}`, `,"setupStart":null`} {
		text := string(raw[:len(raw)-1]) + extra + "}"
		if json.Unmarshal([]byte(text), &got) == nil || got.SetupStart != nil || got.OwnerStart != nil || got.OwnerSwitchCount != nil {
			t.Fatal("mixed/null/duplicate setup start accepted")
		}
	}
}
