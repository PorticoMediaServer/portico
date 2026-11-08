// Package schema owns the schema-v1 bootstrap DDL and its static query contracts.
// It does not open connections or authorize runtime writes.
package schema

import (
	"embed"
	"io/fs"
)

// SHA256 identifies the exact schema.sql bytes under INT change control.
const SHA256 = "484242fbb520ace5b4bfc1db015e2f3530dc01cfa5ff08bfd22a5244812905ca"

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
		{"INT-INDEX-POLICY.json", "7bed1c51584c5349656d4eeef1366e7c085ef0e212fff75862e84591d2e9bc93"},
		{"INT-TYPE-TABLES.json", "eea05a016b83af5148209d0e200d9953f80503dea04ea61a94be01e5a9b4cb32"},
		{"INT-CONTRIBUTIONS.sql", "c6c39b37ee2133f6c06601d5254022210e5f265ead7a3a4fa79dad59ea3371f6"},
		{"INT-LARGE-INDEXES.json", "a7421b760ed247fb30aabd51a0dad97dfae2f796f49e613f124a172a2bba12ee"},
		{"INT-STATEMENT-MAP.md", "e5039dae1a217180fa29928449bff92bed3cf417a9b9f31670eca9100f33ee57"},
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
