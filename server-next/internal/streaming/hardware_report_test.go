package streaming

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/capabilityreport"
)

func hardwareStatus() (capabilityreport.Status, bool) {
	for _, s := range capabilityreport.All() {
		if s.Capability == capabilityreport.Hardware {
			return s, true
		}
	}
	return capabilityreport.Status{}, false
}
