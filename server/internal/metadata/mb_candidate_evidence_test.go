package metadata

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func TestMBCandidateEvidenceAlwaysRequiresOwnerReview(t *testing.T) {
	r := mbEvidenceFixture().Release
	values := []metadataprovider.Recording{{ID: "ABCDEF00-0000-0000-0000-000000000001", Title: "Exact title", ArtistCredit: r.ArtistCredit}}
	out, e := stageMBSongCandidates(values, "Exact title", "One feat. Two")
	if e != nil {
		t.Fatal(e)
	}
	if len(out) != 1 || out[0].id != "abcdef00-0000-0000-0000-000000000001" || out[0].entityType != "recording" || out[0].confidence >= 0.85 {
		t.Fatal("name candidate became automatic identity evidence", out)
	}
	if strings.Join(out[0].reasons, ",") != "provider_name_search,owner_review_required,exact_title,exact_artist" {
		t.Fatal("independent candidate reasons missing", out[0].reasons)
	}
	values[0].ArtistCredit[0].Name = "Changed"
	var saved metadataprovider.Recording
	if e = json.Unmarshal(out[0].payload, &saved); e != nil || mbArtist(saved.ArtistCredit) != "One feat. Two" {
		t.Fatal("candidate payload retained caller-owned credits", e)
	}
}
func TestMBCandidateEvidenceRejectsCaseFoldedDuplicatesAndBounds(t *testing.T) {
	id := "abcdef00-0000-0000-0000-000000000001"
	values := []metadataprovider.ReleaseCandidate{{ID: id, Title: "Edition"}, {ID: strings.ToUpper(id), Title: "Same identity"}}
	if _, e := stageMBAlbumCandidates(values, "Edition", ""); e == nil {
		t.Fatal("case-folded duplicate accepted")
	}
	if _, e := stageMBAlbumCandidates(make([]metadataprovider.ReleaseCandidate, 26), "", ""); e == nil {
		t.Fatal("candidate count truncated instead of rejected")
	}
	values = values[:1]
	values[0].Title = string([]byte{0xff})
	if _, e := stageMBAlbumCandidates(values, "", ""); e == nil {
		t.Fatal("invalid UTF8 candidate accepted")
	}
}
