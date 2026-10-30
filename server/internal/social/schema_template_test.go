package social

import "github.com/PorticoMediaServer/portico/server/internal/persistence"

// Every fixture in this package opens a fresh database, and installing the
// schema from nothing costs about half a second — about sixteen under the race
// detector. Reusing one installed schema for all of them is what lets this
// package be race-tested at all; see internal/persistence/template.go. An init
// in a test file runs before TestMain and before any test, so it needs no
// cooperation from the fixtures themselves.
func init() { persistence.UseSchemaTemplate() }
