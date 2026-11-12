package api

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

// What a device plays says what its queue plays next, for "Next up" on the account's other
// devices; a profile that may not see that title is not told it, and a queue at its end has
// nothing next.
func TestWhatADevicePlaysSaysWhatComesNext(t *testing.T) {
	th := newTheatre(t)
	album, tracks := th.album("Record", 3)
	made := th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}, StartAt: tracks[0]})
	if code, _ := th.start(th.owner.AccessToken, "", PlaybackStart{Queue: &playback.QueueLink{QueueID: made.Queue.ID, EntryID: made.Queue.Current.EntryID}}); code != 201 {
		t.Fatalf("starting the queue answered %d", code)
	}
	playing := get[AccountPlayback](t, th.s, th.owner.AccessToken, "/v1/playback/sessions").Items
	if len(playing) != 1 || playing[0].Next == nil || playing[0].Next.ItemID != tracks[1] || playing[0].Next.Title != "Band: Record 002" {
		t.Fatalf("the account's playing reads %+v", playing)
	}

	var kid Profile
	call(t, th.s, "POST", "/v1/profiles", th.owner.AccessToken, ProfileCreate{Name: "Kid"}, &kid)
	if code := status(t, th.s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", th.owner.AccessToken, Restrictions{AllLibraries: false, Libraries: []string{}}); code != 200 {
		t.Fatalf("keeping the kid from every library answered %d", code)
	}
	var onKid Session
	call(t, th.s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, th.s, 1).Session.AccessToken, ProfileSelect{}, &onKid)
	w := call(t, th.s, "GET", "/v1/playback/sessions", onKid.AccessToken, nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Record 002") || strings.Contains(w.Body.String(), tracks[1]) {
		t.Fatalf("a profile that may not see the next song read %d: %s", w.Code, w.Body)
	}

	// From the last song nothing follows.
	last := th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}, StartAt: tracks[2]})
	th.start(th.owner.AccessToken, "", PlaybackStart{Queue: &playback.QueueLink{QueueID: last.Queue.ID, EntryID: last.Queue.Current.EntryID}})
	for _, p := range get[AccountPlayback](t, th.s, th.owner.AccessToken, "/v1/playback/sessions").Items {
		if p.ItemID == tracks[2] && p.Next != nil {
			t.Fatalf("the last song of the queue says %+v plays next", p.Next)
		}
	}
}
