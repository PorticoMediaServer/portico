package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// CI sets CONTRACTS_CHECK=1 (merge.yml, server_next): a change to a route or a shape fails unless
// the generated files are committed with it. Locally the check runs only when asked.
func TestContractsAreCurrent(t *testing.T) {
	if os.Getenv("CONTRACTS_CHECK") != "1" {
		t.Skip("set CONTRACTS_CHECK=1 to check generated contracts")
	}
	types, openAPI, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string][]byte{typesFile: types, openAPIFile: openAPI} {
		got, err := os.ReadFile(filepath.Join("..", "..", file))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is out of date: run `go run ./cmd/portico-contracts` in server-next and commit the result", file)
		}
	}
}

// Every registered route must be representable even when freshness is not checked.
func TestRegisteredRoutesCanGenerate(t *testing.T) {
	_, openAPI, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name, In string
				Required bool
				Schema   struct{ Type string }
			}
		}
	}
	if err := json.Unmarshal(openAPI, &doc); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][2]string{{"delete", "/v1/admin/items/{id}"}, {"post", "/v1/admin/items/delete"}} {
		found := false
		for _, p := range doc.Paths[r[1]][r[0]].Parameters {
			if p.Name == "confirm" {
				found = p.In == "query" && p.Required && p.Schema.Type == "string"
			}
		}
		if !found {
			t.Errorf("%s %s must declare its required confirm query", r[0], r[1])
		}
	}
}
