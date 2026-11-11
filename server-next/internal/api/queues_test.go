package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

// album adds an album of n songs to the music library and returns its id and the songs' ids
// in track order.
func (th *theatre) album(name string, n int) (string, []string) {
	th.Helper()
	ctx := context.Background()
	lib := th.libs[db.LibraryMusic]
	artist, record := db.ItemKey{Kind: db.KeyName, Key: "band"}, db.ItemKey{Kind: db.KeyFolder, Key: name}
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 2, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		{Kind: db.KindArtist, Keys: []db.ItemKey{artist}, Title: "Band"},
		{Kind: db.KindAlbum, Keys: []db.ItemKey{record}, Parent: &artist, Title: name},
	}}
	age, disc := 0, 1
	batch.Items[0].RatingAge = &age
	for i := 1; i <= n; i++ {
		file := fmt.Sprintf("%s-%03d.flac", name, i)
		track := i
		batch.Files = append(batch.Files, db.ImportFile{FileRef: db.FileRef{Name: file}, Role: db.FileMedia, Size: 1, DurationMS: 180_000,
			Streams: []db.Stream{{Kind: db.StreamAudio, Codec: "flac", Default: true}}})
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindTrack, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: file}}, Parent: &record,
			Title: fmt.Sprintf("%s %03d", name, i), IndexNumber: &track, ParentIndex: &disc,
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: file}}}}}})
	}
	if _, err := th.s.db.Import(ctx, batch); err != nil {
		th.Fatal(err)
	}
	viewer := db.Viewer{UserID: 1, ProfileID: 1}
	albums, err := th.s.db.Items(ctx, viewer, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindAlbum}})
	if err != nil {
		th.Fatal(err)
	}
	var id string
	for _, a := range albums.Items {
		if a.Title == name {
			id = a.PublicID
		}
	}
	tracks, err := th.s.db.Items(ctx, viewer, db.ItemQuery{Parent: id, Limit: 200})
	if err != nil || len(tracks.Items) != n {
		th.Fatalf("album %s: %d tracks, %v", name, len(tracks.Items), err)
	}
	ids := make([]string, n)
	for i, track := range tracks.Items {
		ids[i] = track.PublicID
	}
	return id, ids
}

// queue sends one queue request with the revision in If-Match and returns the status.
func (th *theatre) queue(method, path, revision string, body, out any) *httptest.ResponseRecorder {
	th.Helper()
	r := newRequest(method, path, th.owner.AccessToken, body)
	if revision != "" {
		r.Header.Set("If-Match", `"`+revision+`"`)
	}
	w := serve(th.s, r)
	if out != nil && w.Code < 300 {
		// Absent fields must read as absent, not as what an earlier answer left behind.
		reflect.ValueOf(out).Elem().SetZero()
		if err := jsonInto(w, out); err != nil {
			th.Fatalf("%s %s: %v: %s", method, path, err, w.Body)
		}
	}
	return w
}

// order reads the whole queue and returns its titles' ids in play order.
func (th *theatre) order(id string) []string {
	th.Helper()
	var out []string
	for from := 0; ; from += 200 {
		var page QueueEntries
		if w := th.queue("GET", fmt.Sprintf("/v1/queues/%s/entries?from=%d&limit=200", id, from), "", nil, &page); w.Code != 200 {
			th.Fatalf("entries answered %d: %s", w.Code, w.Body)
		}
		for _, e := range page.Items {
			out = append(out, e.ItemID)
		}
		if len(page.Items) < 200 {
			return out
		}
	}
}

func (th *theatre) create(body QueueCreate) QueueCreated {
	th.Helper()
	var out QueueCreated
	if w := th.queue("POST", "/v1/queues", "", body, &out); w.Code != 201 {
		th.Fatalf("creating a queue answered %d: %s", w.Code, w.Body)
	}
	return out
}

// An album plays from the song that was pressed; entries can be added next and last, moved
// and removed; an edit on an out-of-date copy is refused; the queue is paged; repeat and
// the end of the queue behave; and all of it is still there after a restart.
func TestQueueEditsPagingRepeatAndRestart(t *testing.T) {
	th := newTheatre(t)
	album, tracks := th.album("Record", 12)
	made := th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}, StartAt: tracks[2]})
	q, path := made.Queue, "/v1/queues/"+made.Queue.ID
	if q.Total != 12 || q.Current == nil || q.Current.Position != 2 || q.RemainingCount != 9 || q.RemainingMs != 9*180_000 ||
		q.Medium != "audio" || len(made.Window) != 12 || made.Window[2].ItemID != tracks[2] || made.Window[0].ParentTitle != "Record" {
		t.Fatalf("a new queue: %+v, window of %d", q, len(made.Window))
	}
	// The current entry is started as an ordinary session that names the queue.
	code, session := th.start(th.owner.AccessToken, "", PlaybackStart{Queue: &playback.QueueLink{QueueID: q.ID, EntryID: q.Current.EntryID}})
	if code != 201 || session.ItemID != tracks[2] || session.Queue == nil || session.Queue.EntryID != q.Current.EntryID {
		t.Fatalf("starting the current entry answered %d %+v", code, session)
	}

	// Play next, then add last; both are marked as added by the person.
	if w := th.queue("POST", path+"/entries", q.Revision, QueueAdd{Source: playqueue.QueueSource{Items: []string{th.song}}, Placement: "next"}, &q); w.Code != 200 {
		t.Fatalf("play next answered %d: %s", w.Code, w.Body)
	}
	if w := th.queue("POST", path+"/entries", q.Revision, QueueAdd{Source: playqueue.QueueSource{Container: tracks[0]}}, &q); w.Code != 200 {
		t.Fatalf("add last answered %d: %s", w.Code, w.Body)
	}
	want := slices.Concat(tracks[:3], []string{th.song}, tracks[3:], []string{tracks[0]})
	if got := th.order(q.ID); !slices.Equal(got, want) {
		t.Fatalf("after adding:\n got %v\nwant %v", got, want)
	}
	var page QueueEntries
	th.queue("GET", path+"/entries?around=current&before=1&after=2", "", nil, &page)
	if len(page.Items) != 4 || page.Start != 1 || !page.Items[2].Added || page.Items[1].Added || page.Total != 14 {
		t.Fatalf("the window around the current entry: %+v", page)
	}
	added, last := page.Items[2].EntryID, page.Items[3].EntryID
	// An edit on an out-of-date copy is refused, and so is one that names no revision.
	if w := th.queue("DELETE", path+"/entries/"+added, made.Queue.Revision, nil, nil); w.Code != 412 {
		t.Fatalf("a stale edit answered %d", w.Code)
	}
	if w := th.queue("DELETE", path+"/entries/"+added, "", nil, nil); w.Code != 428 {
		t.Fatalf("an edit with no revision answered %d", w.Code)
	}
	// Move the added song to after the one that followed it, then remove it.
	if w := th.queue("POST", path+"/entries/"+added+"/move", q.Revision, QueueMove{After: last}, &q); w.Code != 200 {
		t.Fatalf("move answered %d: %s", w.Code, w.Body)
	}
	want = slices.Concat(tracks[:4], []string{th.song}, tracks[4:], []string{tracks[0]})
	if got := th.order(q.ID); !slices.Equal(got, want) {
		t.Fatalf("after moving:\n got %v\nwant %v", got, want)
	}
	if w := th.queue("DELETE", path+"/entries/"+added, q.Revision, nil, &q); w.Code != 200 || q.Total != 13 {
		t.Fatalf("remove answered %d, total %d", w.Code, q.Total)
	}
	if w := th.queue("DELETE", path+"/entries/"+q.Current.EntryID, q.Revision, nil, nil); w.Code != 409 {
		t.Fatalf("removing the entry that is playing answered %d", w.Code)
	}

	// Repeat one plays the same entry again when it ends, but Next still moves on.
	th.queue("PATCH", path, "", QueueChange{Repeat: "one"}, &q)
	current := q.Current.EntryID
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "completion"}, &q)
	if q.Current.EntryID != current || q.AutomaticAdvances != 1 {
		t.Fatalf("repeat one moved on: %+v", q)
	}
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "next"}, &q)
	if q.Current.Position != 3 || q.AutomaticAdvances != 0 {
		t.Fatalf("next under repeat one: %+v", q)
	}
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "previous"}, &q)
	if q.Current.Position != 2 {
		t.Fatalf("previous: %+v", q)
	}
	// A Next whose answer was lost is sent again, naming the entry it left: it moves once.
	th.queue("PATCH", path, "", QueueChange{Repeat: "off"}, &q)
	resent := QueueAdvance{Reason: "next", FromEntryID: q.Current.EntryID}
	th.queue("POST", path+"/advance", "", resent, &q)
	th.queue("POST", path+"/advance", "", resent, &q)
	if q.Current.Position != 3 {
		t.Fatalf("a repeated Next moved to %+v", q.Current)
	}
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "previous"}, &q)
	th.queue("PATCH", path, "", QueueChange{Repeat: "one"}, &q)
	// Jump to the last entry. With repeat off the queue ends there; with repeat all it wraps.
	th.queue("PATCH", path, "", QueueChange{Repeat: "off"}, &q)
	th.queue("GET", path+"/entries?from=12&limit=1", "", nil, &page)
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "entry", EntryID: page.Items[0].EntryID}, &q)
	if q.Current.Position != 12 || q.NextEntryID != "" {
		t.Fatalf("at the last entry: %+v", q)
	}
	if w := th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "completion"}, nil); w.Code != 409 || !strings.Contains(w.Body.String(), "queue_ended") {
		t.Fatalf("past the end answered %d: %s", w.Code, w.Body)
	}
	th.queue("PATCH", path, "", QueueChange{Repeat: "all"}, &q)
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "completion"}, &q)
	if q.Current.Position != 0 {
		t.Fatalf("repeat all did not wrap: %+v", q)
	}
	// Saved as a playlist, once however often it is asked.
	var saved, again SavedPlaylist
	for _, out := range []*SavedPlaylist{&saved, &again} {
		r := newRequest("POST", path+"/playlist", th.owner.AccessToken, QueueSave{Name: "Evening"})
		r.Header.Set("Idempotency-Key", "save-1")
		if w := serve(th.s, r); w.Code != 201 || jsonInto(w, out) != nil {
			t.Fatalf("saving as a playlist answered %d: %s", w.Code, w.Body)
		}
	}
	if saved.Entries != 13 || saved.PlaylistID != again.PlaylistID {
		t.Fatalf("saved %+v, then %+v", saved, again)
	}

	// After a restart the queue has the same order, current entry and repeat.
	before := th.order(q.ID)
	closeDB(th.s.db)
	restarted := openTestServer(t, th.path)
	after, header, err := restarted.db.QueueEntries(context.Background(), db.Viewer{UserID: 1, ProfileID: 1}, q.ID, 0, 200)
	if err != nil || header.Repeat != "all" || header.CurrentIndex != 0 || len(after) != len(before) {
		t.Fatalf("after a restart: %+v, %d entries, %v", header, len(after), err)
	}
	for n, e := range after {
		if e.Item.PublicID != before[n] {
			t.Fatalf("entry %d changed across the restart", n)
		}
	}
}

// Turning shuffle on keeps the current entry playing and shuffles the rest; turning it off
// carries on in the original order from the current entry. (Definition rule 48.)
func TestShuffleOnKeepsCurrentAndOffRestoresTheOrder(t *testing.T) {
	th := newTheatre(t)
	album, tracks := th.album("Record", 12)
	q := th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}, StartAt: tracks[4]}).Queue
	path, playing, on, off := "/v1/queues/"+q.ID, q.Current.EntryID, true, false
	th.queue("PATCH", path, q.Revision, QueueChange{Shuffle: &on}, &q)
	shuffled := th.order(q.ID)
	sorted := slices.Sorted(slices.Values(shuffled))
	if !q.Shuffle || q.Current.EntryID != playing || q.Current.Position != 0 || shuffled[0] != tracks[4] ||
		!slices.Equal(sorted, slices.Sorted(slices.Values(tracks))) || slices.Equal(shuffled[1:], slices.Concat(tracks[:4], tracks[5:])) {
		t.Fatalf("after shuffle on: %+v, order %v", q, shuffled)
	}
	// Something added while shuffled plays next, and stays beside the current entry afterwards.
	th.queue("POST", path+"/entries", q.Revision, QueueAdd{Source: playqueue.QueueSource{Items: []string{th.song}}, Placement: "next"}, &q)
	if got := th.order(q.ID); got[1] != th.song {
		t.Fatalf("play next in a shuffled queue: %v", got)
	}
	// Two entries on, shuffle goes off: the album's order is back and playback is where it was.
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "next"}, &q)
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "next"}, &q)
	playing = q.Current.EntryID
	item := th.order(q.ID)[2]
	th.queue("PATCH", path, q.Revision, QueueChange{Shuffle: &off}, &q)
	want := slices.Concat(tracks[:5], []string{th.song}, tracks[5:])
	if got := th.order(q.ID); q.Shuffle || q.Current.EntryID != playing || !slices.Equal(got, want) || got[q.Current.Position] != item {
		t.Fatalf("after shuffle off: %+v\n got %v\nwant %v", q, th.order(q.ID), want)
	}
	// A shuffled start from a container also keeps the original order to go back to.
	q = th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}, Shuffle: true}).Queue
	th.queue("PATCH", "/v1/queues/"+q.ID, q.Revision, QueueChange{Shuffle: &off}, &q)
	if got := th.order(q.ID); !slices.Equal(got, tracks) {
		t.Fatalf("a queue shuffled from the start, unshuffled: %v", got)
	}
}

// A view longer than a queue holds starts at once and refills as the listener nears the end:
// shuffled, every title plays once before any repeats; unshuffled, it follows the view from
// the title that was pressed. Turning shuffle off mid-way carries on in the view's order.
// The queue never holds more than its limit, and adding to a full queue is refused.
// (Definition rules 49 and 50.)
func TestALongViewRefillsAndAFullQueueRefusesMore(t *testing.T) {
	th := newTheatre(t)
	db.QueueMax, playqueue.Fill, playqueue.RefillBelow = 30, 10, 3
	t.Cleanup(func() { db.QueueMax, playqueue.Fill, playqueue.RefillBelow = 10_000, 1000, 200 })
	_, tracks := th.album("Record", 50)
	all := append([]string{th.song}, tracks...) // the view sorted by title: "Record 001"… then "Song"
	slices.Sort(all)
	titles := slices.Concat(tracks, []string{th.song})
	view := &playqueue.QueueView{Library: th.libs[db.LibraryMusic].PublicID, Kinds: []string{"track"}}

	q := th.create(QueueCreate{Source: playqueue.QueueSource{View: view}, Shuffle: true}).Queue
	path := "/v1/queues/" + q.ID
	if !q.Refills || !q.Shuffle || q.Total != 10 {
		t.Fatalf("a long shuffled view: %+v", q)
	}
	current := func() string {
		var page QueueEntries
		th.queue("GET", path+"/entries?around=current&before=0&after=0", "", nil, &page)
		if page.Total > db.QueueMax {
			t.Fatalf("the queue holds %d entries, more than its limit", page.Total)
		}
		return page.Items[0].ItemID
	}
	played := []string{current()}
	for len(played) < len(titles) {
		if w := th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "completion"}, &q); w.Code != 200 {
			t.Fatalf("advance %d answered %d: %s", len(played), w.Code, w.Body)
		}
		played = append(played, current())
	}
	if got := slices.Sorted(slices.Values(played)); !slices.Equal(got, slices.Sorted(slices.Values(titles))) {
		t.Fatalf("a shuffle of %d titles played %d different ones before the end", len(titles), len(slices.Compact(got)))
	}
	if slices.Equal(played, titles) {
		t.Fatal("the shuffle played in the view's own order")
	}
	if w := th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "completion"}, nil); w.Code != 409 {
		t.Fatalf("after everything had played, advance answered %d", w.Code)
	}

	// In the view's own order, from the title that was pressed.
	q = th.create(QueueCreate{Source: playqueue.QueueSource{View: view}, StartAt: tracks[19]}).Queue
	path = "/v1/queues/" + q.ID
	if got := th.order(q.ID); !slices.Equal(got, titles[19:29]) && !slices.Equal(got, titles[19:30]) {
		t.Fatalf("a long view from the 20th title holds %v", got)
	}
	for range 9 {
		th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "next"}, &q)
	}
	if got := current(); got != tracks[28] || q.Total <= 11 {
		t.Fatalf("nine titles on: playing %s with %d held", got, q.Total)
	}
	// Shuffle on keeps what is playing and shuffles the rest of the view; off carries on
	// from what is playing in the view's order.
	on, off := true, false
	th.queue("PATCH", path, "", QueueChange{Shuffle: &on}, &q)
	rest := th.order(q.ID)
	if rest[0] != tracks[28] || q.Current.Position != 0 || slices.Contains(rest[1:], tracks[28]) || slices.Equal(rest[1:4], tracks[29:32]) {
		t.Fatalf("shuffle on in a long view: %+v %v", q, rest)
	}
	th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "next"}, &q)
	playing := current()
	at := slices.Index(titles, playing)
	th.queue("PATCH", path, "", QueueChange{Shuffle: &off}, &q)
	got := th.order(q.ID)
	if got[0] != playing || q.Shuffle || (at+1 < len(titles) && got[1] != titles[at+1]) {
		t.Fatalf("shuffle off in a long view: playing %s (view place %d), then %v", playing, at, got[:min(3, len(got))])
	}

	// A queue that holds everything it plays is full at its limit.
	album, _ := th.album("Other", 28)
	q = th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}}).Queue
	path = "/v1/queues/" + q.ID
	th.queue("POST", path+"/entries", q.Revision, QueueAdd{Source: playqueue.QueueSource{Items: []string{th.song, tracks[0]}}}, &q)
	if q.Total != 30 {
		t.Fatalf("a queue filled to its limit holds %d", q.Total)
	}
	w := th.queue("POST", path+"/entries", q.Revision, QueueAdd{Source: playqueue.QueueSource{Items: []string{tracks[1]}}}, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "This queue is too long. Remove some of it or start a new one.") {
		t.Fatalf("adding to a full queue answered %d: %s", w.Code, w.Body)
	}
}

// A queue nobody has played for 30 days is removed; one played more recently is kept. A
// device keeps a music queue and a video queue side by side. (Definition rules 53 and 55.)
func TestIdleQueuesAreRemovedAfterThirtyDays(t *testing.T) {
	th := newTheatre(t)
	album, _ := th.album("Record", 3)
	music := th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}}).Queue
	video := th.create(QueueCreate{Source: playqueue.QueueSource{Container: th.film}}).Queue
	var mine Page[Queue]
	th.queue("GET", "/v1/queues", "", nil, &mine)
	if len(mine.Items) != 2 || video.Medium != "video" {
		t.Fatalf("the device's queues: %+v", mine.Items)
	}
	// Twenty days on the music is played; twenty more and only the video queue is idle.
	th.clock = th.clock.Add(20 * 24 * time.Hour)
	th.owner = onMain(t, th.s, signIn(t, th.s, 0))
	th.queue("POST", "/v1/queues/"+music.ID+"/advance", "", QueueAdvance{Reason: "next"}, nil)
	th.clock = th.clock.Add(20 * 24 * time.Hour)
	phone := onMain(t, th.s, signIn(t, th.s, 6))
	if w := call(t, th.s, "POST", "/v1/queues", phone.AccessToken, QueueCreate{Source: playqueue.QueueSource{Container: th.song}}, nil); w.Code != 201 {
		t.Fatalf("a new queue elsewhere answered %d", w.Code)
	}
	th.owner = onMain(t, th.s, signIn(t, th.s, 0))
	if w := th.queue("GET", "/v1/queues/"+video.ID, "", nil, nil); w.Code != 404 {
		t.Fatalf("a queue idle for 40 days answered %d", w.Code)
	}
	if w := th.queue("GET", "/v1/queues/"+music.ID, "", nil, nil); w.Code != 200 {
		t.Fatalf("a queue played 20 days ago answered %d", w.Code)
	}
}

// The next song is got ready before the current one ends and becomes the session when the
// app commits it: no second stream is counted, the same token gives the same session, and
// an edit to the queue in between cancels it.
func TestPrepareAndCommitNext(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	album, tracks := th.album("Record", 4)
	q := th.create(QueueCreate{Source: playqueue.QueueSource{Container: album}}).Queue
	path := "/v1/queues/" + q.ID
	_, first := th.start(token, "", PlaybackStart{Queue: &playback.QueueLink{QueueID: q.ID, EntryID: q.Current.EntryID}})
	var next PreparedNext
	if w := th.queue("POST", path+"/prepare-next", "", QueuePrepare{SessionID: first.ID}, &next); w.Code != 200 {
		t.Fatalf("prepare answered %d: %s", w.Code, w.Body)
	}
	if next.ItemID != tracks[1] || next.Presentation.URL == "" || th.engine.streams() != 2 || len(th.s.playback.All()) != 1 {
		t.Fatalf("prepared %+v with %d streams and %d sessions", next, th.engine.streams(), len(th.s.playback.All()))
	}
	var done, again QueueCommitted
	for _, out := range []*QueueCommitted{&done, &again} {
		if w := th.queue("POST", path+"/commit-next", "", QueueCommit{Token: next.Token}, out); w.Code != 200 {
			t.Fatalf("commit answered %d: %s", w.Code, w.Body)
		}
	}
	if done.Session.ID == first.ID || done.Session.ID != again.Session.ID || done.Session.Presentation.URL != next.Presentation.URL ||
		done.Queue.Current.Position != 1 || done.Session.ItemID != tracks[1] || th.engine.streams() != 1 {
		t.Fatalf("committed %+v then %s, %d streams", done.Session, again.Session.ID, th.engine.streams())
	}
	var old PlaybackSession
	call(t, th.s, "GET", "/v1/playback/sessions/"+first.ID, token, nil, &old)
	if old.State != "ended" {
		t.Fatalf("the song before is still %s", old.State)
	}
	// A prepared stream the engine replaces by itself (a hardware encoder that gave up) is
	// the one that gets committed.
	th.queue("POST", path+"/prepare-next", "", QueuePrepare{SessionID: done.Session.ID}, &next)
	th.engine.mu.Lock()
	for id, running := range th.engine.running {
		if id != done.Session.ID {
			running.Replaced(playback.Stream{Grant: "software", URL: "/v1/media/software/master.m3u8", Mode: "stream"})
		}
	}
	th.engine.mu.Unlock()
	var swapped QueueCommitted
	if w := th.queue("POST", path+"/commit-next", "", QueueCommit{Token: next.Token}, &swapped); w.Code != 200 ||
		swapped.Session.Presentation.URL != "/v1/media/software/master.m3u8" {
		t.Fatalf("committing a replaced prepared stream answered %d %+v", w.Code, swapped.Session.Presentation)
	}
	done = swapped
	// Prepared again, then the queue is edited: the prepared song is dropped.
	th.queue("POST", path+"/prepare-next", "", QueuePrepare{SessionID: done.Session.ID}, &next)
	th.queue("PATCH", path, "", QueueChange{Repeat: "all"}, nil)
	if w := th.queue("POST", path+"/commit-next", "", QueueCommit{Token: next.Token}, nil); w.Code != 410 || th.engine.streams() != 1 {
		t.Fatalf("committing a cancelled preparation answered %d with %d streams", w.Code, th.engine.streams())
	}
}
