package assets

import "testing"

func TestWave2AudioEvidenceRetainsListeningAndProviderFields(t *testing.T) {
	var f Facts
	CopyEmbeddedAudioTags(&f, map[string]string{"series_index": "2.5", "series_title": "Saga", "replaygain_track_gain": "-6 dB", "r128_album_gain": "-384", "encoder_delay": "576", "musicbrainz_recordingid": "11111111-1111-1111-1111-111111111111"})
	for key, want := range map[string]string{"series_position": "2.5", "series": "Saga", "replaygain_track_gain": "-6 dB", "r128_album_gain": "-384", "encoder_delay": "576", "musicbrainz_trackid": "11111111-1111-1111-1111-111111111111"} {
		if f.Tags[key] != want {
			t.Fatalf("%s lost: %q", key, f.Tags[key])
		}
	}
	if len(f.AudioEvidence) != 6 {
		t.Fatal("provenance dropped", f.AudioEvidence)
	}
}
