package workload

import "github.com/PorticoMediaServer/portico/server/internal/store/internal/walobserve"

// Aliases preserve the existing sustained-v1 wire and callers. The ONE read-only
// implementation is cycle-free so W-private test fixtures can use it directly.
type WALSnapshot = walobserve.WALSnapshot
type WALFileIdentity = walobserve.WALFileIdentity
type WALFileObservation = walobserve.WALFileObservation
