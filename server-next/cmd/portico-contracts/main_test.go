package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// An app built against a stale contract breaks at run time, so a route or shape cannot
// change without the checked-in files changing with it.
func TestContractsAreCurrent(t *testing.T) {
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
