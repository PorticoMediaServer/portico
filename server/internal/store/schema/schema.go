// Package schema owns the schema-v1 bootstrap DDL and its static query contracts.
// It does not open connections or authorize runtime writes.
package schema

import (
	"embed"
	"io/fs"
)

// SHA256 identifies the exact schema.sql bytes under INT change control.
const SHA256 = "9d9aa39dfcdb99d4943f61b91ce7307f17026bf5ff7afed36a248f6b55b21f05"

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
		{"INT-INDEX-POLICY.json", "05c677a9b67af063439dbd103472add0b1d1a56fb7622662ba0c7da28084dfa4"},
		{"INT-TYPE-TABLES.json", "c5e17156e7a3f8863d94f71a5ea750be7daa64998f91fd2a0a736e8f7e4a62de"},
		{"INT-CONTRIBUTIONS.sql", "c6c39b37ee2133f6c06601d5254022210e5f265ead7a3a4fa79dad59ea3371f6"},
		{"INT-LARGE-INDEXES.json", "4a5b12acfc405c9b2f64079aa8eac93c802704b09317654f91d0f7fef06a3145"},
		{"INT-STATEMENT-MAP.md", "a08acfca686da4c82699cd70342bca72da6f0734d0ca276a8d4f51087bb4e9a0"},
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
