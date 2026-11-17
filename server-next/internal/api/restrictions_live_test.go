package api

import (
	"context"
	"testing"
)

// The two switches read back, survive saves of other fields and actually govern the guide,
// watching and bookings. An owner's profile can narrow its account too.
func TestProfileLiveAndRecordingChoicesAreEnforced(t *testing.T) {
	s, owner, _ := recordable(t)
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onKid)
	path := "/v1/profiles/" + kid.ID + "/restrictions"
	read := get[Restrictions](t, s, owner, path)
	if read.AllowLiveTV == nil || !*read.AllowLiveTV || read.AllowDVR == nil || !*read.AllowDVR || !read.AccountAllowLiveTV || !read.AccountAllowDVR {
		t.Fatalf("defaults: %+v", read)
	}
	programme := programmeID(t, s, owner, "Late News")
	channel := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items[0].ID
	save := func(body any) Restrictions {
		t.Helper()
		var out Restrictions
		if w := call(t, s, "PUT", path, owner, body, &out); w.Code != 200 {
			t.Fatalf("save: %d %s", w.Code, w.Body)
		}
		return out
	}
	save(map[string]any{"allowDvr": false})
	if got := save(map[string]any{"allowDownloads": false}); *got.AllowDVR || !*got.AllowLiveTV || !got.AccountAllowDVR {
		t.Fatalf("another field undid the recording choice: %+v", got)
	}
	if get[DvrOverview](t, s, onKid.AccessToken, "/v1/dvr").CanRecord || get[LiveProgrammeDetail](t, s, onKid.AccessToken, "/v1/live/programmes/"+programme).CanRecord {
		t.Fatal("the profile still offers Record")
	}
	for path, body := range map[string]any{
		"/v1/dvr/recordings": DvrRecordingBook{Programme: programme},
		"/v1/dvr/rules":      DvrRuleSave{Programme: programme},
	} {
		if w := call(t, s, "POST", path, onKid.AccessToken, body, nil); w.Code != 403 {
			t.Fatalf("recording switch ignored at %s: %d %s", path, w.Code, w.Body)
		}
	}
	save(map[string]any{"allowDvr": true, "allowLiveTv": false})
	for _, path := range []string{"/v1/live/channels", "/v1/live/guide", "/v1/live/programmes/" + programme} {
		if w := call(t, s, "GET", path, onKid.AccessToken, nil, nil); w.Code != 404 {
			t.Fatalf("Live TV switch ignored at %s: %d %s", path, w.Code, w.Body)
		}
	}
	if w := call(t, s, "POST", "/v1/playback/sessions", onKid.AccessToken, PlaybackStart{Channel: channel}, nil); w.Code != 404 {
		t.Fatalf("watching with Live TV off: %d %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/v1/dvr/recordings", onKid.AccessToken, DvrRecordingBook{Programme: programme}, nil); w.Code != 404 {
		t.Fatalf("booking with Live TV off: %d %s", w.Code, w.Body)
	}
	for _, row := range get[Page[DvrPermission]](t, s, owner, "/v1/admin/dvr/permissions").Items {
		if row.ProfileID == kid.ID && (row.LiveAllowed || row.CanRecord) {
			t.Fatalf("permission read differs from the routes: %+v", row)
		}
	}
	save(map[string]any{"allowLiveTv": true})
	if got := get[Restrictions](t, s, owner, path); !*got.AllowLiveTV || !*got.AllowDVR || *got.AllowDownloads {
		t.Fatalf("readback lost another switch: %+v", got)
	}
	if w := call(t, s, "POST", "/v1/dvr/recordings", onKid.AccessToken, DvrRecordingBook{Programme: programme}, nil); w.Code != 200 {
		t.Fatalf("recording was not restored: %d %s", w.Code, w.Body)
	}
	// The main owner's Live TV setting is a real profile choice too.
	main, err := s.db.ProfileByID(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, s, "PUT", "/v1/profiles/"+main.PublicID+"/restrictions", owner, map[string]any{"allowLiveTv": false}, nil); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := call(t, s, "GET", "/v1/live/channels", owner, nil, nil); w.Code != 404 {
		t.Fatalf("the main owner bypassed its profile choice: %d %s", w.Code, w.Body)
	}
}

func TestProfileRecordingChoiceCannotLiftTheAccount(t *testing.T) {
	s, owner, members := seededWith(t, 20, 1)
	var member DvrPermission
	for _, row := range get[Page[DvrPermission]](t, s, owner, "/v1/admin/dvr/permissions").Items {
		if !row.OwnerAccount {
			member = row
		}
	}
	path := "/v1/profiles/" + member.ProfileID + "/restrictions?member=" + member.AccountID
	var out Restrictions
	if w := call(t, s, "PUT", path, owner, map[string]any{"allowDvr": true, "accountAllowDvr": true}, &out); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if out.AllowDVR == nil || !*out.AllowDVR || out.AccountAllowDVR {
		t.Fatalf("a profile lifted the account's recording choice: %+v", out)
	}
	if get[DvrOverview](t, s, members[0], "/v1/dvr").CanRecord {
		t.Fatal("account ceiling did not reach the recording routes")
	}
	if w := call(t, s, "POST", "/v1/dvr/recordings", members[0], DvrRecordingBook{Programme: "missing"}, nil); w.Code != 403 {
		t.Fatalf("booking passed the account ceiling: %d %s", w.Code, w.Body)
	}
}
