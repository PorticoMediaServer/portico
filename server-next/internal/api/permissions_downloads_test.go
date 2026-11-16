package api

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"testing"
)

func TestDownloadSyncDoesNotRevealAnUnownedHiddenTitle(t *testing.T) {
	rig := newDownloadRig(t)
	reason := func(id string) string {
		t.Helper()
		var out DownloadSyncReply
		w := call(t, rig.s, "POST", "/v1/downloads/sync", rig.kid, DownloadSync{Holds: []HeldTitle{{DownloadID: "dl_absent", ItemID: id}}}, &out)
		if w.Code != 200 || len(out.Remove) != 1 {
			t.Fatalf("sync: %d", w.Code)
		}
		return out.Remove[0].Reason
	}
	hidden, absent := reason(rig.film), reason("itm_absent")
	if hidden != absent {
		t.Errorf("hidden reason %q, absent reason %q", hidden, absent)
	}
}

func TestDownloadSyncDoesNotOfferATitleAfterAccessIsLost(t *testing.T) {
	rig := newDownloadRig(t)
	var partner Profile
	call(t, rig.s, "POST", "/v1/profiles", rig.owner, ProfileCreate{Name: "Partner"}, &partner)
	var selected Session
	call(t, rig.s, "POST", "/v1/profiles/"+partner.ID+"/select", signIn(t, rig.s, 5).Session.AccessToken, ProfileSelect{}, &selected)
	code, dl := rig.ask(selected.AccessToken, DownloadAsk{ItemID: rig.film, Quality: "original"})
	if code != 201 {
		t.Fatal(code)
	}
	call(t, rig.s, "PUT", "/v1/profiles/"+partner.ID+"/restrictions", rig.owner, map[string]any{"maxRatingAge": 6}, nil)
	var got DownloadSyncReply
	// POST has a body; use the same sync a real device sends with nothing held yet.
	call(t, rig.s, "POST", "/v1/downloads/sync", selected.AccessToken, DownloadSync{Holds: []HeldTitle{}}, &got)
	if len(got.Fetch) != 0 {
		t.Errorf("hidden fetch: %+v", got.Fetch)
	}
	if code := status(t, rig.s, "POST", "/v1/downloads/"+dl.ID+"/items/"+rig.film+"/received", selected.AccessToken, nil); code != 404 {
		t.Errorf("acknowledged hidden title: HTTP %d", code)
	}
}

func TestDownloadRetryCountsOnlyTheTitlesStillVisible(t *testing.T) {
	rig := newDownloadRig(t)
	show := rig.importShow(1, 2)
	code, made := rig.ask(rig.owner, DownloadAsk{ItemID: show, Quality: "original"})
	if code != 201 || made.Counts.Titles != 2 {
		t.Fatal(code, made.Counts)
	}
	titles := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+made.ID).Items
	locks, err := rig.s.db.MetadataLocks(context.Background(), titles[0].ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.s.db.EditMetadata(context.Background(), titles[0].ItemID, locks.Revision, db.MetadataEdit{ContentRating: new("R")}); err != nil {
		t.Fatal(err)
	}
	main := get[Page[Profile]](t, rig.s, rig.owner, "/v1/profiles").Items[0].ID
	call(t, rig.s, "PUT", "/v1/profiles/"+main+"/restrictions", rig.owner, map[string]any{"maxRatingAge": 13, "allowUnrated": true}, nil)
	current := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+made.ID)
	if current.Counts.Titles != 1 {
		t.Fatal("fixture", current.Counts)
	}
	code, repeated := rig.ask(rig.owner, DownloadAsk{ItemID: show, Quality: "original"})
	if code != 200 || repeated.Counts != current.Counts {
		t.Errorf("duplicate request returned hidden counts: HTTP %d %+v; current %+v", code, repeated.Counts, current.Counts)
	}
}

func TestOwnedDownloadCannotProbeUntrackedTitleIDs(t *testing.T) {
	rig := newDownloadRig(t)
	show := rig.importShow(1, 1)
	code, dl := rig.ask(rig.owner, DownloadAsk{ItemID: show, Quality: "original"})
	if code != 201 {
		t.Fatal(code)
	}
	main := get[Page[Profile]](t, rig.s, rig.owner, "/v1/profiles").Items[0].ID
	call(t, rig.s, "PUT", "/v1/profiles/"+main+"/restrictions", rig.owner, map[string]any{"maxRatingAge": 12, "allowUnrated": true}, nil)
	for _, item := range []string{rig.film, "itm_absent"} {
		var out DownloadSyncReply
		if w := call(t, rig.s, "POST", "/v1/downloads/sync", rig.owner, DownloadSync{Holds: []HeldTitle{{DownloadID: dl.ID, ItemID: item}}}, &out); w.Code != 200 {
			t.Fatal(w.Code)
		}
		if len(out.Remove) != 0 {
			t.Errorf("untracked title distinguished from a removed offline title: %+v", out.Remove)
		}
	}
}
