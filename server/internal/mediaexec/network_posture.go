package mediaexec

import (
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
)

var networkPosture struct {
	sync.Mutex
	unconfined bool
	reason     string
}

// Keep a conservative process-wide report: a later isolated file job must not
// erase the fact that this server also admitted a host-network decoder path.
// No endpoint, capability token or source URL is included in diagnostics.
func reportNetworkIsolation(isolated bool, reason string) {
	networkPosture.Lock()
	defer networkPosture.Unlock()
	if !isolated {
		networkPosture.unconfined = true
		networkPosture.reason = reason
	}
	if networkPosture.unconfined {
		isolated, reason = false, networkPosture.reason
	}
	// Publish under the same lock: an earlier isolated caller must not replace
	// the unavailable report after a host-network caller has published it.
	if isolated {
		capabilityreport.Report(capabilityreport.DecoderNetworkIsolation, true, "", described("Admitted decoder commands have network isolation; declared bridges are confined to their approved loopback endpoint."))
		return
	}
	capabilityreport.Report(capabilityreport.DecoderNetworkIsolation, false, "decoder_network_unconfined", described(reason))
}
