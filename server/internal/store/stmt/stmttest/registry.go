// Package stmttest supplies deliberately unqualified SQL for unit fault fixtures.
// It refuses ordinary binaries and must never be imported by production code.
// These fixtures are not native plan qualification or production receipts.
package stmttest

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/internal/qualification"
)

var ErrTestOnly = errors.New("stmt fixture registration is available only in a Go test binary")

func Receipt(t testing.TB, definitions []stmt.Definition) (stmt.Receipt, error) {
	if !testing.Testing() || t == nil {
		return nil, ErrTestOnly
	}
	t.Helper()
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		return nil, err
	}
	entries := make(map[string][]byte)
	for _, entry := range catalogue.Manifest() {
		data, err := json.Marshal(entry.Definition)
		if err != nil {
			return nil, err
		}
		entries[string(entry.ID)] = data
	}
	return qualification.Mint(entries, []byte("unqualified unit fault fixture"), []byte("Go test binary only; no source certification")), nil
}

func New(t testing.TB, definitions []stmt.Definition) (*stmt.Registry, error) {
	proof, err := Receipt(t, definitions)
	if err != nil {
		return nil, err
	}
	return stmt.New(definitions, proof)
}
