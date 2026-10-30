package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// reviewRequestBudget bounds one request in the save-as-playlist review tests.
// It sits above the default lane's 5s budget, so a healthy machine still gets
// the route's own deadline first, and below the tests' 20s poll windows and far
// below the package timeout, so a request that blocks reports a status instead
// of hanging the package. The deadline is carried by the request context the
// handler (and the catalogue write it makes) reads, so the blocked operation
// itself ends; no goroutine is left waiting for a reply.
const reviewRequestBudget = 10 * time.Second

func callWithReviewDeadline(t *testing.T, f *v1Fixture, token, method, path string, headers map[string]string, body any, want int, out any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), reviewRequestBudget)
	defer cancel()
	r := httptest.NewRequest(method, path, bytes.NewReader(data)).WithContext(ctx)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		contentType := "application/json"
		if method == "PATCH" {
			contentType = "application/merge-patch+json"
		}
		r.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if want != 0 && w.Code != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	if out != nil && w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, w.Body.String())
		}
	}
	return w
}

// Review P21: an advance moves only onto an entry the viewer can play. A
// restriction that arrives after the queue was built is skipped by next; a
// named entry the viewer can't play is not found, and the queue doesn't move.
func TestPlaybackV1AdvanceSkipsEntriesTheViewerCantPlay(t *testing.T) {
	f, member, ids := queueAccessFixture(t)
	var kids int64
	if err := f.db.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, f.catalogTest.ID(ids["film"])).Scan(&kids); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pg-1", "pg-2", "pg-3"} {
		item := newPlaybackMovieEntity(f, kids, id, id, 2000)
		f.names[id] = item
		ids[id] = item.Public
		f.catalogTest.Attributes(item.ID, "contentRating", "PG")
	}
	f.catalogTest.Drain()
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "advance-skip-000001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(ids["pg-1"], ids["pg-2"], ids["pg-3"])}}}, 201, &q)
	middle := q.Window[1].EntryID
	// pg-2 becomes R after the queue was built.
	f.catalogTest.Attributes(f.catalogTest.ID(ids["pg-2"]), "contentRating", "R")
	f.catalogTest.Drain()
	base := "/v1/queues/" + q.Queue.ID
	if w := f.raw("POST", base+":advance", member, map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"reason": "entry", "entryId": middle}); w.Code != 404 {
		t.Fatalf("advance onto a withheld entry: %d %s", w.Code, w.Body.String())
	}
	var still playbackv1.QueueView
	f.callAs(member, "GET", base, nil, nil, 200, &still)
	if still.Current == nil || still.Current.Position != 0 || still.Revision != q.Queue.Revision {
		t.Fatalf("the queue moved: %+v", still.Current)
	}
	var next playbackv1.QueueReply
	f.callAs(member, "POST", base+":advance", map[string]string{"If-Match": still.Revision}, map[string]any{"reason": "next"}, 200, &next)
	if next.Queue.Current == nil || next.Queue.Current.Position != 2 {
		t.Fatalf("next didn't skip the withheld entry: %+v", next.Queue.Current)
	}
}

// Review P20: deleting a queue that doesn't exist and one that isn't yours
// get the same answer, and only your own is deleted.
func TestPlaybackV1DeleteQueueIsNoExistenceOracle(t *testing.T) {
	f, member, _ := queueAccessFixture(t)
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "delete-oracle-00001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items[0])}}}, 201, &q)
	missing := f.raw("DELETE", "/v1/queues/q_nosuchqueue000000000", member, nil, nil)
	foreign := f.raw("DELETE", "/v1/queues/"+q.Queue.ID, member, nil, nil)
	if missing.Code != 204 || foreign.Code != 204 {
		t.Fatalf("missing %d, another profile's %d", missing.Code, foreign.Code)
	}
	f.call("GET", "/v1/queues/"+q.Queue.ID, nil, nil, 200, nil)
}

// Review P18: a create's key is answered by replay. A 128-character key still
// starts its playback; a late replay after a newer Play gets its original
// answer and leaves the newer queue alone; a create that committed without
// keeping its answer is finished by the replay (the same session); two
// concurrent requests with one key make one queue.
func TestPlaybackV1QueueCreateIdempotency(t *testing.T) {
	f := newV1Fixture(t, 2)
	body := func(item string) map[string]any {
		return map[string]any{"segments": []any{map[string]any{"source": queueItems(item)}}, "startPlayback": map[string]any{"state": "playing"}}
	}
	long := strings.Repeat("k", 128)
	var a playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": long}, body(f.items[0]), 201, &a)
	if a.Session == nil {
		t.Fatalf("a 128-character key started no playback: %+v", a.Queue)
	}
	var b playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "idem-newer-play-001"}, body(f.items[1]), 201, &b)
	var late playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": long}, body(f.items[0]), 201, &late)
	if late.Queue.ID != a.Queue.ID {
		t.Fatalf("a late replay made queue %s (first %s)", late.Queue.ID, a.Queue.ID)
	}
	f.call("GET", "/v1/queues/"+b.Queue.ID, nil, nil, 200, nil)
	// Committed, answer not kept: the replay finishes it with the same session.
	if _, err := f.db.Exec(`UPDATE queue_v1_create_receipts SET response='' WHERE create_key='idem-newer-play-001'`); err != nil {
		t.Fatal(err)
	}
	var again playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "idem-newer-play-001"}, body(f.items[1]), 201, &again)
	if again.Queue.ID != b.Queue.ID || again.Session == nil || b.Session == nil || again.Session.ID != b.Session.ID {
		t.Fatalf("finished replay %+v, first %+v", again.Session, b.Session)
	}
	if w := f.raw("POST", "/v1/queues", f.owner.AccessToken, map[string]string{"Idempotency-Key": "idem-newer-play-001"}, body(f.items[0])); w.Code != 422 {
		t.Fatalf("another body under a used key: %d", w.Code)
	}
	var wg sync.WaitGroup
	got := make([]playbackv1.QueueReply, 4)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "idem-concurrent-001"}, body(f.items[0]), 201, &got[i])
		}()
	}
	wg.Wait()
	for _, r := range got[1:] {
		if r.Queue.ID != got[0].Queue.ID {
			t.Fatalf("concurrent creates made %s and %s", got[0].Queue.ID, r.Queue.ID)
		}
	}
}

// Review P19: queues are removed by the sweep, off the command's write: the
// queue a Play replaced (its keys a batch at a time), a queue whose device is
// gone, and one idle for 90 days.
func TestPlaybackV1QueuesAreSwept(t *testing.T) {
	f := newV1Fixture(t, 1)
	body := map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items[0])}}}
	var first, second playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "sweep-replaced-0001"}, body, 201, &first)
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "sweep-replaced-0002"}, body, 201, &second)
	tv := f.device("tv-sweep")
	var gone playbackv1.QueueReply
	f.callAs(tv.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "sweep-orphaned-0001"}, body, 201, &gone)
	if _, err := f.db.Exec(`UPDATE queues_v1 SET updated_ms=0 WHERE id=?`, second.Queue.ID); err != nil {
		t.Fatal(err)
	}
	// The device is forgotten: what refers to it goes first, then its record.
	var device string
	if err := f.db.QueryRow(`SELECT owner_id FROM queues_v1 WHERE id=?`, gone.Queue.ID).Scan(&device); err != nil {
		t.Fatal(err)
	}
	refs, err := f.db.Query(`SELECT m.name,k."from" FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) k WHERE m.type='table' AND k."table"='identity_devices'`)
	if err != nil {
		t.Fatal(err)
	}
	var deletes []string
	for refs.Next() {
		var table, column string
		if err = refs.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		deletes = append(deletes, `DELETE FROM `+table+` WHERE `+column+`=?`)
	}
	refs.Close()
	for _, q := range append(deletes, `DELETE FROM identity_devices WHERE id=?`) {
		if _, err := f.db.Exec(q, device); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	f.v1.SweepQueues(context.Background())
	var left int
	if err := f.db.QueryRow(`SELECT count(*) FROM queues_v1 WHERE id IN(?,?,?)`, first.Queue.ID, second.Queue.ID, gone.Queue.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d of the replaced, idle and orphaned queues left: %v", left, err)
	}
}

// Spec §8 (INT P21, P32): a queue is saved, in its play order, as a new
// playlist of the caller's, through the catalog's playlist create (positions
// from 1, its receipt kept); an entry they can no longer play is left out; a
// retry answers with the same playlist; a queue longer than a playlist can
// hold is refused whole.
func TestPlaybackV1SaveQueueAsPlaylist(t *testing.T) {
	f, member, ids := queueAccessFixture(t)
	var kids int64
	if err := f.db.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, f.catalogTest.ID(ids["film"])).Scan(&kids); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"save-1", "save-2", "save-3"} {
		item := newPlaybackMovieEntity(f, kids, id, id, 2000)
		f.names[id] = item
		ids[id] = item.Public
		f.catalogTest.Attributes(item.ID, "contentRating", "PG")
	}
	f.catalogTest.Drain()
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "save-queue-000000001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(ids["save-3"], ids["save-1"], ids["save-2"])}}}, 201, &q)
	f.catalogTest.Attributes(f.catalogTest.ID(ids["save-1"]), "contentRating", "R")
	f.catalogTest.Drain()
	path := "/v1/queues/" + q.Queue.ID + ":save-as-playlist"
	var saved playbackv1.SavedPlaylist
	f.callAs(member, "POST", path, map[string]string{"Idempotency-Key": "save-playlist-00001", "If-Match": q.Queue.Revision}, map[string]any{"name": "Evening"}, 201, &saved)
	if saved.Entries != 2 || saved.PlaylistID == "" {
		t.Fatalf("saved %+v", saved)
	}
	rows, err := f.db.Query(`SELECT COALESCE(pid(e.public_id),'')||'@'||pe.position FROM catalog_playlist_entries pe JOIN catalog_playlists p ON p.id=pe.playlist_id LEFT JOIN catalog_entities e ON e.id=pe.item_id WHERE p.token=? ORDER BY pe.position`, saved.PlaylistID)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for rows.Next() {
		var item string
		_ = rows.Scan(&item)
		got = append(got, item)
	}
	rows.Close()
	if strings.Join(got, ",") != ids["save-3"]+"@1,"+ids["save-2"]+"@2" {
		t.Fatalf("entries %v", got)
	}
	var receipts int
	if err = f.db.QueryRow(`SELECT count(*) FROM playlist_receipts WHERE operation_id='save-playlist-00001'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("the catalog's receipt: %d %v", receipts, err)
	}
	var again playbackv1.SavedPlaylist
	f.callAs(member, "POST", path, map[string]string{"Idempotency-Key": "save-playlist-00001"}, map[string]any{"name": "Evening"}, 201, &again)
	if again.PlaylistID != saved.PlaylistID {
		t.Fatalf("a retry made playlist %s (first %s)", again.PlaylistID, saved.PlaylistID)
	}
	if w := f.raw("POST", path, member, map[string]string{"Idempotency-Key": "save-playlist-00001"}, map[string]any{"name": "Morning"}); w.Code != 422 {
		t.Fatalf("another body under the key: %d", w.Code)
	}
	if saved.State != playbackv1.SaveStateSaved || again.State != playbackv1.SaveStateSaved {
		t.Fatalf("a short save is saved at once: %q %q", saved.State, again.State)
	}
}

// NEW-37: a queue longer than one playlist write is saved whole, in play
// order: the request creates the playlist and answers "saving"; the builder
// copies a window per write; a replay of the key reports progress until
// "saved". Nothing is refused for size.
func TestPlaybackV1SaveAsPlaylistCopiesALongQueueWhole(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, _, _ := queueAccessFixture(t)
	bigCollection(t, f, 1001)
	drainPlaybackCatalogue(t, f.db)
	var big playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "save-queue-big-00001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public)}}}, 201, &big)
	path := "/v1/queues/" + big.Queue.ID + ":save-as-playlist"
	headers := map[string]string{"Idempotency-Key": "save-playlist-big01"}
	var saved playbackv1.SavedPlaylist
	f.call("POST", path, headers, map[string]any{"name": "Everything"}, 201, &saved)
	if saved.PlaylistID == "" || saved.Total != 1001 || saved.State == playbackv1.SaveStateFailed {
		t.Fatalf("large save %+v", saved)
	}
	deadline := time.Now().Add(20 * time.Second)
	for saved.State != playbackv1.SaveStateSaved {
		if time.Now().After(deadline) {
			t.Fatalf("the copy never finished: %+v", saved)
		}
		time.Sleep(50 * time.Millisecond)
		var again playbackv1.SavedPlaylist
		f.call("POST", path, headers, map[string]any{"name": "Everything"}, 201, &again)
		if again.PlaylistID != saved.PlaylistID || again.State == playbackv1.SaveStateFailed || again.Entries < saved.Entries {
			t.Fatalf("progress went wrong: %+v after %+v", again, saved)
		}
		saved = again
	}
	if saved.Entries != 1001 {
		t.Fatalf("saved %d of 1001", saved.Entries)
	}
	// The playlist holds the queue's own order, one entry per queue entry.
	var queueOrder, playlistOrder []string
	for from := 0; from < 1001; from += 200 {
		var page playbackv1.EntryPage
		f.call("GET", "/v1/queues/"+big.Queue.ID+"/entries?from="+strconv.Itoa(from)+"&limit=200", nil, nil, 200, &page)
		for _, e := range page.Items {
			queueOrder = append(queueOrder, e.ItemID)
		}
	}
	rows, err := f.db.Query(`SELECT pid(e.public_id) FROM catalog_playlist_entries pe JOIN catalog_playlists p ON p.id=pe.playlist_id JOIN catalog_entities e ON e.id=pe.item_id WHERE p.token=? ORDER BY pe.order_key,pe.id`, saved.PlaylistID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		playlistOrder = append(playlistOrder, id)
	}
	rows.Close()
	if len(queueOrder) != 1001 || strings.Join(playlistOrder, ",") != strings.Join(queueOrder, ",") {
		t.Fatalf("playlist order differs from the queue: %d vs %d entries", len(playlistOrder), len(queueOrder))
	}
	// Another name under the key is still a reuse.
	if w := f.raw("POST", path, f.owner.AccessToken, headers, map[string]any{"name": "Something else"}); w.Code != 422 {
		t.Fatalf("another body under the key: %d %s", w.Code, w.Body.String())
	}
}

// A queue changed while its copy runs stops the copy with queue_changed; the
// entries copied so far stay in the playlist, and nothing is read from the
// new order.
func TestPlaybackV1SaveAsPlaylistStopsWhenTheQueueChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, _, _ := queueAccessFixture(t)
	bigCollection(t, f, 1001)
	var big playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "save-queue-chg-00001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public)}}}, 201, &big)
	svc := f.v1
	svc.HoldQueueBuilds()
	path := "/v1/queues/" + big.Queue.ID + ":save-as-playlist"
	headers := map[string]string{"Idempotency-Key": "save-playlist-chg01"}
	var saved playbackv1.SavedPlaylist
	f.call("POST", path, headers, map[string]any{"name": "Changing"}, 201, &saved)
	if saved.State != playbackv1.SaveStateSaving || saved.Entries != 0 {
		t.Fatalf("paused save %+v", saved)
	}
	var changed playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+big.Queue.ID+"/segments", map[string]string{"If-Match": big.Queue.Revision}, map[string]any{"placement": "end", "source": queueItems(f.names["big0000000"].Public)}, 200, &changed)
	svc.ResumeQueueBuilds()
	deadline := time.Now().Add(20 * time.Second)
	for saved.State == playbackv1.SaveStateSaving {
		if time.Now().After(deadline) {
			t.Fatalf("the copy never stopped: %+v", saved)
		}
		time.Sleep(50 * time.Millisecond)
		f.call("POST", path, headers, map[string]any{"name": "Changing"}, 201, &saved)
	}
	if saved.State != playbackv1.SaveStateFailed || saved.ErrorCode != "queue_changed" || saved.Entries != 0 {
		t.Fatalf("a changed queue: %+v", saved)
	}
}

// Playing or setting the queue while it is copied moves its revision but not
// its order: the copy carries on and saves everything.
func TestPlaybackV1SaveAsPlaylistSurvivesARevisionThatKeepsTheOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, _, _ := queueAccessFixture(t)
	bigCollection(t, f, 1001)
	var big playbackv1.QueueReply
	callWithReviewDeadline(t, f, f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "save-queue-rep-00001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public)}}}, 201, &big)
	f.v1.HoldQueueBuilds()
	path := "/v1/queues/" + big.Queue.ID + ":save-as-playlist"
	headers := map[string]string{"Idempotency-Key": "save-playlist-rep01"}
	var saved playbackv1.SavedPlaylist
	callWithReviewDeadline(t, f, f.owner.AccessToken, "POST", path, headers, map[string]any{"name": "Repeating"}, 201, &saved)
	var repeated playbackv1.QueueReply
	callWithReviewDeadline(t, f, f.owner.AccessToken, "PATCH", "/v1/queues/"+big.Queue.ID, map[string]string{"If-Match": big.Queue.Revision}, map[string]any{"repeat": "all"}, 200, &repeated)
	if repeated.Queue.Revision == big.Queue.Revision {
		t.Fatal("the setting didn't move the revision; the test proves nothing")
	}
	f.v1.ResumeQueueBuilds()
	deadline := time.Now().Add(20 * time.Second)
	for saved.State == playbackv1.SaveStateSaving {
		if time.Now().After(deadline) {
			t.Fatalf("the copy never finished: %+v", saved)
		}
		time.Sleep(50 * time.Millisecond)
		callWithReviewDeadline(t, f, f.owner.AccessToken, "POST", path, headers, map[string]any{"name": "Repeating"}, 201, &saved)
	}
	if saved.State != playbackv1.SaveStateSaved || saved.Entries != 1001 {
		t.Fatalf("a revision without a new order: %+v", saved)
	}
}

// A change that lands after the copy read its windows but before the first
// append write is still a different play order: the write transaction must
// refuse it, leaving what was already copied and never marking the save saved.
func TestPlaybackV1SaveAsPlaylistStopsWhenTheQueueChangesAfterTheRead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, _, _ := queueAccessFixture(t)
	svc := f.v1
	reached, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	svc.SaveReadCheck = func() { once.Do(func() { close(reached); <-proceed }) }
	bigCollection(t, f, 1001)
	var big playbackv1.QueueReply
	callWithReviewDeadline(t, f, f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "save-after-read-01"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public)}}}, 201, &big)
	svc.HoldQueueBuilds()
	path := "/v1/queues/" + big.Queue.ID + ":save-as-playlist"
	headers := map[string]string{"Idempotency-Key": "save-after-read-save"}
	var saved playbackv1.SavedPlaylist
	callWithReviewDeadline(t, f, f.owner.AccessToken, "POST", path, headers, map[string]any{"name": "After the read"}, 201, &saved)
	if saved.State != playbackv1.SaveStateSaving || saved.Entries != 0 {
		t.Fatalf("paused save %+v", saved)
	}
	svc.ResumeQueueBuilds()
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		t.Fatalf("the copy never read its windows: %+v", saved)
	}
	var changed playbackv1.QueueReply
	callWithReviewDeadline(t, f, f.owner.AccessToken, "POST", "/v1/queues/"+big.Queue.ID+"/segments", map[string]string{"If-Match": big.Queue.Revision}, map[string]any{"placement": "end", "source": queueItems(f.names["big0000000"].Public)}, 200, &changed)
	close(proceed)
	deadline := time.Now().Add(20 * time.Second)
	for saved.State == playbackv1.SaveStateSaving {
		if time.Now().After(deadline) {
			t.Fatalf("the copy never stopped: %+v", saved)
		}
		time.Sleep(50 * time.Millisecond)
		callWithReviewDeadline(t, f, f.owner.AccessToken, "POST", path, headers, map[string]any{"name": "After the read"}, 201, &saved)
	}
	if saved.State != playbackv1.SaveStateFailed || saved.ErrorCode != "queue_changed" || saved.Entries != 0 {
		t.Fatalf("a queue changed after the read: %+v", saved)
	}
}
