package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RSNativeSearchDefinitions is an inert caller-owned contribution. Append it
// to NativeFullSearchDefinitions BEFORE Describe/Capture/New and pool creation.
// Domain opt-in installs no SQL; a pool missing these definitions refuses.
//
// Reading is the only RS domain activated here: recipes40/41 stay private, and
// only recipe40 opts in to native search, so its publication/inventory points
// are the owned SQL contribution. The image-format converter (recipe41) is not
// a native-search recipe and cannot be selected by a hit.
//
// The complete collection domain (kind10) is deliberately absent. Source8's
// authored tuple and the default catalogue recipe cannot prove complete
// automatic membership, so the merged authored collection recipe refuses an
// automatic identity explicitly (ErrUnqualified) and never invents an empty
// member set. RS-05's automatic reducer owns the complete collection
// source/point definitions; until it lands there is nothing inert and exact to
// append here, and adding a catalogue fallback would advertise a domain the
// packet does not enable.
func RSNativeSearchDefinitions() []stmt.Definition { return ReadingFactsDefinitions() }
