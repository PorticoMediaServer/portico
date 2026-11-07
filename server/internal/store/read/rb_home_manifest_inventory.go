package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RBHomeManifestDefinitions is inert explicit harness inventory. Actual Reader
// Capture and the saved-view dependency inventory must authorize composition;
// this does not register a product dispatcher or expose caller-defined recipes.
func RBHomeManifestDefinitions() []stmt.Definition { return rbHomeManifestDefinitions() }
