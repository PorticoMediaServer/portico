// Package mediacompose binds existing concrete media owners outside writer.
// Importing storage here cannot register legacy SQLite functions in writer's
// canonical binaries. This adapter starts no process and supplies no authority.
package mediacompose

import (
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/physicalrun"
)

var _ physicalrun.Runner = (*storage.Supervisor)(nil)

func PhysicalRunner(s *storage.Supervisor) physicalrun.Runner { return s }
