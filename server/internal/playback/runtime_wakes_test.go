package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/dbwork/waketest"
	"testing"
)

func TestRuntimeHLSWakesOnlyForRealInputs(t *testing.T) {
	waketest.Inputs(t, watchHLSMaintenance, "playback_sessions", "playback_artifacts", "playback_hls_reservations", "playback_hls_demand", "playback_source_pins", "catalog_assets", "server_settings", "authorization_access")
}
