package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RBSavedViewDefinitions supplies the inert saved-view inventory for explicit
// harness composition. The actual Reader must Capture and authorize it before
// NewSavedViewReader can execute; this registers no production dispatcher.
func RBSavedViewDefinitions() []stmt.Definition { return rbSavedViewDefinitions() }
