package metadata

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func mbEvidenceFixture() metadataprovider.ReleaseLookup {
	return metadataprovider.ReleaseLookup{RequestedID: "10000000-0000-0000-0000-000000000001", Release: metadataprovider.Release{
		ID: "10000000-0000-0000-0000-000000000002", Title: "Edition", ReleaseGroup: metadataprovider.ReleaseGroup{ID: "20000000-0000-0000-0000-000000000001", Title: "Group"},
		ArtistCredit: []metadataprovider.ArtistCredit{{Name: "One", JoinPhrase: " feat. ", Artist: metadataprovider.Artist{ID: "30000000-0000-0000-0000-000000000001", Name: "First"}}, {Name: "Two", Artist: metadataprovider.Artist{ID: "30000000-0000-0000-0000-000000000002", Name: "Second"}}},
		Media:        []metadataprovider.Medium{{Position: 1, Tracks: []metadataprovider.ReleaseTrack{{ID: "40000000-0000-0000-0000-000000000001", Position: 1, Number: "A1", Title: "Track", Recording: metadataprovider.Recording{ID: "50000000-0000-0000-0000-000000000001", Title: "Recording"}}}}},
	}}
}
func TestMBEvidenceDetachedTypedIdentities(t *testing.T) {
	input := mbEvidenceFixture()
	got, e := stageMBRelease(input)
	if e != nil {
		t.Fatal(e)
	}
	input.Release.Title = "Changed"
	input.Release.ArtistCredit[0].JoinPhrase = " changed "
	input.Release.Media[0].Tracks[0].Recording.Title = "Changed recording"
	if got.requested == got.value.ID || got.value.ID == got.value.ReleaseGroup.ID || got.value.Media[0].Tracks[0].ID == got.value.Media[0].Tracks[0].Recording.ID {
		t.Fatal("distinct typed identities collapsed")
	}
	if got.value.Title != "Edition" || got.value.Media[0].Tracks[0].Recording.Title != "Recording" || mbArtist(got.value.ArtistCredit) != "One feat. Two" {
		t.Fatal("caller mutation changed staged evidence or credits lost")
	}
	var stored metadataprovider.Release
	if e = json.Unmarshal(got.payload, &stored); e != nil || stored.Title != "Edition" {
		t.Fatal("payload differs from detached stage", e)
	}
	recording, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: "50000000-0000-0000-0000-000000000001", Recording: got.value.Media[0].Tracks[0].Recording})
	if e != nil {
		t.Fatal(e)
	}
	if recording.digest == got.digest {
		t.Fatal("typed digest domains collided")
	}
}
func TestMBEvidenceRejectsMalformedOwnership(t *testing.T) {
	cases := map[string]func(*metadataprovider.ReleaseLookup){
		"requested":        func(r *metadataprovider.ReleaseLookup) { r.RequestedID = "wrong" },
		"group":            func(r *metadataprovider.ReleaseLookup) { r.Release.ReleaseGroup.ID = "wrong" },
		"duplicate medium": func(r *metadataprovider.ReleaseLookup) { r.Release.Media = append(r.Release.Media, r.Release.Media[0]) },
		"duplicate track": func(r *metadataprovider.ReleaseLookup) {
			v := r.Release.Media[0].Tracks[0]
			v.Position = 2
			r.Release.Media[0].Tracks = append(r.Release.Media[0].Tracks, v)
		},
		"duplicate position": func(r *metadataprovider.ReleaseLookup) {
			v := r.Release.Media[0].Tracks[0]
			v.ID = "40000000-0000-0000-0000-000000000002"
			r.Release.Media[0].Tracks = append(r.Release.Media[0].Tracks, v)
		},
		"zero position": func(r *metadataprovider.ReleaseLookup) { r.Release.Media[0].Tracks[0].Position = 0 },
		"invalid utf8":  func(r *metadataprovider.ReleaseLookup) { r.Release.Title = string([]byte{0xff}) },
		"nul":           func(r *metadataprovider.ReleaseLookup) { r.Release.Title = "bad\x00text" },
		"negative length": func(r *metadataprovider.ReleaseLookup) {
			n := -1
			r.Release.Media[0].Tracks[0].Recording.LengthMillis = &n
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := mbEvidenceFixture()
			mutate(&r)
			if _, e := stageMBRelease(r); e == nil {
				t.Fatal("malformed release accepted")
			}
		})
	}
}
func TestMBEvidenceCanonicalOrderAndChangedRecording(t *testing.T) {
	r := mbEvidenceFixture()
	second := r.Release.Media[0].Tracks[0]
	second.ID = "40000000-0000-0000-0000-000000000002"
	second.Position = 2
	r.Release.Media[0].Tracks = append(r.Release.Media[0].Tracks, second)
	first, e := stageMBRelease(r)
	if e != nil {
		t.Fatal(e)
	}
	r.Release.Media[0].Tracks[0], r.Release.Media[0].Tracks[1] = r.Release.Media[0].Tracks[1], r.Release.Media[0].Tracks[0]
	r.Release.ID = strings.ToUpper(r.Release.ID)
	same, e := stageMBRelease(r)
	if e != nil {
		t.Fatal(e)
	}
	if first.digest != same.digest {
		t.Fatal("equivalent ordered evidence differs")
	}
	r.Release.Media[0].Tracks[0].Recording.Title = "New shared recording description"
	changed, e := stageMBRelease(r)
	if e != nil {
		t.Fatal(e)
	}
	if changed.digest == first.digest || first.value.Media[0].Tracks[0].Recording.Title != "Recording" {
		t.Fatal("changed acquisition reused or rewrote old evidence")
	}
}
func TestMBEvidenceCardinalityAndEncodedBudget(t *testing.T) {
	for _, count := range []int{mbEvidenceTracks, mbEvidenceTracks + 1} {
		r := mbEvidenceFixture()
		r.Release.Media[0].Tracks = nil
		for i := 0; i < count; i++ {
			r.Release.Media[0].Tracks = append(r.Release.Media[0].Tracks, metadataprovider.ReleaseTrack{ID: fmt.Sprintf("40000000-0000-0000-0000-%012d", i), Position: i + 1, Recording: metadataprovider.Recording{ID: "50000000-0000-0000-0000-000000000001"}})
		}
		_, e := stageMBRelease(r)
		if (e == nil) != (count == mbEvidenceTracks) {
			t.Fatalf("count %d: %v", count, e)
		}
	}
	r := mbEvidenceFixture()
	r.Release.ArtistCredit = make([]metadataprovider.ArtistCredit, mbEvidenceCredits+1)
	if _, e := stageMBRelease(r); e == nil {
		t.Fatal("overbudget credits accepted")
	}
	// JSON escapes amplify valid text, so the encoded budget must be enforced
	// independently of the preliminary raw string-length accounting.
	r = mbEvidenceFixture()
	r.Release.Media[0].Tracks = nil
	for i := 0; i < 200; i++ {
		r.Release.Media[0].Tracks = append(r.Release.Media[0].Tracks, metadataprovider.ReleaseTrack{ID: fmt.Sprintf("40000000-0000-0000-0000-%012d", i), Position: i + 1, Title: strings.Repeat("<", 4096), Recording: metadataprovider.Recording{ID: "50000000-0000-0000-0000-000000000001"}})
	}
	if _, e := stageMBRelease(r); e == nil {
		t.Fatal("encoded response budget bypassed")
	}
}
