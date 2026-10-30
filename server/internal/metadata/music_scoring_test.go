package metadata

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func musicScoringFixture() (mbJob, metadataprovider.Recording) {
	n := 100000
	j := mbJob{kind: "song", title: "Song", artist: "Artist"}
	j.base.Music = musicInputs{Duration: 100, Tags: map[string]string{}}
	return j, metadataprovider.Recording{ID: "11111111-1111-1111-1111-111111111111", Title: "Song", LengthMillis: &n, ArtistCredit: []metadataprovider.ArtistCredit{{Name: "Artist", Artist: metadataprovider.Artist{ID: "55555555-5555-5555-5555-555555555555", Name: "Artist"}}}}
}
func scoreMusicFixture(j mbJob, v metadataprovider.Recording, acoustic float64) mbCandidateEvidence {
	raw, _ := json.Marshal(v)
	row := mbCandidateEvidence{id: v.ID, title: v.Title, artist: mbArtist(v.ArtistCredit), payload: raw}
	rankMusicSong(&row, j, v, acoustic)
	return row
}
func TestMusicEvidenceConfidenceIndependentSignalsAndMargin(t *testing.T) {
	j, v := musicScoringFixture()
	row := scoreMusicFixture(j, v, 0)
	id, o := rankMusicCandidates([]mbCandidateEvidence{row}, j)
	if id != v.ID || o.Confidence < .85 || o.StrongSignals != 3 {
		t.Fatal(id, o)
	}
	v.LengthMillis = nil
	if id, _ = rankMusicCandidates([]mbCandidateEvidence{scoreMusicFixture(j, v, 0)}, j); id != "" {
		t.Fatal("name-only match published")
	}
	_, v = musicScoringFixture()
	second := v
	second.ID = "22222222-2222-2222-2222-222222222222"
	if id, o = rankMusicCandidates([]mbCandidateEvidence{scoreMusicFixture(j, v, 0), scoreMusicFixture(j, second, 0)}, j); id != "" || o.Margin != 0 {
		t.Fatal("ambiguous recordings auto-selected", id, o)
	}
	j.manual = true
	if id, _ = rankMusicCandidates([]mbCandidateEvidence{row}, j); id != "" {
		t.Fatal("manual identity replaced")
	}
	j.manual = false
	j.base.RecordingID = second.ID
	if id, _ = rankMusicCandidates([]mbCandidateEvidence{row}, j); id != "" {
		t.Fatal("accepted identity replaced")
	}
}
func TestMusicContradictionsOverrideFingerprintAndVersionTokensRemain(t *testing.T) {
	j, v := musicScoringFixture()
	n := 130000
	v.LengthMillis = &n
	row := scoreMusicFixture(j, v, 1)
	if !row.contradiction || row.confidence > .49 {
		t.Fatal(row)
	}
	if musicSame("Song (Live)", "Song") || musicSame("Song Remix", "Song") {
		t.Fatal("variant title was normalized away")
	}
	j, v = musicScoringFixture()
	j.base.Music.Tags["isrc"] = "CAAAA2300001"
	v.ISRCs = []string{"USAAA2300001"}
	if row = scoreMusicFixture(j, v, 1); !row.contradiction {
		t.Fatal("ISRC conflict ignored")
	}
}
func TestMusicFullLookupMustStillBeatCompetitors(t *testing.T) {
	j, v := musicScoringFixture()
	rows := []mbCandidateEvidence{scoreMusicFixture(j, v, 0)}
	if !musicVerifyRecording(rows, j, v.ID, v) {
		t.Fatal("consistent lookup rejected")
	}
	changed := v
	n := 130000
	changed.LengthMillis = &n
	if musicVerifyRecording(rows, j, v.ID, changed) {
		t.Fatal("conflicting detailed lookup accepted")
	}
	rows = []mbCandidateEvidence{scoreMusicFixture(j, v, 0)}
	changed = v
	changed.ID = "22222222-2222-2222-2222-222222222222"
	if musicVerifyRecording(rows, j, v.ID, changed) {
		t.Fatal("merged search identity bypassed review")
	}
	for i := 0; i < 5; i++ {
		rankMusicCandidates(rows, j)
	}
	seen := map[string]bool{}
	for _, reason := range rows[0].reasons {
		if seen[reason] {
			t.Fatal("duplicate reason", reason)
		}
		seen[reason] = true
	}
}
func TestMusicReleasePhysicalEditionContradictions(t *testing.T) {
	j, _ := musicScoringFixture()
	j.kind = "album"
	j.title = "Album"
	j.base.Music.Tags = map[string]string{"barcode": "123456789012", "date": "2020", "release_country": "CA", "total_tracks": "12"}
	v := metadataprovider.ReleaseCandidate{ID: "22222222-2222-2222-2222-222222222222", Title: "Album", Date: "2020", Country: "CA", Barcode: "123456789012", TrackCount: 12}
	row := mbCandidateEvidence{id: v.ID, title: v.Title, artist: "Artist"}
	rankMusicAlbum(&row, j, v)
	if id, _ := rankMusicCandidates([]mbCandidateEvidence{row}, j); id == "" {
		t.Fatal(row)
	}
	v.Country = "US"
	rankMusicAlbum(&row, j, v)
	if id, _ := rankMusicCandidates([]mbCandidateEvidence{row}, j); id != "" {
		t.Fatal("different physical edition auto-selected")
	}
	j.incompleteCandidates = true
	v.Country = "CA"
	rankMusicAlbum(&row, j, v)
	if id, _ := rankMusicCandidates([]mbCandidateEvidence{row}, j); id != "" {
		t.Fatal("partial candidate set auto-selected")
	}
}
