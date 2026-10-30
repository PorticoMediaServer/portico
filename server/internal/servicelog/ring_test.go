package servicelog

import (
	"fmt"
	"testing"
	"time"
)

// TestRingWrapsAndKeepsNewestRecords checks the circular buffer keeps the same
// newest-first view, and the same reset boundary, as the former shifting slice.
func TestRingWrapsAndKeepsNewestRecords(t *testing.T) {
	r := New(Options{Capacity: 3})
	defer r.Close()
	r.SetLevel("debug")
	for i := 1; i <= 8; i++ {
		r.Record("info", "server", fmt.Sprintf("m%d", i))
	}
	page, err := r.Read(Query{Level: "debug", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].Message != "m8" || page.Items[1].Message != "m7" || page.Items[2].Message != "m6" {
		t.Fatalf("newest first after wrap: %+v", page.Items)
	}
	if page.NextCursor != "" {
		t.Fatalf("a complete page must not offer a cursor: %q", page.NextCursor)
	}

	// The oldest retained sequence is 6. Resuming after 5 is contiguous and must
	// not reset; resuming after 4 has lost a record and must reset.
	after := int64(5)
	replay, reset, _, cancel := r.SubscribeAfter("debug", &after)
	cancel()
	if reset || len(replay) != 3 || replay[0].Message != "m6" || replay[2].Message != "m8" {
		t.Fatalf("contiguous replay after wrap: reset=%v %+v", reset, replay)
	}
	after = 4
	replay, reset, _, cancel = r.SubscribeAfter("debug", &after)
	cancel()
	if !reset || len(replay) != 3 || replay[0].Message != "m6" || replay[2].Message != "m8" {
		t.Fatalf("lost-record replay after wrap: reset=%v %+v", reset, replay)
	}
}

// TestReadPagingFilterAndCursorAcrossWrap pages a wrapped ring in both
// directions of filtering and checks that no record repeats or disappears.
func TestReadPagingFilterAndCursorAcrossWrap(t *testing.T) {
	r := New(Options{Capacity: 5})
	defer r.Close()
	r.SetLevel("debug")
	for i := 1; i <= 12; i++ {
		level, category := "info", "server"
		if i%3 == 0 {
			level = "warn"
		}
		if i%4 == 0 {
			category = "playback"
		}
		r.Record(level, category, fmt.Sprintf("m%d", i))
	}

	var got []int64
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		page, err := r.Read(Query{Level: "warn", Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			got = append(got, item.Sequence)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	// The ring holds sequences 8..12; warnings are 9 and 12.
	want := []int64{12, 9}
	if len(got) != len(want) {
		t.Fatalf("paged warn levels: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paged warn levels: got %v want %v", got, want)
		}
	}

	playback, err := r.Read(Query{Level: "debug", Category: "playback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(playback.Items) != 2 || playback.Items[0].Sequence != 12 || playback.Items[1].Sequence != 8 {
		t.Fatalf("category filter after wrap: %+v", playback.Items)
	}

	ahead, err := r.Read(Query{Level: "debug", Cursor: "99"})
	if err != nil || len(ahead.Items) != 5 || ahead.Items[0].Sequence != 12 {
		t.Fatalf("cursor above the newest record: %+v %v", ahead.Items, err)
	}
}

// TestPruneCompactsRingAndKeepsCursorOrder removes a record from the middle of a
// wrapped ring, then appends past the compaction point and checks ordering,
// cursor resets and the oldest-sequence boundary.
func TestPruneCompactsRingAndKeepsCursorOrder(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	r := New(Options{Capacity: 4, Now: func() time.Time { return now }})
	defer r.Close()
	r.SetLevel("debug")
	r.Record("info", "scan", "old-a")    // 1, expires
	r.Record("info", "server", "keep-1") // 2
	r.Record("info", "scan", "old-b")    // 3, expires
	r.Record("info", "server", "keep-2") // 4
	now = now.Add(47 * time.Hour)
	r.Record("info", "scan", "keep-scan") // 5, wraps over 1 and survives
	now = now.Add(time.Hour)
	r.SetRetention(map[string]int{"scan": 1})

	page, err := r.Read(Query{Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	// Sequence 3 was pruned from the middle; 2, 4 and 5 remain.
	want := []int64{5, 4, 2}
	if len(page.Items) != len(want) {
		t.Fatalf("after prune: %+v", page.Items)
	}
	for i, sequence := range want {
		if page.Items[i].Sequence != sequence {
			t.Fatalf("after prune order: %+v want %v", page.Items, want)
		}
	}

	after := int64(1)
	replay, reset, _, cancel := r.SubscribeAfter("debug", &after)
	cancel()
	if reset || len(replay) != 3 || replay[0].Sequence != 2 || replay[2].Sequence != 5 {
		t.Fatalf("replay after middle prune: reset=%v %+v", reset, replay)
	}

	r.Record("info", "server", "after-1") // 6
	r.Record("info", "server", "after-2") // 7, overwrites the oldest
	page, err = r.Read(Query{Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	// Appending after compaction keeps the buffer chronological and wraps again.
	want = []int64{7, 6, 5, 4}
	if len(page.Items) != len(want) {
		t.Fatalf("after appends: %+v", page.Items)
	}
	for i, sequence := range want {
		if page.Items[i].Sequence != sequence {
			t.Fatalf("after appends order: %+v want %v", page.Items, want)
		}
	}
	after = 2
	_, reset, _, cancel = r.SubscribeAfter("debug", &after)
	cancel()
	if !reset {
		t.Fatal("a cursor at the discarded sequence must reset")
	}
}

// TestLoadHistoryRebuildsBoundedRing checks startup restores the same bounded
// newest tail, that paging and replay work on it, and that appending continues
// to wrap with the next sequence ID.
func TestLoadHistoryRebuildsBoundedRing(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	first := New(Options{Capacity: 20, Directory: directory, FileBytes: 1 << 20, FileCount: 4, Now: clock})
	first.SetLevel("debug")
	for i := 1; i <= 12; i++ {
		first.Record("info", "server", fmt.Sprintf("m%d", i))
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := New(Options{Capacity: 5, Directory: directory, FileBytes: 1 << 20, FileCount: 4, Now: clock})
	defer second.Close()
	second.SetLevel("debug")
	page, err := second.Read(Query{Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	// History is renumbered 1..12 for this run; the ring keeps 8..12.
	if len(page.Items) != 5 || page.Items[0].Sequence != 12 || page.Items[4].Sequence != 8 {
		t.Fatalf("history tail: %+v", page.Items)
	}
	firstPage, err := second.Read(Query{Level: "debug", Limit: 2})
	if err != nil || len(firstPage.Items) != 2 || firstPage.Items[0].Message != "m12" || firstPage.Items[1].Message != "m11" || firstPage.NextCursor != "11" {
		t.Fatalf("history first page: %+v %v", firstPage, err)
	}
	lastPage, err := second.Read(Query{Level: "debug", Cursor: "9", Limit: 2})
	if err != nil || len(lastPage.Items) != 1 || lastPage.Items[0].Message != "m8" || lastPage.NextCursor != "" {
		t.Fatalf("history last page: %+v %v", lastPage, err)
	}

	second.Record("info", "server", "m13") // 13, wraps over 8
	page, err = second.Read(Query{Level: "debug"})
	if err != nil || len(page.Items) != 5 || page.Items[0].Message != "m13" || page.Items[4].Message != "m9" {
		t.Fatalf("append after history: %+v %v", page.Items, err)
	}
	after := int64(6)
	replay, reset, _, cancel := second.SubscribeAfter("debug", &after)
	cancel()
	if !reset || len(replay) != 5 || replay[0].Sequence != 9 {
		t.Fatalf("reset after history append: reset=%v %+v", reset, replay)
	}
}
