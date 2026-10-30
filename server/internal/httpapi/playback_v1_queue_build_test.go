package httpapi

import (
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// bigCollection is a collection of n movies in the owner's library, in the
// collection's canonical order (year, title).
func bigCollection(t *testing.T, f *v1Fixture, n int) []string {
	t.Helper()
	_, ids := createPlaybackCollection(t, f, n, "big", "Big")
	return ids
}

func waitBuilt(t *testing.T, f *v1Fixture, token, id string, within time.Duration) playbackv1.QueueView {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var q playbackv1.QueueView
		f.callAs(token, "GET", "/v1/queues/"+id, nil, nil, 200, &q)
		ready := len(q.Segments) > 0
		for _, g := range q.Segments {
			ready = ready && g.State == "ready"
		}
		if ready && q.Current != nil {
			return q
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue still building after %s: %+v", within, q)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func queueItemsInOrder(t *testing.T, f *v1Fixture, token, id string, total int64) []string {
	t.Helper()
	out := make([]string, 0, total)
	for from := int64(0); from < total; from += playbackv1.WindowMax {
		var page struct {
			Items []playbackv1.QueueEntry `json:"items"`
		}
		f.callAs(token, "GET", "/v1/queues/"+id+"/entries?from="+strconv.FormatInt(from, 10)+"&limit=200", nil, nil, 200, &page)
		for _, e := range page.Items {
			if !e.Available {
				t.Fatalf("entry %d unavailable after the build: %+v", e.Position, e)
			}
			out = append(out, e.ItemID)
		}
	}
	return out
}

func sameSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d entries, want %d", len(got), len(want))
	}
	a, b := append([]string{}, got...), append([]string{}, want...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("entry %q where %q was expected (sorted position %d)", a[i], b[i], i)
		}
	}
}

// P16 part two, ordered: a selection larger than a request snapshots answers
// at once, starts playing its first entry, and grows in the background until
// every key is there exactly once, in source order.
func TestPlaybackV1QueueLargeOrderedStartsAtOnceAndFinishes(t *testing.T) {
	testtier.Media(t, "a 20,000-item collection projected per test")
	f := newV1Fixture(t, 1)
	want := bigCollection(t, f, 20_000)
	f.v1.QueueScanBudget = 1024
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "big-ordered-000001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public)}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	if q.Session == nil || q.Session.ItemID != want[0] || q.Queue.Current == nil || q.Queue.Current.Position != 0 {
		t.Fatalf("first playback: session %+v current %+v", q.Session, q.Queue.Current)
	}
	done := waitBuilt(t, f, f.owner.AccessToken, q.Queue.ID, 30*time.Second)
	if done.Total != int64(len(want)) {
		t.Fatalf("total %d", done.Total)
	}
	got := queueItemsInOrder(t, f, f.owner.AccessToken, q.Queue.ID, done.Total)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: %s, want %s", i, got[i], want[i])
		}
	}
}

// P16 part two, shuffled: one fenced count gives the full size, the first
// entry's key is resolved ahead, and playback starts at once; the build then
// fills the rest. Every key appears exactly once, and the same seed gives the
// same order on another device.
func TestPlaybackV1QueueLargeShuffleStartsAtOnceWithAStableOrder(t *testing.T) {
	testtier.Media(t, "a 20,000-item collection projected per test")
	f := newV1Fixture(t, 1)
	want := bigCollection(t, f, 20_000)
	f.v1.QueueScanBudget = 1024
	body := map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public), "order": map[string]any{"mode": "shuffle", "seed": "11"}}}, "startPlayback": map[string]any{"state": "playing"}}
	var a playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "big-shuffle-000001"}, body, 201, &a)
	if a.Session == nil || a.Queue.Current == nil || a.Queue.Total != int64(len(want)) || a.Queue.Shuffle == nil {
		t.Fatalf("first playback: session %+v queue %+v", a.Session, a.Queue)
	}
	first := a.Session.ItemID
	doneA := waitBuilt(t, f, f.owner.AccessToken, a.Queue.ID, 30*time.Second)
	gotA := queueItemsInOrder(t, f, f.owner.AccessToken, a.Queue.ID, doneA.Total)
	sameSet(t, gotA, want)
	if gotA[0] != first {
		t.Fatalf("the first entry changed after the build: %s, then %s", first, gotA[0])
	}
	tv := f.device("living-room-tv")
	var b playbackv1.QueueReply
	f.callAs(tv.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "big-shuffle-000002"}, body, 201, &b)
	waitBuilt(t, f, tv.AccessToken, b.Queue.ID, 30*time.Second)
	gotB := queueItemsInOrder(t, f, tv.AccessToken, b.Queue.ID, doneA.Total)
	for i := range gotA {
		if gotA[i] != gotB[i] {
			t.Fatalf("the same seed differs between devices at %d: %s vs %s", i, gotA[i], gotB[i])
		}
	}
}

// P16 part two, fallback: when even the count doesn't fit in time, the start
// waits for the build (no current entry, no session), and the queue picks its
// first entry from the seed when the build finishes.
func TestPlaybackV1QueueLargeShuffleWaitsWhenTheCountRunsLong(t *testing.T) {
	testtier.Media(t, "a 20,000-item collection projected per test")
	f := newV1Fixture(t, 1)
	want := bigCollection(t, f, 20_000)
	f.v1.QueueScanBudget = 1024
	f.v1.QueueCountBudget = time.Nanosecond
	f.v1.HoldQueueBuilds()
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "big-waiting-000001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public), "order": map[string]any{"mode": "shuffle", "seed": "5"}}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	if q.Session != nil || q.Queue.Current != nil || q.Queue.Segments[0].State != "building" {
		t.Fatalf("a start that can't be decided yet: session %+v queue %+v", q.Session, q.Queue)
	}
	// While paused (as after a restart), nothing can play yet.
	if w := f.raw("POST", "/v1/queues/"+q.Queue.ID+":advance", f.owner.AccessToken, map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"reason": "next"}); w.Code != 503 || v1Code(w) != "queue_building" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("advance while building: %d %s", w.Code, w.Body.String())
	}
	f.v1.ResumeQueueBuilds()
	done := waitBuilt(t, f, f.owner.AccessToken, q.Queue.ID, 30*time.Second)
	if done.Shuffle == nil || done.Shuffle.Seed != "5" || done.Total != int64(len(want)) {
		t.Fatalf("after the build: %+v", done)
	}
	sameSet(t, queueItemsInOrder(t, f, f.owner.AccessToken, q.Queue.ID, done.Total), want)
}

// P16 part two: an item anchor past what the request snapshotted waits for
// the build to reach it; meanwhile the ordered segment holds what's built.
func TestPlaybackV1QueueDeepAnchorWaitsForTheBuild(t *testing.T) {
	testtier.Media(t, "a 20,000-item collection projected per test")
	f := newV1Fixture(t, 1)
	want := bigCollection(t, f, 20_000)
	f.v1.QueueScanBudget = 1024
	f.v1.HoldQueueBuilds()
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "big-anchor-0000001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public), "anchor": map[string]any{"itemId": want[15_000]}}}}, 201, &q)
	if q.Queue.Current != nil || q.Queue.Total != 1024 || q.Queue.Segments[0].State != "building" {
		t.Fatalf("before the anchor is built: current %+v total %d", q.Queue.Current, q.Queue.Total)
	}
	f.v1.ResumeQueueBuilds()
	done := waitBuilt(t, f, f.owner.AccessToken, q.Queue.ID, 30*time.Second)
	if done.Current == nil || done.Current.Position != 15_000 {
		t.Fatalf("current after the build: %+v", done.Current)
	}
}

// PORTICO_QUEUE_SCALE=1: a synthetic 1M-key library, measured. Time to the
// first playback (ordered and shuffled), time to the finished build, every key
// exactly once, and the same order on two devices.
func TestPlaybackV1QueueOneMillionKeys(t *testing.T) {
	if os.Getenv("PORTICO_QUEUE_SCALE") == "" {
		t.Skip("set PORTICO_QUEUE_SCALE=1 to run the 1M-key measurement")
	}
	n := 1_000_000
	if v, err := strconv.Atoi(os.Getenv("PORTICO_QUEUE_SCALE")); err == nil && v > 1 {
		n = v // a smaller run while investigating
	}
	f := newV1Fixture(t, 1)
	started := time.Now()
	want := bigCollection(t, f, n)
	t.Logf("fixture: %d items in %s", n, time.Since(started).Round(time.Millisecond))
	for _, mode := range []string{"source", "shuffle"} {
		body := map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public), "order": map[string]any{"mode": mode, "seed": "42"}}}, "startPlayback": map[string]any{"state": "playing"}}
		started = time.Now()
		var q playbackv1.QueueReply
		f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "million-" + mode + "-00001"}, body, 201, &q)
		first := time.Since(started)
		if q.Session == nil {
			t.Fatalf("%s: no first playback: %+v", mode, q.Queue)
		}
		done := waitBuilt(t, f, f.owner.AccessToken, q.Queue.ID, 10*time.Minute)
		built := time.Since(started)
		got := queueItemsInOrder(t, f, f.owner.AccessToken, q.Queue.ID, done.Total)
		sameSet(t, got, want)
		t.Logf("%-7s first playback %s, built %s, %d keys, each once", mode, first.Round(time.Millisecond), built.Round(time.Millisecond), len(got))
		if mode == "shuffle" {
			tv := f.device("tv-" + mode)
			var b playbackv1.QueueReply
			started = time.Now()
			f.callAs(tv.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "million-" + mode + "-00002"}, body, 201, &b)
			t.Logf("%-7s first playback on a second device (kept size) %s", mode, time.Since(started).Round(time.Millisecond))
			waitBuilt(t, f, tv.AccessToken, b.Queue.ID, 10*time.Minute)
			other := queueItemsInOrder(t, f, tv.AccessToken, b.Queue.ID, done.Total)
			for i := range got {
				if got[i] != other[i] {
					t.Fatalf("order differs between devices at %d", i)
				}
			}
		}
	}
}

// P16 part two: a shuffled queue knows its full size at once; while its build
// is held (as after a restart) the entries not built yet are pending
// placeholders, and an advance onto one is a retryable 503.
func TestPlaybackV1QueueShufflePendingEntriesWhileBuilding(t *testing.T) {
	testtier.Media(t, "a 20,000-item collection projected per test")
	f := newV1Fixture(t, 1)
	want := bigCollection(t, f, 20_000)
	f.v1.QueueScanBudget = 1024
	f.v1.HoldQueueBuilds()
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "big-pending-000001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public), "order": map[string]any{"mode": "shuffle", "seed": "3"}}}}, 201, &q)
	if q.Queue.Total != int64(len(want)) || q.Queue.Current == nil || q.Queue.Segments[0].State != "building" {
		t.Fatalf("header while building: %+v", q.Queue)
	}
	var page struct {
		Items []playbackv1.QueueEntry `json:"items"`
	}
	f.call("GET", "/v1/queues/"+q.Queue.ID+"/entries?from=0&limit=200", nil, nil, 200, &page)
	pending, playable := 0, 0
	for _, e := range page.Items {
		switch {
		case e.Kind == "pending" && !e.Available && e.ItemID == "":
			pending++
		case e.Available:
			playable++
		}
	}
	if pending == 0 || playable == 0 || !page.Items[0].Available {
		t.Fatalf("window while building: %d pending, %d playable, first %+v", pending, playable, page.Items[0])
	}
	f.v1.ResumeQueueBuilds()
	done := waitBuilt(t, f, f.owner.AccessToken, q.Queue.ID, 30*time.Second)
	sameSet(t, queueItemsInOrder(t, f, f.owner.AccessToken, q.Queue.ID, done.Total), want)
}

// P16: a large shuffle's size is kept per catalog revision for callers the
// item fence can't narrow, so the next such shuffle starts without counting.
// The first entry is the one the counting pass picks for that seed, so a
// device using the kept size plays the same order; a catalog change makes the
// kept size stale and the pass runs again.
func TestPlaybackV1ShuffleKeepsTheContainerSize(t *testing.T) {
	testtier.Media(t, "a 20,000-item collection projected per test")
	f := newV1Fixture(t, 1)
	want := bigCollection(t, f, 20_000)
	f.v1.QueueScanBudget = 1024
	body := map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public), "order": map[string]any{"mode": "shuffle", "seed": "7"}}}, "startPlayback": map[string]any{"state": "playing"}}
	shuffle := func(token, key string) playbackv1.QueueReply {
		t.Helper()
		var q playbackv1.QueueReply
		f.callAs(token, "POST", "/v1/queues", map[string]string{"Idempotency-Key": key}, body, 201, &q)
		if q.Session == nil {
			t.Fatalf("no first playback: %+v", q.Queue)
		}
		return q
	}
	kept := func() int64 {
		var n int64
		if err := f.db.QueryRow(`SELECT keys FROM queue_v1_container_counts WHERE selector LIKE 'collection:%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	first := shuffle(f.owner.AccessToken, "kept-size-000000001")
	if first.Queue.Total != 20_000 || kept() != 20_000 {
		t.Fatalf("counted %d, kept %d", first.Queue.Total, kept())
	}
	// The next shuffle reads the kept size (shown by a tampered one), not the keys.
	if _, err := f.db.Exec(`UPDATE queue_v1_container_counts SET keys=19999`); err != nil {
		t.Fatal(err)
	}
	if q := shuffle(f.owner.AccessToken, "kept-size-000000002"); q.Queue.Total != 19_999 {
		t.Fatalf("the kept size wasn't used: total %d", q.Queue.Total)
	}
	if _, err := f.db.Exec(`UPDATE queue_v1_container_counts SET keys=20000`); err != nil {
		t.Fatal(err)
	}
	// Another device, from the kept size: the same first entry and the same order.
	tv := f.device("tv-kept-size")
	other := shuffle(tv.AccessToken, "kept-size-000000003")
	if other.Session.ItemID != first.Session.ItemID {
		t.Fatalf("first entry %s from the kept size, %s from the count", other.Session.ItemID, first.Session.ItemID)
	}
	// This device's queue replaced its earlier ones: count again (nothing kept) to compare.
	if _, err := f.db.Exec(`DELETE FROM queue_v1_container_counts`); err != nil {
		t.Fatal(err)
	}
	counted := shuffle(f.owner.AccessToken, "kept-size-000000005")
	if counted.Session.ItemID != other.Session.ItemID {
		t.Fatalf("first entry %s counted, %s from the kept size", counted.Session.ItemID, other.Session.ItemID)
	}
	a := queueItemsInOrder(t, f, f.owner.AccessToken, counted.Queue.ID, waitBuilt(t, f, f.owner.AccessToken, counted.Queue.ID, time.Minute).Total)
	b := queueItemsInOrder(t, f, tv.AccessToken, other.Queue.ID, waitBuilt(t, f, tv.AccessToken, other.Queue.ID, time.Minute).Total)
	if len(a) != 20_000 || len(b) != 20_000 {
		t.Fatalf("built %d and %d entries", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("order differs at %d", i)
		}
	}
	// An anchored shuffle from the kept size starts on its anchor, far past the first scan.
	var anchored playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "kept-size-000000006"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public), "order": map[string]any{"mode": "shuffle", "seed": "7"}, "anchor": map[string]any{"itemId": want[19_000]}}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &anchored)
	if anchored.Session == nil || anchored.Session.ItemID != want[19_000] || anchored.Queue.Total != 20_000 {
		t.Fatalf("anchored shuffle %+v", anchored.Queue)
	}
	// A catalog change: the kept size is stale, so the shuffle counts again.
	addPlaybackCollectionMovie(t, f, "big-extra", "Big extra", f.names["big"])
	if q := shuffle(f.owner.AccessToken, "kept-size-000000004"); q.Queue.Total != 20_001 || kept() != 20_001 {
		t.Fatalf("after a catalog change: total %d, kept %d", q.Queue.Total, kept())
	}
}
