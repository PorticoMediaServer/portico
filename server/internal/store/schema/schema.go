// Package schema owns the schema-v1 bootstrap DDL and its static query contracts.
// It does not open connections or authorize runtime writes.
package schema

import (
	"embed"
	"io/fs"
)

// SHA256 identifies the exact schema.sql bytes under INT change control.
const SHA256 = "567267b78205ab47b7efe0be764286b29d14f99ad476c5acdb44c65ac9f8bd3f"

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
		{"INT-INDEX-POLICY.json", "64e7c167fddbdd281a885ee216e5adf6bd1068c38487f8b528279284311ebe45"},
		{"INT-TYPE-TABLES.json", "739fbf93f34ea1fe38c259689d382b838980fc86eef474b14ebf71cb84c55d44"},
		{"INT-CONTRIBUTIONS.sql", "c6c39b37ee2133f6c06601d5254022210e5f265ead7a3a4fa79dad59ea3371f6"},
		{"INT-LARGE-INDEXES.json", "a2230582ba823daa73d0a771311fae619ef525f0a345a7f009d97e59ad32b7c5"},
		{"INT-STATEMENT-MAP.md", "aaa472d0a10be8710aae0e7d7987277839f87ed9af6eb6407bf8979ef7e5a2fe"},
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
