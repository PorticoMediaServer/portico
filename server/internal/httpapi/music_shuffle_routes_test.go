package httpapi

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// Numeric embeddings are declared fixture facts. Actual HTTP, authorization,
// queue writers and captured-row replay are exercised, not audio inference.
func TestMusicShuffleMountedCapturesOrderAndKeepsItAfterSonicOff(t *testing.T) {
	f, items := mixesHTTPFixture(t)
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.Public)
	}
	create := func(key string, retained ...string) playbackv1.QueueReply {
		var q playbackv1.QueueReply
		body := map[string]any{
			"selector": map[string]any{"items": map[string]any{"ids": ids}},
			"shuffle":  map[string]any{"seed": "42"},
		}
		if len(retained) > 0 {
			body["retainQueueIds"] = retained
		}
		f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": key}, body, 201, &q)
		return q
	}
	read := func(id string) []playbackv1.QueueEntry {
		var page playbackv1.EntryPage
		f.call("GET", "/v1/queues/"+id+"/entries?from=0&limit=200", nil, nil, 200, &page)
		return page.Items
	}
	q := create("music-shuffle-capture-001")
	var model, version, digest string
	var count int
	var ordinals []byte
	if err := f.db.QueryRow(`SELECT model_id,model_version,model_sha256,count,ordinals FROM queue_v1_sonic_windows WHERE queue_id=? AND window_index=0`, q.Queue.ID).Scan(&model, &version, &digest, &count, &ordinals); err != nil {
		t.Fatal(err)
	}
	if model != sonic.ModelID || version != sonic.ModelVersion || digest != sonic.ModelSHA256 || count != len(ids) || len(ordinals) != len(ids)*8 {
		t.Fatal("queue did not capture the actual current model", model, version, digest, count, len(ordinals))
	}
	first := read(q.Queue.ID)
	if len(first) != len(ids) {
		t.Fatal(first)
	}
	seen := map[string]bool{}
	for position, entry := range first {
		source := binary.LittleEndian.Uint64(ordinals[position*8:])
		if source >= uint64(len(ids)) || entry.ItemID != ids[source] || seen[entry.ItemID] {
			t.Fatal("wire order differs from the immutable seeded source set", position, source, entry)
		}
		seen[entry.ItemID] = true
	}
	// A manual move cuts the captured base order into spans. The occurrence
	// moved and its untouched neighbours must retain the exact heard identity.
	var moved playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+"/entries/"+first[5].EntryID+":move", map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"before": first[1].EntryID}, 200, &moved)
	wantMoved := append([]playbackv1.QueueEntry{first[0], first[5]}, first[1:5]...)
	first = read(q.Queue.ID)
	for index, entry := range first {
		if entry.EntryID != wantMoved[index].EntryID || entry.ItemID != wantMoved[index].ItemID {
			t.Fatal("move changed another captured occurrence", index, entry, wantMoved[index])
		}
	}
	f.catalogTest.Exec(`INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES('mix-music',0,1)`)
	if after := read(q.Queue.ID); !reflect.DeepEqual(first, after) {
		t.Fatal("Sonic Off rewrote a heard/captured window", first, after)
	}
	qOff := create("music-shuffle-off-new-001", q.Queue.ID)
	if err := f.db.QueryRow(`SELECT model_id FROM queue_v1_sonic_windows WHERE queue_id=? AND window_index=0`, qOff.Queue.ID).Scan(&model); err != nil || model != "" {
		t.Fatal("a new Off window claimed Sonic ordering", model, err)
	}
	off := read(qOff.Queue.ID)
	f.catalogTest.Exec(`UPDATE library_sonic_settings SET enabled=1,revision=revision+1 WHERE library_id='mix-music'`)
	if after := read(q.Queue.ID); !reflect.DeepEqual(first, after) {
		t.Fatal("turning On rewrote a stored smoothed window", first, after)
	}
	if after := read(qOff.Queue.ID); !reflect.DeepEqual(off, after) {
		t.Fatal("turning On rewrote an already captured ordinary window", off, after)
	}
	var restored playbackv1.QueueReply
	f.call("PATCH", "/v1/queues/"+qOff.Queue.ID, map[string]string{"If-Match": qOff.Queue.Revision}, map[string]any{"shuffle": map[string]any{"on": false}}, 200, &restored)
	for index, entry := range read(qOff.Queue.ID) {
		if entry.ItemID != ids[index] {
			t.Fatal("shuffle Off lost the original source order", index, entry)
		}
	}

}

func TestMusicShuffleMountedReadCapturesAnUnvisitedWindowBeforeReturningIt(t *testing.T) {
	f, items := mixesHTTPFixture(t)
	ids := make([]string, 276)
	for index := range ids {
		ids[index] = items[index%len(items)].Public
	}
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "music-shuffle-two-windows-001"}, map[string]any{
		"segments": []any{
			map[string]any{"source": map[string]any{"items": map[string]any{"ids": ids[:256]}}, "order": map[string]any{"mode": "shuffle", "seed": "42"}},
			map[string]any{"source": map[string]any{"items": map[string]any{"ids": ids[256:]}}},
		},
	}, 201, &q)
	if q.Queue.Total != int64(len(ids)) || q.Queue.Shuffle == nil {
		t.Fatal(q)
	}
	var before int
	if err := f.db.QueryRow(`SELECT count(*) FROM queue_v1_sonic_windows WHERE queue_id=? AND window_index=1`, q.Queue.ID).Scan(&before); err != nil || before != 0 {
		t.Fatal("unvisited window was captured eagerly", before, err)
	}
	var page playbackv1.EntryPage
	f.call("GET", "/v1/queues/"+q.Queue.ID+"/entries?from=256&limit=200", nil, nil, 200, &page)
	var model string
	var count int
	var ordinals []byte
	if err := f.db.QueryRow(`SELECT model_id,count,ordinals FROM queue_v1_sonic_windows WHERE queue_id=? AND window_index=1`, q.Queue.ID).Scan(&model, &count, &ordinals); err != nil {
		t.Fatal(err)
	}
	if model != sonic.ModelID || count != 20 || len(ordinals) != 160 || len(page.Items) != 20 {
		t.Fatal("read returned an uncommitted or incomplete window", model, count, len(ordinals), len(page.Items))
	}
	for position, entry := range page.Items {
		source := binary.LittleEndian.Uint64(ordinals[position*8:])
		if source >= uint64(len(ids)) || entry.Position != int64(256+position) || entry.ItemID != ids[source] {
			t.Fatal("unvisited window response did not use the committed capture", position, source, entry)
		}
	}
}
