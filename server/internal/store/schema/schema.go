// Package schema owns the schema-v1 bootstrap DDL and its static query contracts.
// It does not open connections or authorize runtime writes.
package schema

import (
	"embed"
	"io/fs"
)

// SHA256 identifies the exact schema.sql bytes under INT change control.
const SHA256 = "5785482ac840ebbfa456f9a805b0ff2f23b964ca9b5cd3c1e1e3913674088744"

//go:embed schema.sql
var ddl string

//go:embed INT-INDEX-POLICY.json INT-TYPE-TABLES.json INT-CONTRIBUTIONS.sql INT-LARGE-INDEXES.json INT-STATEMENT-MAP.md
var contracts embed.FS

// SQL returns the complete bootstrap DDL, without seeds or migrations.
func SQL() string { return ddl }

// Artifact pins a copied contract by its exact bytes.
type Artifact struct {
	Name   string
	SHA256 string
}

// Contracts returns an independent inventory of the embedded query contracts.
func Contracts() []Artifact {
	return []Artifact{
		{"INT-INDEX-POLICY.json", "068eadae29269065d41430bb76a435ebfb8513bbaf2cf42a67cc5e78287829e7"},
		{"INT-TYPE-TABLES.json", "2b1121ebc86e6bf74ac18d721efc8971dde3e5a835313279946b20d005c84843"},
		{"INT-CONTRIBUTIONS.sql", "c6c39b37ee2133f6c06601d5254022210e5f265ead7a3a4fa79dad59ea3371f6"},
		{"INT-LARGE-INDEXES.json", "29262de402517821830b5d60e55efb2c2429c8b3411c4df7826d700c42747a0f"},
		{"INT-STATEMENT-MAP.md", "17d8d8d7e908434b3d33c035f941e175c1a5657ba8e394f462c057643e5defa5"},
	}
}

// ReadContract returns a fresh copy of a named contract. The index-policy JSON
// is the shared authority for exact partial-index predicates; callers must not
// independently rewrite those predicates. Contribution SQL is a reference
// fragment already included in SQL(), not an additional bootstrap script.
func ReadContract(name string) ([]byte, error) {
	for _, artifact := range Contracts() {
		if name == artifact.Name {
			return contracts.ReadFile(name)
		}
	}
	return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
}
