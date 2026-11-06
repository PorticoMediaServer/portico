package nativeinventory

import (
	"log"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Monitor is the SAME fixed-cardinality state held by the actual Supervisor.
// It has no path/job labels and grants no operation or resource authority.
type Monitor struct {
	Active      atomic.Int64
	OverGrace   atomic.Uint64
	WarningBusy atomic.Bool
}

func (s *Monitor) Warn() {
	// Logging cannot extend the caller grace or block the reaper. At most one
	// warning worker per supervisor can be blocked by a slow operator log sink;
	// concurrent warnings coalesce, while both scalar metrics retain the facts.
	if s.WarningBusy.CompareAndSwap(false, true) {
		supervise.Go("storage.native-retirement-warning", func() {
			defer s.WarningBusy.Store(false)
			log.Print("warning: native storage helper retirement exceeds five-second grace; physical owner and helper slot remain held")
		})
	}
}
