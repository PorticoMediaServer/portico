package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The merge desk checks the generated files after regenerating a batch of API changes.
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
