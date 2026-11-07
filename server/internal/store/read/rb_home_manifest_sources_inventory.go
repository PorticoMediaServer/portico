package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RBHomeManifestSourceDefinitions is inert composition inventory. Its caller
// also captures the common browse, saved-view, manifest and UO personal points.
func RBHomeManifestSourceDefinitions() []stmt.Definition { return rbHomeManifestSourceDefinitions() }
