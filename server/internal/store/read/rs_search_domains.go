package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RSNativeSearchDefinitions is an inert caller-owned contribution. Append it
// to NativeFullSearchDefinitions BEFORE Describe/Capture/New and pool creation.
// Domain opt-in installs no SQL; a pool missing these definitions refuses.
func RSNativeSearchDefinitions() []stmt.Definition { return ReadingFactsDefinitions() }
