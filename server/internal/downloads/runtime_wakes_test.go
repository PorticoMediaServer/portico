package downloads

import (
	"github.com/PorticoMediaServer/portico/server/internal/dbwork/waketest"
	"testing"
)

func TestRuntimeWorkWakesOnlyForRealInputs(t *testing.T) {
	waketest.Inputs(t, watchDownloadWork, "download_requests", "download_preparations", "download_grants", "prepared_media_versions", "catalog_assets", "library_sources", "server_settings", "direct_profiles", "playlist_shares")
}
