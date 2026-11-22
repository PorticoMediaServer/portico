package api

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

// A queue says what it plays from, for Now Playing's "Playing from": an album by its title and
// a playlist by its name as it is now, with the entries that came with it naming it and what was
// added by hand naming nothing. Titles named one by one play from no name of their own.
func TestAQueueNamesWhatItPlaysFrom(t *testing.T) {
	th := newTheatre(t)
	album, tracks := th.album("After Hours", 3)
	_, others := th.album("Starboy", 2)

	made := th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}})
	want := QueueSegment{ID: "source", Kind: "selector", Label: "After Hours", Source: QueueSegmentSource{Kind: "container", ID: album}}
	if len(made.Queue.Segments) != 1 || made.Queue.Segments[0] != want {
		t.Fatalf("an album's queue plays from %+v", made.Queue.Segments)
	}
	for _, e := range made.Window {
		if e.SegmentID != "source" {
			t.Fatalf("an album's song %s names segment %q", e.Title, e.SegmentID)
		}
	}
	var added Queue
	add := QueueAdd{Source: playqueue.QueueSource{Container: others[0]}, Placement: "end"}
	if w := th.queue("POST", "/v1/queues/"+made.Queue.ID+"/entries", made.Queue.Revision, add, &added); w.Code != 200 {
		t.Fatalf("adding a song answered %d: %s", w.Code, w.Body)
	}
	var page QueueEntries
	th.queue("GET", "/v1/queues/"+made.Queue.ID+"/entries?from=0&limit=10", "", nil, &page)
	if len(page.Items) != 4 || page.Items[3].ItemID != others[0] || page.Items[3].SegmentID != "" || page.Items[0].SegmentID != "source" {
		t.Fatalf("after adding a song by hand: %+v", page.Items)
	}

	var list ListCreated
	if w := th.queue("POST", "/v1/lists", "", ListCreate{Kind: "playlist", Name: "Late Night", Items: tracks[:2]}, &list); w.Code != 201 {
		t.Fatalf("making a playlist answered %d: %s", w.Code, w.Body)
	}
	fromList := th.create(QueueCreate{Source: playqueue.QueueSource{List: list.ID}})
	if s := fromList.Queue.Segments; len(s) != 1 || s[0].Label != "Late Night" || s[0].Source != (QueueSegmentSource{Kind: "list", ID: list.ID}) {
		t.Fatalf("a playlist's queue plays from %+v", s)
	}
	name := "After Midnight"
	if w := th.queue("PATCH", "/v1/lists/"+list.ID, list.Revision, ListEdit{Name: &name}, nil); w.Code != 200 {
		t.Fatalf("renaming the playlist answered %d: %s", w.Code, w.Body)
	}
	var again Queue
	th.queue("GET", "/v1/queues/"+fromList.Queue.ID, "", nil, &again)
	if len(again.Segments) != 1 || again.Segments[0].Label != "After Midnight" {
		t.Fatalf("after the playlist was renamed the queue plays from %+v", again.Segments)
	}

	// A saved view played through its list id plays as the view it is, and from its name.
	var view ListCreated
	if w := th.queue("POST", "/v1/lists", "", ListCreate{Kind: "saved_view", Name: "Band songs", Library: th.libs[db.LibraryMusic].PublicID, Query: "kind=track"}, &view); w.Code != 201 {
		t.Fatalf("saving a view answered %d: %s", w.Code, w.Body)
	}
	fromView := th.create(QueueCreate{Source: playqueue.QueueSource{List: view.ID}})
	if s := fromView.Queue.Segments; len(s) != 1 || s[0].Label != "Band songs" || s[0].Source != (QueueSegmentSource{Kind: "list", ID: view.ID}) {
		t.Fatalf("a saved view's queue plays from %+v", s)
	}

	named := th.create(QueueCreate{Source: playqueue.QueueSource{Items: []string{tracks[0], others[1]}}})
	if s := named.Queue.Segments; len(s) != 1 || s[0].Kind != "items" || s[0].Label != "" || s[0].Source.Kind != "items" {
		t.Fatalf("named songs play from %+v", s)
	}
}

// A queue taken on by another device plays from the same source, and a song added by hand there
// stays added by hand. A second profile of the account that takes on a queue played from a
// playlist it may not see is told neither the playlist's name nor its id.
func TestATakenOnQueueKeepsItsSourceAndWhatWasAddedByHand(t *testing.T) {
	th := newTheatre(t)
	album, _ := th.album("After Hours", 3)
	_, others := th.album("Starboy", 1)
	made := th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}})
	add := QueueAdd{Source: playqueue.QueueSource{Container: others[0]}, Placement: "end"}
	if w := th.queue("POST", "/v1/queues/"+made.Queue.ID+"/entries", made.Queue.Revision, add, nil); w.Code != 200 {
		t.Fatalf("adding a song answered %d: %s", w.Code, w.Body)
	}
	takeOn := func(token string, from string) db.Queue {
		t.Helper()
		claims, err := th.s.tokens.CheckAccess(token, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		caller := playback.Caller{Viewer: db.Viewer{UserID: claims.UserID, ProfileID: claims.ProfileID}, SignInID: claims.SessionID}
		q, err := th.s.queues.Create(context.Background(), caller, playqueue.CreateInput{Source: playqueue.QueueSource{Queue: from}})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	tv := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken
	copied := takeOn(tv, made.Queue.ID)
	var page QueueEntries
	if w := call(t, th.s, "GET", "/v1/queues/"+copied.PublicID+"/entries?from=0&limit=10", tv, nil, &page); w.Code != 200 || len(page.Items) != 4 {
		t.Fatalf("the copy's entries answered %d: %+v", w.Code, page.Items)
	}
	if page.Items[3].ItemID != others[0] || page.Items[3].SegmentID != "" || !page.Items[3].Added || page.Items[0].SegmentID != "source" {
		t.Fatalf("on the other device: %+v", page.Items)
	}
	if s := queueSegments(copied); len(s) != 1 || s[0].Label != "After Hours" {
		t.Fatalf("the copy plays from %+v", s)
	}

	var list ListCreated
	if w := th.queue("POST", "/v1/lists", "", ListCreate{Kind: "playlist", Name: "Private", Items: others}, &list); w.Code != 201 {
		t.Fatalf("making a playlist answered %d: %s", w.Code, w.Body)
	}
	fromList := th.create(QueueCreate{Source: playqueue.QueueSource{List: list.ID}})
	var kid Profile
	call(t, th.s, "POST", "/v1/profiles", th.owner.AccessToken, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	if w := call(t, th.s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, th.s, 3).Session.AccessToken, ProfileSelect{}, &onKid); w.Code != 200 {
		t.Fatalf("choosing the kid's profile answered %d", w.Code)
	}
	theirs := takeOn(onKid.AccessToken, fromList.Queue.ID)
	if s := queueSegments(theirs); len(s) != 1 || s[0].Label != "" || s[0].Source != (QueueSegmentSource{Kind: "list"}) {
		t.Fatalf("another profile taking on a queue from a playlist it may not see is told %+v", s)
	}
}
