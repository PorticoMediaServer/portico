package main

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"testing"
)

func TestDateOperandsUseThePublishedQueryGrammar(t *testing.T) {
	field := catalog.BrowseFieldCapability{ID: "releaseDate", Type: "date", Operators: []string{"within-last-days", "between", "is-present"}}
	for _, node := range fieldOperands(field) {
		raw, err := json.Marshal(node)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = catalog.ParseBrowseQuery(raw, "query"); err != nil {
			t.Fatalf("%s: %v", node.Operator, err)
		}
	}
}
