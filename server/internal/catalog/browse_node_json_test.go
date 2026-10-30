package catalog

import (
	"encoding/json"
	"testing"
)

// CD-29: a predicate is always exactly {field, operator, value}, with a null
// value for the presence operators, so an echoed saved view or deep link
// round-trips through clients that read those three keys.
func TestBrowseNodeEncodesPresenceWithNullValue(t *testing.T) {
	for _, tc := range []struct {
		node BrowseNode
		want string
	}{
		{BrowseNode{Field: "contentRating", Operator: "is-present"}, `{"variant":"predicate","field":"contentRating","operator":"is-present","value":null}`},
		{BrowseNode{Field: "decade", Operator: "equals", Value: 1990}, `{"variant":"predicate","field":"decade","operator":"equals","value":1990}`},
		{BrowseNode{All: []BrowseNode{{Field: "lastPlayedAt", Operator: "is-missing"}}}, `{"variant":"group","all":[{"variant":"predicate","field":"lastPlayedAt","operator":"is-missing","value":null}]}`},
		{BrowseNode{Not: &BrowseNode{Field: "genre", Operator: "contains", Value: "Drama"}}, `{"variant":"group","not":{"variant":"predicate","field":"genre","operator":"contains","value":"Drama"}}`},
	} {
		raw, err := json.Marshal(tc.node)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("got %s, want %s", raw, tc.want)
		}
	}
}

// R-09b: every node on the wire is tagged, in requests as in responses.
func TestBrowseNodeRequiresItsVariant(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"untagged predicate", `{"field":"year","operator":"at-least","value":2000}`},
		{"untagged group", `{"all":[{"variant":"predicate","field":"year","operator":"at-least","value":2000}]}`},
		{"unknown variant", `{"variant":"fuzzy","field":"year","operator":"at-least","value":2000}`},
		{"predicate tagged group", `{"variant":"group","field":"year","operator":"at-least","value":2000}`},
		{"group tagged predicate", `{"variant":"predicate","all":[]}`},
		{"relative operator under predicate", `{"variant":"predicate","field":"dateAdded","operator":"within-last-days","value":7}`},
		{"other operator under relative", `{"variant":"relative","field":"year","operator":"at-least","value":2000}`},
		{"nested untagged", `{"variant":"group","all":[{"field":"year","operator":"at-least","value":2000}]}`},
	} {
		if _, err := ParseBrowseQuery(json.RawMessage(tc.raw), "query"); err == nil {
			t.Errorf("%s: accepted %s", tc.name, tc.raw)
		}
	}
	for _, raw := range []string{
		`{"variant":"predicate","field":"year","operator":"at-least","value":2000}`,
		`{"variant":"relative","field":"dateAdded","operator":"within-last-days","value":7}`,
		`{"variant":"group","not":{"variant":"predicate","field":"year","operator":"at-least","value":2000}}`,
	} {
		node, err := ParseBrowseQuery(json.RawMessage(raw), "query")
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		out, err := json.Marshal(node)
		if err != nil || string(out) != raw {
			t.Fatalf("round trip %s -> %s (%v)", raw, out, err)
		}
		var decoded BrowseNode
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	}
}
