// Package schema owns the schema-v1 bootstrap DDL and its static query contracts.
// It does not open connections or authorize runtime writes.
package schema

import (
	"embed"
	"io/fs"
)

// SHA256 identifies the exact schema.sql bytes approved for phase 1.
const SHA256 = "291ab3ac4b78338227923e090174b562726d3f7cddb36669e94cb14e05747fdd"

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
		{"INT-INDEX-POLICY.json", "02074d8e7713d1c3420baebc7590dbc718f8dabb798b04aff7f0bb9b8a3c4540"},
		{"INT-TYPE-TABLES.json", "6143f5c59466d23ac472b5d68b87d26185be00e7b8e2ad74c58891bafe36c6f3"},
		{"INT-CONTRIBUTIONS.sql", "c6c39b37ee2133f6c06601d5254022210e5f265ead7a3a4fa79dad59ea3371f6"},
		{"INT-LARGE-INDEXES.json", "55c061685813d1d2f29c761c66ed817c203ffcc6081ebe1bde4a58ba3893984e"},
		{"INT-STATEMENT-MAP.md", "e993120899d46b7a66dba8793a64328b27a51bec2d2c4825a86ef74d6eaaaa4b"},
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
