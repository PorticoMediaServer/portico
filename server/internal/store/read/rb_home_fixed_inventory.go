package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RBHomeFixedDefinitions is inert harness inventory. Compose with the common
// browse, saved-view, Home manifest and committed-source definitions. It includes
// the two existing whole-binding payload dependencies used by this reader;
// callers must not register those IDs a second time. Actual Capture is required.
func RBHomeFixedDefinitions() []stmt.Definition {
	defs := append([]stmt.Definition(nil), rbHomeBindingDefinitions()[1:]...)
	defs = append(defs, rbHomeFixedDefinitions()...)
	return append(defs, rbHomeFixedBindingByteDefinitions()...)
}
