package writer

import (
	"reflect"
	"testing"
	"time"
)

// A refresh rewrites ONE journal with ONE cursor, so finishing streams (1/2)
// leaves the cursor at family 2 when chapters (3/4) start, and finishing the
// chapter restore leaves it at 3/4 when the stream restore starts. Each pair's
// walker must read the other pair's cursor as "this pair has not started".
// These tests drive both pairs across turns on a fake clock; the single-pair
// tests never cross a pair boundary.

func iaRefreshCrossDesiredStreams(codec string) iaRefreshStreamDesired {
	return iaRefreshStreamDesired{
		Fact: &iaRefreshStreamFactRow{AssetID: "refresh-asset", Revision: 11, Size: 42, ModifiedNS: 11, Fingerprint: "new", DetailVersion: 2},
		Rows: []iaRefreshStreamRow{iaRefreshStreamFixtureRow(1, codec), iaRefreshStreamFixtureRow(3, "av1")},
	}
}

func iaRefreshCrossDesiredChapters(title string) iaRefreshChapterDesired {
	return iaRefreshChapterDesired{
		Fact: &iaRefreshChapterFactRow{AssetID: "refresh-asset", Revision: 11, Size: 42, ModifiedNS: 11, Status: "ready", Fingerprint: "new"},
		Rows: []iaRefreshChapterRow{iaRefreshChapterFixtureRow(0, title), iaRefreshChapterFixtureRow(2, "Credits")},
	}
}

func TestIARefreshBothFamiliesWalkAndRollback(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	clock := w.now()
	w.now = func() time.Time { return clock }
	video := iaRefreshStreamBreadth(1, "h264")
	audio := iaRefreshStreamBreadth(2, "aac")
	opening := iaRefreshChapterFixtureRow(0, "Opening")
	middle := iaRefreshChapterFixtureRow(1, "Middle")
	iaRefreshSeed(t, w, iaRefreshStreamFact(10))
	iaRefreshSeed(t, w, iaRefreshChapterFact(10))
	for _, row := range []iaRefreshRecord{video, audio, opening, middle} {
		iaRefreshSeed(t, w, row)
	}
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	streams := iaRefreshCrossDesiredStreams("hevc")
	chapters := iaRefreshCrossDesiredChapters("Opening (new)")
	last := begin.result.revision

	forward := []iaRefreshKey{}
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawStreamStep(t, w, r, q, streams)
		if err != nil {
			t.Fatal("stream forward", err)
		}
		if !step.more {
			break
		}
		if step.revision != last {
			t.Fatalf("stream resume read revision %d want %d", step.revision, last)
		}
		forward = append(forward, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		out := iaRefreshApply(t, w, r, p, q)
		last = out.result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	// The chapter walk starts with the cursor still in the stream pair. Without
	// the cross-pair rule this first read refuses with ErrResolveRequired and the
	// whole refresh can never finish.
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawChapterStep(t, w, r, q, chapters)
		if err != nil {
			t.Fatal("chapter forward across the stream pair", err)
		}
		if !step.more {
			break
		}
		if step.revision != last {
			t.Fatalf("chapter resume read revision %d want %d", step.revision, last)
		}
		forward = append(forward, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		out := iaRefreshApply(t, w, r, p, q)
		last = out.result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	wantForward := []iaRefreshKey{
		{1, "refresh-asset", 0}, {2, "refresh-asset", 1}, {2, "refresh-asset", 2}, {2, "refresh-asset", 3},
		{3, "refresh-asset", 0}, {4, "refresh-asset", 0}, {4, "refresh-asset", 1}, {4, "refresh-asset", 2},
	}
	if !reflect.DeepEqual(forward, wantForward) {
		t.Fatalf("forward order %+v want %+v", forward, wantForward)
	}
	if row, ok := iaRefreshChapterImage(t, w, 2); !ok || row.Title != "Credits" {
		t.Fatal("new chapter row not written", row, ok)
	}
	if row, ok := iaRefreshStreamImage(t, w, 3); !ok || row.Codec != "av1" {
		t.Fatal("new stream row not written", row, ok)
	}

	restored := []iaRefreshKey{}
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawChapterRestore(t, w, r, q)
		if err != nil {
			t.Fatal("chapter restore", err)
		}
		if !step.more {
			break
		}
		if step.revision != last {
			t.Fatalf("chapter restore read revision %d want %d", step.revision, last)
		}
		restored = append(restored, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
		out := iaRefreshApply(t, w, r, p, q)
		last = out.result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	// The stream restore starts with the cursor in the chapter pair. Without the
	// cross-pair rule the stream walker refuses with ErrResolveRequired and the
	// refresh can never be rolled back.
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawStreamRestore(t, w, r, q)
		if err != nil {
			t.Fatal("stream restore across the chapter pair", err)
		}
		if !step.more {
			break
		}
		if step.revision != last {
			t.Fatalf("stream restore read revision %d want %d", step.revision, last)
		}
		restored = append(restored, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
		out := iaRefreshApply(t, w, r, p, q)
		last = out.result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	wantRestore := []iaRefreshKey{
		{3, "refresh-asset", 0}, {4, "refresh-asset", 2}, {4, "refresh-asset", 1}, {4, "refresh-asset", 0},
		{1, "refresh-asset", 0}, {2, "refresh-asset", 3}, {2, "refresh-asset", 2}, {2, "refresh-asset", 1},
	}
	if !reflect.DeepEqual(restored, wantRestore) {
		t.Fatalf("restore order %+v want %+v", restored, wantRestore)
	}
	for _, expected := range []iaRefreshStreamRow{video, audio} {
		if row, ok := iaRefreshStreamImage(t, w, expected.StreamIndex); !ok || !reflect.DeepEqual(row, expected) {
			t.Fatalf("exact old stream lost at index %d: %+v", expected.StreamIndex, row)
		}
	}
	if row, ok := iaRefreshStreamImage(t, w, 3); ok {
		t.Fatal("new stream row survived the rollback", row)
	}
	for _, expected := range []iaRefreshChapterRow{opening, middle} {
		if row, ok := iaRefreshChapterImage(t, w, expected.ChapterIndex); !ok || !reflect.DeepEqual(row, expected) {
			t.Fatalf("exact old chapter lost at index %d: %+v", expected.ChapterIndex, row)
		}
	}
	if row, ok := iaRefreshChapterImage(t, w, 2); ok {
		t.Fatal("new chapter row survived the rollback", row)
	}
	if got := iaRefreshCount(t, w, `SELECT revision FROM asset_stream_facts WHERE asset_id='refresh-asset'`); got != 10 {
		t.Fatal("stream parent fact was not restored", got)
	}
	if got := iaRefreshCount(t, w, `SELECT revision FROM asset_chapter_facts WHERE asset_id='refresh-asset'`); got != 10 {
		t.Fatal("chapter parent fact was not restored", got)
	}
	if got := iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`); got != 1 {
		t.Fatal("core cleared IA23's fence", got)
	}
}

func TestIARefreshBothFamiliesResumeSecondPair(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	clock := w.now()
	w.now = func() time.Time { return clock }
	iaRefreshSeed(t, w, iaRefreshStreamFact(10))
	iaRefreshSeed(t, w, iaRefreshChapterFact(10))
	iaRefreshSeed(t, w, iaRefreshChapterFixtureRow(0, "Opening"))
	iaRefreshSeed(t, w, iaRefreshChapterFixtureRow(1, "Middle"))
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	streams := iaRefreshCrossDesiredStreams("hevc")
	chapters := iaRefreshCrossDesiredChapters("Opening (new)")
	last := begin.result.revision
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawStreamStep(t, w, r, q, streams)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		last = iaRefreshApply(t, w, r, p, q).result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	// Two chapter Steps land the parent and its first child, then a NEW turn
	// reads again: the cursor is inside the chapter pair and must resume at
	// exactly the next key (the live-only child 1), not restart the pair.
	named := []iaRefreshKey{}
	for j := 0; j < 2; j++ {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawChapterStep(t, w, r, q, chapters)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			t.Fatalf("chapter walk closed after %d keys", j)
		}
		named = append(named, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		last = iaRefreshApply(t, w, r, p, q).result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	clock = clock.Add(time.Millisecond)
	resume, err := iaRefreshRawChapterStep(t, w, r, q, chapters)
	if err != nil || !resume.more {
		t.Fatal("chapter resume across turns", resume, err)
	}
	if resume.key != (iaRefreshKey{4, "refresh-asset", 1}) || resume.revision != last {
		t.Fatalf("chapter resume named %+v at revision %d want the live child 1 at %d", resume.key, resume.revision, last)
	}
	named = append(named, resume.key)
	q = iaRefreshStepRequest(t, q, IARefreshStep, resume)
	last = iaRefreshApply(t, w, r, p, q).result.revision
	iaRefreshAck(t, w, begin.result.refreshID)
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawChapterStep(t, w, r, q, chapters)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		named = append(named, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		last = iaRefreshApply(t, w, r, p, q).result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	want := []iaRefreshKey{{3, "refresh-asset", 0}, {4, "refresh-asset", 0}, {4, "refresh-asset", 1}, {4, "refresh-asset", 2}}
	if !reflect.DeepEqual(named, want) {
		t.Fatalf("chapter keys %+v want %+v", named, want)
	}
	for index := int64(0); index <= 2; index++ {
		row, ok := iaRefreshChapterImage(t, w, index)
		if index == 1 {
			if ok {
				t.Fatal("removed chapter survived", row)
			}
			continue
		}
		if !ok || row.Title != map[int64]string{0: "Opening (new)", 2: "Credits"}[index] {
			t.Fatalf("chapter %d not rewritten: %+v", index, row)
		}
	}
}

func TestIARefreshBothFamiliesResumeFirstPair(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	clock := w.now()
	w.now = func() time.Time { return clock }
	video := iaRefreshStreamBreadth(1, "h264")
	audio := iaRefreshStreamBreadth(2, "aac")
	iaRefreshSeed(t, w, iaRefreshStreamFact(10))
	for _, row := range []iaRefreshRecord{video, audio} {
		iaRefreshSeed(t, w, row)
	}
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	streams := iaRefreshCrossDesiredStreams("hevc")
	named := []iaRefreshKey{}
	last := begin.result.revision
	for j := 0; j < 2; j++ {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawStreamStep(t, w, r, q, streams)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			t.Fatalf("stream walk closed after %d keys", j)
		}
		named = append(named, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		last = iaRefreshApply(t, w, r, p, q).result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	clock = clock.Add(time.Millisecond)
	resume, err := iaRefreshRawStreamStep(t, w, r, q, streams)
	if err != nil || !resume.more {
		t.Fatal("stream resume across turns", resume, err)
	}
	if resume.key != (iaRefreshKey{2, "refresh-asset", 2}) || resume.revision != last {
		t.Fatalf("stream resume named %+v at revision %d want the live child 2 at %d", resume.key, resume.revision, last)
	}
	named = append(named, resume.key)
	q = iaRefreshStepRequest(t, q, IARefreshStep, resume)
	last = iaRefreshApply(t, w, r, p, q).result.revision
	iaRefreshAck(t, w, begin.result.refreshID)
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawStreamStep(t, w, r, q, streams)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		named = append(named, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		last = iaRefreshApply(t, w, r, p, q).result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	want := []iaRefreshKey{{1, "refresh-asset", 0}, {2, "refresh-asset", 1}, {2, "refresh-asset", 2}, {2, "refresh-asset", 3}}
	if !reflect.DeepEqual(named, want) {
		t.Fatalf("stream keys %+v want %+v", named, want)
	}
	if row, ok := iaRefreshStreamImage(t, w, 3); !ok || row.Codec != "av1" {
		t.Fatal("new stream row not written", row, ok)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM asset_streams WHERE asset_id='refresh-asset'`); got != 2 {
		t.Fatal("stream projection", got)
	}
}
