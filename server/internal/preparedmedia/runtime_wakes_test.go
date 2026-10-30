package preparedmedia

import (
	"github.com/PorticoMediaServer/portico/server/internal/dbwork/waketest"
	"testing"
)

func TestRuntimeCollectionWakesOnlyForRealInputs(t *testing.T) {
	waketest.Inputs(t, watchPreparedCollection, "prepared_media_jobs", "prepared_media_versions", "prepared_media_session_pins", "playback_sessions", "catalog_asset_links")
}

func TestRuntimeWorkWakesOnlyForRealInputs(t *testing.T) {
	waketest.Inputs(t, watchPreparedWork, "prepared_media_jobs", "catalog_assets", "inventory_objects", "remote_source_read_profiles", "server_settings", "authorization_access", "playback_sessions")
}
