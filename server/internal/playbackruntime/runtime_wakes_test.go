package playbackruntime

import (
	"github.com/PorticoMediaServer/portico/server/internal/dbwork/waketest"
	"testing"
)

func TestRuntimeLinearWakesOnlyForRealInputs(t *testing.T) {
	waketest.Inputs(t, watchLinearWork, "playback_channel_sessions", "playback_v1_sessions", "live_sources", "live_channel_versions", "lc_channels", "live_programme_metadata", "live_allocations", "server_settings", "authorization_access")
}
