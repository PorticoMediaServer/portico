package api

import (
	"testing"

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

	named := th.create(QueueCreate{Source: playqueue.QueueSource{Items: []string{tracks[0], others[1]}}})
	if s := named.Queue.Segments; len(s) != 1 || s[0].Kind != "items" || s[0].Label != "" || s[0].Source.Kind != "items" {
		t.Fatalf("named songs play from %+v", s)
	}
}
