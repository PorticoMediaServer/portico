package subtitles

import (
	"github.com/PorticoMediaServer/portico/server/internal/dbwork/waketest"
	"testing"
)

func TestRuntimeCollectionWakesOnlyForRealInputs(t *testing.T) {
	waketest.Inputs(t, watchSubtitleCollection,
		"subtitle_provider_candidates", "subtitle_resources", "subtitle_revisions", "subtitle_original_files", "subtitle_obsolete_revisions",
		"playback_subtitle_pins", "playback_manifest_subtitles", "playback_disc_subtitle_resources")
}
