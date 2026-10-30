package assets

import "testing"

func TestEmbeddedAudioEvidenceKeepsTypedIDsAndIndependentObservations(t *testing.T) {
	var f Facts
	CopyEmbeddedAudioTags(&f, map[string]string{"MUSICBRAINZ_TRACKID": "11111111-1111-1111-1111-111111111111", "MusicBrainz Album Id": "22222222-2222-2222-2222-222222222222", "ACOUSTID_FINGERPRINT": "AQAAEAAAgAAAQAAAAAAAAAAA", "SERIES-PART": "1.5", "TITLE": "Local Song", "rating": "5"})
	if f.Tags["musicbrainz_trackid"] == f.Tags["musicbrainz_albumid"] || f.Tags["series_position"] != "1.5" || f.Tags["rating"] != "" || f.Tags["acoustid_fingerprint"] == "" {
		t.Fatal(f.Tags)
	}
	CopyEmbeddedAudioTags(&f, map[string]string{"musicbrainz_trackid": "33333333-3333-3333-3333-333333333333"})
	if f.Tags["musicbrainz_trackid"] != "conflicting IDs" || len(f.AudioEvidence) != 6 {
		t.Fatal(f)
	}
	if ValidAudioTag("unknown", "value") || ValidAudioTag("title", "value\x00") {
		t.Fatal("invalid embedded metadata accepted")
	}
}
