package api

import (
	"io"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

// An account held to the home network, or outside its viewing hours,
// gets no token and no stream. A download's address carries its own permission and is good
// for up to seven days, so it must be held to the same two limits: otherwise the account
// fetches whole films away from home, or after its hours have ended, and watches them offline.
func TestADownloadAddressIsHeldToHomeAndToViewingHours(t *testing.T) {
	rig := newDownloadRig(t)
	max := addMember(t, rig.s, rig.owner, "max", "member")
	token := signInAs(t, rig.s, "max", testPassword, 7).AccessToken
	code, asked := rig.ask(token, DownloadAsk{ItemID: rig.film, Quality: "original"})
	if code != 201 {
		t.Fatalf("asking answered %d", code)
	}
	var item DownloadedItem
	if w := call(t, rig.s, "GET", "/v1/downloads/"+asked.ID+"/items/"+rig.film, token, nil, &item); w.Code != 200 || len(item.Files) != 1 {
		t.Fatalf("the title's files answered %d: %s", w.Code, w.Body)
	}
	fetchFrom := func(peer string) int {
		r := newRequest("GET", item.Files[0].URL, "", nil)
		r.RemoteAddr = peer + ":4000"
		r.Header.Set("Range", "bytes=0-99")
		res := serve(rig.s, r).Result()
		io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	if got := fetchFrom("127.0.0.1"); got != 206 {
		t.Fatalf("at home, held to nothing, the file answered %d", got)
	}

	limits := noLimits()
	limits.HomeOnly = true
	hold(t, rig.s, rig.owner, max.ID, limits)
	if got := fetchFrom("203.0.113.9"); got == 206 {
		t.Errorf("an account held to the home network fetched its download from away: %d", got)
	}

	limits = noLimits()
	limits.ViewingHours = &auth.ViewingHours{Timezone: "UTC", Windows: []auth.ViewingWindow{{Days: []int{int(rig.s.now().UTC().Weekday()+3) % 7}, StartMinute: 600, EndMinute: 660}}}
	hold(t, rig.s, rig.owner, max.ID, limits)
	if got := fetchFrom("127.0.0.1"); got == 206 {
		t.Errorf("an account outside its viewing hours fetched its download: %d", got)
	}

	// An account that may not make the server convert downloads titles only as they are.
	limits = noLimits()
	limits.AllowConversion = false
	hold(t, rig.s, rig.owner, max.ID, limits)
	fresh := signInAs(t, rig.s, "max", testPassword, 8).AccessToken
	if code, _ := rig.ask(fresh, DownloadAsk{ItemID: rig.film, Quality: "480p"}); code != 403 {
		t.Errorf("an account that may not convert asking for a smaller copy answered %d, want 403", code)
	}
	if code, _ := rig.ask(fresh, DownloadAsk{ItemID: rig.film, Quality: "original"}); code != 201 {
		t.Errorf("an account that may not convert asking for the file as it is answered %d, want 201", code)
	}
}
