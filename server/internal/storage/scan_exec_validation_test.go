package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func TestInventoryCommandGeneratedArgumentBoundary(t *testing.T) {
	r := request{Inventory: &InventoryRequest{}, Argv: make([]string, mediaexec.MaxGeneratedArguments)}
	r.Argv[0] = "/usr/bin/bwrap"
	if err := validateInventoryCommand(r); err != nil {
		t.Fatalf("complete bounded sandbox argv was rejected: %v", err)
	}
	r.Argv = append(r.Argv, "--extra")
	if err := validateInventoryCommand(r); err == nil {
		t.Fatal("helper accepted more than the generator permits")
	}
	r.Argv = []string{"/usr/bin/bwrap", strings.Repeat("x", 16384)}
	if err := validateInventoryCommand(r); err != nil {
		t.Fatalf("per-argument boundary was rejected: %v", err)
	}
	r.Argv[1] += "x"
	if err := validateInventoryCommand(r); err == nil {
		t.Fatal("per-argument byte guard was widened")
	}
	for _, invalid := range []request{
		{Argv: []string{"/usr/bin/bwrap", "--unshare-net"}},
		{Inventory: &InventoryRequest{}, Argv: []string{"/usr/bin/bwrap"}},
	} {
		if err := validateInventoryCommand(invalid); err == nil {
			t.Fatal("malformed inventory command was accepted")
		}
	}
}

func TestInventoryCommandSerializedByteLimitRemainsBounded(t *testing.T) {
	r := request{Operation: "inventory-command", Inventory: &InventoryRequest{}, Argv: []string{"/usr/bin/bwrap"}}
	for i := 0; i < 5; i++ {
		r.Argv = append(r.Argv, strings.Repeat("x", 16384))
	}
	if err := validateInventoryCommand(r); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = Helper(bytes.NewReader(payload), &out); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("the helper's existing 64KiB serialized request guard changed: %v", err)
	}
}
