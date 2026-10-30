package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func queueItems(ids ...string) map[string]any {
	return map[string]any{"items": map[string]any{"ids": ids}}
}

// Slice 3: Play is one request (queue + first session), windows ≤ 200,
// If-Match on commands, advance starts the next session and replaces the old
// one, repeat, play next, remove and move (spec §8, §17.8–10).
func TestPlaybackV1QueuePlayAdvanceAndEdit(t *testing.T) {
	f := newV1Fixture(t, 3)
	key := map[string]string{"Idempotency-Key": "queue-key-000000001"}
	body := map[string]any{"owner": "device", "segments": []any{map[string]any{"source": queueItems(f.items...), "anchor": map[string]any{"itemId": f.items[1]}}}, "startPlayback": map[string]any{"state": "playing"}}
	var created playbackv1.QueueReply
	w := f.call("POST", "/v1/queues", key, body, 201, &created)
	q := created.Queue
	if q.Total != 3 || q.Current == nil || q.Current.Position != 1 || len(created.Window) != 3 || created.Session == nil {
		t.Fatalf("created %+v", created)
	}
	if created.Session.ItemID != f.items[1] || created.Session.Queue == nil || created.Session.Queue.QueueID != q.ID || created.Session.Queue.EntryID != q.Current.EntryID {
		t.Fatalf("first session %+v", created.Session)
	}
	if w.Header().Get("ETag") != `"`+q.Revision+`"` || created.Window[1].Title == "" || !created.Window[1].Available {
		t.Fatalf("etag %q window %+v", w.Header().Get("ETag"), created.Window)
	}
	// A replay is the same answer; the key with another body is 422.
	var replay playbackv1.QueueReply
	f.call("POST", "/v1/queues", key, body, 201, &replay)
	if replay.Queue.ID != q.ID || replay.Session == nil || replay.Session.ID != created.Session.ID {
		t.Fatalf("replay %+v", replay)
	}
	other := map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items[0])}}}
	if w = f.raw("POST", "/v1/queues", f.owner.AccessToken, key, other); w.Code != 422 || v1Code(w) != "idempotency_key_reused" {
		t.Fatalf("reused key: %d %s", w.Code, w.Body.String())
	}

	base := "/v1/queues/" + q.ID
	// Commands need If-Match; a stale one is 412 with the current header.
	if w = f.raw("POST", base+":advance", f.owner.AccessToken, nil, map[string]any{"reason": "next"}); w.Code != 428 {
		t.Fatalf("no If-Match: %d", w.Code)
	}
	w = f.raw("POST", base+":advance", f.owner.AccessToken, map[string]string{"If-Match": `"99"`}, map[string]any{"reason": "next"})
	var stale struct {
		Error struct {
			Code    string               `json:"code"`
			Current playbackv1.QueueView `json:"current"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &stale)
	if w.Code != 412 || stale.Error.Current.ID != q.ID || stale.Error.Current.Revision != q.Revision {
		t.Fatalf("stale advance %d %s", w.Code, w.Body.String())
	}
	// Advance starts the next entry's session and ends the previous one.
	var next playbackv1.QueueReply
	f.call("POST", base+":advance", map[string]string{"If-Match": q.Revision}, map[string]any{"reason": "next"}, 200, &next)
	if next.Queue.Current.Position != 2 || next.Session == nil || next.Session.ItemID != f.items[2] {
		t.Fatalf("advance %+v", next)
	}
	var old playbackv1.SessionView
	f.call("GET", "/v1/playback/sessions/"+created.Session.ID, nil, nil, 200, &old)
	if old.State != "ended" {
		t.Fatalf("previous session %s", old.State)
	}
	if w = f.raw("POST", base+":advance", f.owner.AccessToken, map[string]string{"If-Match": next.Queue.Revision}, map[string]any{"reason": "next"}); w.Code != 409 || v1Code(w) != "queue_ended" {
		t.Fatalf("past the end: %d %s", w.Code, w.Body.String())
	}
	var repeated playbackv1.QueueReply
	f.call("PATCH", base, map[string]string{"If-Match": next.Queue.Revision}, map[string]any{"repeat": "all"}, 200, &repeated)
	var wrapped playbackv1.QueueReply
	f.call("POST", base+":advance", map[string]string{"If-Match": repeated.Queue.Revision}, map[string]any{"reason": "completion"}, 200, &wrapped)
	if wrapped.Queue.Current.Position != 0 || wrapped.Session == nil || wrapped.Session.ItemID != f.items[0] {
		t.Fatalf("repeat all %+v", wrapped)
	}

	// Play next goes right after the current entry; move and remove keep ids stable.
	var added playbackv1.QueueReply
	f.call("POST", base+"/segments", map[string]string{"If-Match": wrapped.Queue.Revision}, map[string]any{"placement": "next", "source": queueItems(f.items[2])}, 200, &added)
	var page playbackv1.EntryPage
	f.call("GET", base+"/entries?from=0&limit=10", nil, nil, 200, &page)
	if added.Queue.Total != 4 || len(page.Items) != 4 || page.Items[1].ItemID != f.items[2] || page.Page.Total != 4 || page.Items[0].EntryID != wrapped.Queue.Current.EntryID {
		t.Fatalf("play next %+v", page.Items)
	}
	var moved playbackv1.QueueReply
	f.call("POST", base+"/entries/"+page.Items[3].EntryID+":move", map[string]string{"If-Match": added.Queue.Revision}, map[string]any{"before": page.Items[1].EntryID}, 200, &moved)
	f.call("GET", base+"/entries?from=0&limit=10", nil, nil, 200, &page)
	order := []string{}
	for _, e := range page.Items {
		order = append(order, e.ItemID)
	}
	if fmt.Sprint(order) != fmt.Sprint([]string{f.items[0], f.items[2], f.items[2], f.items[1]}) {
		t.Fatalf("after move %v", order)
	}
	var removed playbackv1.QueueReply
	f.call("DELETE", base+"/entries/"+page.Items[2].EntryID, map[string]string{"If-Match": moved.Queue.Revision}, nil, 200, &removed)
	if removed.Queue.Total != 3 || removed.Queue.Current.EntryID != wrapped.Queue.Current.EntryID {
		t.Fatalf("after remove %+v", removed.Queue)
	}
	if w = f.raw("DELETE", base+"/entries/"+page.Items[2].EntryID, f.owner.AccessToken, map[string]string{"If-Match": removed.Queue.Revision}, nil); w.Code != 404 {
		t.Fatalf("removed twice: %d", w.Code)
	}
	var window playbackv1.EntryPage
	f.call("GET", base+"/window?around=current&before=5&after=5", nil, nil, 200, &window)
	if len(window.Items) != 3 || window.Page.Start != 0 {
		t.Fatalf("window %+v", window)
	}
	// Play on this device replaces its queue; the old one is gone.
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "queue-key-000000002"}, other, 201, nil)
	if w = f.raw("GET", base, f.owner.AccessToken, nil, nil); w.Code != 404 {
		t.Fatalf("replaced queue still readable: %d", w.Code)
	}
}

// A 20,000-entry shuffled queue: windows never exceed 200; the order is stable
// for a seed on every device and is a permutation (each entry once); reading a
// window costs the same at any size (invariants 7 and 8).
func TestPlaybackV1QueueShuffleAtScale(t *testing.T) {
	testtier.Media(t, "a 20,000-item collection projected per test")
	f := newV1Fixture(t, 1)
	const n = 20_000
	collection, _ := createPlaybackCollection(t, f, n, "bulk", "Bulk")
	create := func(token, keyText string) playbackv1.QueueReply {
		var out playbackv1.QueueReply
		f.callAs(token, "POST", "/v1/queues", map[string]string{"Idempotency-Key": keyText}, map[string]any{"segments": []any{map[string]any{"source": map[string]any{"container": map[string]any{"kind": "collection", "id": collection.Public}}, "order": map[string]any{"mode": "shuffle", "seed": "42"}}}}, 201, &out)
		return out
	}
	started := time.Now()
	a := create(f.owner.AccessToken, "shuffle-key-0000001")
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("creating a 20k queue took %s", elapsed)
	}
	tv := f.device("living-room-tv")
	b := create(tv.AccessToken, "shuffle-key-0000002")
	if a.Queue.Total != n || a.Queue.Shuffle == nil || a.Queue.Shuffle.Seed != "42" || len(a.Window) > playbackv1.WindowMax {
		t.Fatalf("header %+v window %d", a.Queue, len(a.Window))
	}
	seen := make(map[string]bool, n)
	for from := 0; from < n; from += 200 {
		var pa, pb playbackv1.EntryPage
		f.call("GET", fmt.Sprintf("/v1/queues/%s/entries?from=%d&limit=200", a.Queue.ID, from), nil, nil, 200, &pa)
		f.callAs(tv.AccessToken, "GET", fmt.Sprintf("/v1/queues/%s/entries?from=%d&limit=200", b.Queue.ID, from), nil, nil, 200, &pb)
		if len(pa.Items) != 200 || len(pb.Items) != 200 {
			t.Fatalf("page at %d: %d/%d", from, len(pa.Items), len(pb.Items))
		}
		for i := range pa.Items {
			if pa.Items[i].ItemID != pb.Items[i].ItemID {
				t.Fatalf("same seed, different order at %d", from+i)
			}
			if seen[pa.Items[i].ItemID] {
				t.Fatalf("%s appears twice", pa.Items[i].ItemID)
			}
			seen[pa.Items[i].ItemID] = true
		}
	}
	if len(seen) != n {
		t.Fatalf("%d distinct entries, want %d", len(seen), n)
	}
	if w := f.raw("GET", "/v1/queues/"+a.Queue.ID+"/entries?from=0&limit=201", f.owner.AccessToken, nil, nil); w.Code != 400 {
		t.Fatalf("a 201-entry page: %d", w.Code)
	}
	// Shuffle off continues in source order from the current entry.
	var off playbackv1.QueueReply
	f.call("PATCH", "/v1/queues/"+a.Queue.ID, map[string]string{"If-Match": a.Queue.Revision}, map[string]any{"shuffle": map[string]any{"on": false}}, 200, &off)
	if off.Queue.Shuffle != nil || off.Queue.Current == nil || off.Queue.Current.EntryID != a.Queue.Current.EntryID {
		t.Fatalf("shuffle off %+v", off.Queue)
	}
	started = time.Now()
	for i := 0; i < 20; i++ {
		f.call("GET", "/v1/queues/"+a.Queue.ID+"/window?before=100&after=99", nil, nil, 200, nil)
	}
	if per := time.Since(started) / 20; per > 250*time.Millisecond {
		t.Fatalf("a 200-entry window took %s", per)
	}
}

// Queues are authorized like the catalogue: a container in a library the member
// can't see is 404; an explicit item they can't see is left out, exactly like an
// id that doesn't exist.
func TestPlaybackV1QueueAuthorization(t *testing.T) {
	f := newV1Fixture(t, 1)
	member, film := f.member()
	collection := f.catalogTest.Collection(f.libraryHandle, "Owner films", f.records[0])
	f.catalogTest.Drain()
	if w := f.raw("POST", "/v1/queues", member, map[string]string{"Idempotency-Key": "member-queue-000001"}, map[string]any{"segments": []any{map[string]any{"source": map[string]any{"container": map[string]any{"kind": "collection", "id": collection.Public}}}}}); w.Code != 404 {
		t.Fatalf("unshared collection: %d %s", w.Code, w.Body.String())
	}
	var mixed playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "member-queue-000002"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(film, f.items[0])}}}, 201, &mixed)
	// The owner's title is absent from the member's queue, not a placeholder (P11).
	if len(mixed.Window) != 1 || !mixed.Window[0].Available || mixed.Window[0].ItemID != film || mixed.Queue.Total != 1 {
		t.Fatalf("queue %+v window %+v", mixed.Queue, mixed.Window)
	}
	// The owner can't read the member's queue.
	if w := f.raw("GET", "/v1/queues/"+mixed.Queue.ID, f.owner.AccessToken, nil, nil); w.Code != 404 {
		t.Fatalf("owner read a member's queue: %d", w.Code)
	}
	// A supported browse query still refuses an inaccessible library.
	if w := f.raw("POST", "/v1/queues", member, map[string]string{"Idempotency-Key": "member-queue-000003"}, map[string]any{"segments": []any{map[string]any{"source": querySource(f.library, "asc")}}}); w.Code != 404 || v1Code(w) != "not_found" {
		t.Fatalf("query selector: %d %s", w.Code, w.Body.String())
	}
}

// The window carries the artwork an Up next card shows: the poster the item
// art route serves (only when one exists) and an episode's own still.
func TestPlaybackV1QueueWindowArtwork(t *testing.T) {
	f := newV1Fixture(t, 3)
	digest := fmt.Sprintf("%064x", 7)
	if _, err := f.db.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?,'image/jpeg',800,1200,10,'2000-01-01T00:00:00Z','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, digest); err != nil {
		t.Fatal(err)
	}
	for i, v := range []struct{ item, role string }{{f.items[1], "poster"}, {f.items[2], "still"}} {
		var entity int64
		if err := f.db.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, v.item).Scan(&entity); err != nil {
			t.Fatal(err)
		}
		candidate := fmt.Sprintf("candidate-%d", i)
		if _, err := f.db.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,'item',?,?,'owner',?,'upload','','fence','2000-01-01')`, candidate, entity, v.role, candidate); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,candidate_id,digest,actor,observed_at) SELECT column1,column2,column3,column4,column5,column7,column8 FROM (VALUES('item',?,?,?,?,?,'owner','2000-01-01'))`, entity, v.role, candidate, digest, digest); err != nil {
			t.Fatal(err)
		}
	}
	body := map[string]any{"owner": "device", "segments": []any{map[string]any{"source": queueItems(f.items...)}}}
	var created playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "queue-art-0000000001"}, body, 201, &created)
	if len(created.Window) != 3 {
		t.Fatalf("window %+v", created.Window)
	}
	if w := created.Window[0]; w.PosterURL != "" || w.StillURL != "" {
		t.Fatalf("no artwork: %+v", w)
	}
	described := "v=" + digest + "&sd=800x1200.10." + digest[:16] + "&ld=800x1200.10." + digest[:16]
	if w := created.Window[1]; w.PosterURL != "/v1/items/"+f.items[1]+"/art/poster?"+described || w.StillURL != "" {
		t.Fatalf("poster: %+v", w)
	}
	if w := created.Window[2]; w.PosterURL != "" || w.StillURL != "/v1/metadata/item/"+f.items[2]+"/art/still?"+described {
		t.Fatalf("still: %+v", w)
	}
}
