package compactcatalog_test

import "github.com/PorticoMediaServer/portico/server/internal/persistence"

// Every fixture in this directory opens a fresh database — catalogtest.Open,
// persistence.Open, and the crash-writer subprocess — and installing the
// schema from nothing costs about half a second, about sixteen under the race
// detector. Reusing one installed schema for all of them is what keeps this
// package's test time in proportion to the work it does; see
// internal/persistence/template.go. The internal and external test packages
// compile into one binary, so an init here covers both, and it runs before any
// test or subprocess. Fixtures that need a real install call
// persistence.OpenFresh, which ignores the template, and an existing database
// is never answered from it.
func init() { persistence.UseSchemaTemplate() }
