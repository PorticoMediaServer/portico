package setupflow_test

import "github.com/PorticoMediaServer/portico/server/internal/persistence"

// Fresh test databases in this package are byte copies of one installed schema
// per test binary instead of a full install each (see
// internal/persistence/template.go).
func init() { persistence.UseSchemaTemplate() }
