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
		{BrowseNode{Field: "contentRating", Operator: "is-present"}, `{"field":"contentRating","operator":"is-present","value":null}`},
		{BrowseNode{Field: "decade", Operator: "equals", Value: 1990}, `{"field":"decade","operator":"equals","value":1990}`},
		{BrowseNode{All: []BrowseNode{{Field: "lastPlayedAt", Operator: "is-missing"}}}, `{"all":[{"field":"lastPlayedAt","operator":"is-missing","value":null}]}`},
		{BrowseNode{Not: &BrowseNode{Field: "genre", Operator: "contains", Value: "Drama"}}, `{"not":{"field":"genre","operator":"contains","value":"Drama"}}`},
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
