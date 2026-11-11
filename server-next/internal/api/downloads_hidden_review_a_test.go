package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// Review desk A, #2738. What a profile may not see is absent everywhere, downloads included
// (rules that apply everywhere, rule 5). A profile that asked for a show and was then limited
// to another library must not go on reading the show's name and its episodes' names from the
// Downloads list, which now carries every title.
func TestReviewAATheDownloadsListDoesNotNameWhatTheProfileMayNoLongerSee(t *testing.T) {
	rig := newDownloadRig(t)
	show := rig.importShow(1, 2)
	var partner Profile
	call(t, rig.s, "POST", "/v1/profiles", rig.owner, ProfileCreate{Name: "Partner"}, &partner)
	var onPartner Session
	call(t, rig.s, "POST", "/v1/profiles/"+partner.ID+"/select", signIn(t, rig.s, 2).Session.AccessToken, ProfileSelect{}, &onPartner)
	if code, _ := rig.ask(onPartner.AccessToken, DownloadAsk{ItemID: show, Quality: "original"}); code != 201 {
		t.Fatalf("the partner asking answered %d", code)
	}
	before := get[Page[Download]](t, rig.s, onPartner.AccessToken, "/v1/downloads").Items
	if len(before) != 1 || len(before[0].Items) != 2 {
		t.Fatalf("before the limit the list is %+v", before)
	}
	names := []string{before[0].Items[0].Title, before[0].Items[1].Title, before[0].Items[0].ItemID, before[0].Items[1].ItemID}
	limited := Restrictions{Libraries: []string{rig.films.PublicID}}
	if w := call(t, rig.s, "PUT", "/v1/profiles/"+partner.ID+"/restrictions", rig.owner, limited, nil); w.Code != 200 {
		t.Fatalf("limiting the partner answered %d: %s", w.Code, w.Body)
	}
	if code := status(t, rig.s, "GET", "/v1/items/"+show, onPartner.AccessToken, nil); code != 404 {
		t.Fatalf("the show itself answers the limited profile %d, want 404", code)
	}
	raw, _ := json.Marshal(get[Page[Download]](t, rig.s, onPartner.AccessToken, "/v1/downloads"))
	for _, name := range names {
		if name != "" && strings.Contains(string(raw), name) {
			t.Errorf("the limited profile's Downloads list still holds %q", name)
		}
	}
	t.Logf("the limited profile's list: %.500s", raw)
}
