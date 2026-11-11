package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The owner's away limit for music set to CD quality (1,411 kbps): a high-resolution song
// played away from home becomes CD quality, the best that fits, not 320 kbps.
func TestReviewAwayMusicLimitOfCDQualityGivesCDQuality(t *testing.T) {
	// The menu mediaengine.Engine.Qualities gives a song.
	menu := []QualityOption{{Mode: QualityOriginal}, {Mode: QualityCD},
		{Mode: QualityLimit, ID: "320", MaxAudioBitrateKbps: 320}, {Mode: QualityLimit, ID: "96", MaxAudioBitrateKbps: 96}}
	hiRes := db.PlaybackVersion{Version: db.Version{Bitrate: 4_600_000}} // 24-bit 96 kHz FLAC
	kept, got, err := fit(hiRes, db.KindTrack, menu, Quality{Mode: QualityOriginal}, 1411, false)
	if err != nil || got.Mode != QualityCD {
		t.Fatalf("under an away limit of CD quality a high-resolution song got %+v (%v), want CD quality; offered %+v", got, err, kept)
	}
	// The same when it is the upload speed that leaves 1,500 kbps.
	if _, got, err = fit(hiRes, db.KindTrack, menu, Quality{Mode: QualityOriginal}, 1500, true); err != nil || got.Mode != QualityCD {
		t.Fatalf("with 1,500 kbps of upload left a high-resolution song got %+v (%v), want CD quality", got, err)
	}
}
