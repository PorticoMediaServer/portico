package httpapi

import (
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"testing"
)

func TestShuffledQueueEditsKeepPlayedOrder(t *testing.T) {
	f := newV1Fixture(t, 6)
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "shuffle-edit-key-0001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items...)}}}, 201, &q)
	base := "/v1/queues/" + q.Queue.ID
	call := func(method, path string, body any) {
		t.Helper()
		f.call(method, path, map[string]string{"If-Match": q.Queue.Revision}, body, 200, &q)
	}
	call("PATCH", base, map[string]any{"shuffle": map[string]any{"on": true, "seed": "42"}})
	call("POST", base+":advance", map[string]any{"reason": "next"})
	var before playbackv1.EntryPage
	f.call("GET", base+"/entries?from=0&limit=20", nil, nil, 200, &before)
	current := q.Queue.Current.EntryID
	call("POST", base+"/segments", map[string]any{"placement": "next", "source": queueItems(f.items[5])})
	var after playbackv1.EntryPage
	f.call("GET", base+"/entries?from=0&limit=20", nil, nil, 200, &after)
	if len(after.Items) != 7 || after.Items[2].ItemID != f.items[5] || q.Queue.Current.EntryID != current || q.Queue.Current.Position != 1 {
		t.Fatalf("next %+v %+v", q.Queue, after.Items)
	}
	for i := 3; i < len(after.Items); i++ {
		if after.Items[i].EntryID != before.Items[i-1].EntryID {
			t.Fatalf("order changed at %d", i)
		}
	}
	removed := after.Items[4].EntryID
	call("DELETE", base+"/entries/"+removed, nil)
	var fewer playbackv1.EntryPage
	f.call("GET", base+"/entries?from=0&limit=20", nil, nil, 200, &fewer)
	if len(fewer.Items) != 6 || q.Queue.Current.Position != 1 || fewer.Items[0].EntryID != before.Items[0].EntryID {
		t.Fatalf("remove %+v", fewer.Items)
	}
	moved := fewer.Items[5].EntryID
	call("POST", base+"/entries/"+moved+":move", map[string]any{"before": fewer.Items[2].EntryID})
	f.call("GET", base+"/entries?from=0&limit=20", nil, nil, 200, &after)
	if after.Items[2].EntryID != moved || q.Queue.Current.Position != 1 {
		t.Fatalf("move %+v", after.Items)
	}
	call("PATCH", base, map[string]any{"shuffle": map[string]any{"on": false}})
	if q.Queue.Total != 6 || q.Queue.Current.EntryID != current {
		t.Fatalf("unshuffle %+v", q.Queue)
	}
}

func TestShuffledQueueCanRemoveItsFinalEntry(t *testing.T) {
	f := newV1Fixture(t, 1)
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "shuffle-remove-last-0001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items...)}}}, 201, &q)
	base := "/v1/queues/" + q.Queue.ID
	f.call("PATCH", base, map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"shuffle": map[string]any{"on": true, "seed": "42"}}, 200, &q)
	var removed playbackv1.QueueReply
	f.call("DELETE", base+"/entries/"+q.Queue.Current.EntryID, map[string]string{"If-Match": q.Queue.Revision}, nil, 200, &removed)
	q = removed
	if q.Queue.Total != 0 || q.Queue.Current != nil {
		t.Fatalf("removed entry survived: %+v", q.Queue)
	}
}

// A playlist can hold the same title more than once. Playing it from one of
// those copies starts at that copy, not the first: the anchor names the
// playlist entry. An entry removed since starts at its title instead.
func TestPlaylistPlaysFromThePressedOccurrence(t *testing.T) {
	f := newV1Fixture(t, 3)
	a, b, c := f.items[0], f.items[1], f.items[2]
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "occurrence-queue-0001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(a, b, a, c)}}}, 201, &q)
	var saved playbackv1.SavedPlaylist
	f.call("POST", "/v1/queues/"+q.Queue.ID+":save-as-playlist", map[string]string{"Idempotency-Key": "occurrence-save-00001", "If-Match": q.Queue.Revision}, map[string]any{"name": "Twice"}, 201, &saved)
	rows, err := f.db.Query(`SELECT pe.token FROM catalog_playlist_entries pe JOIN catalog_playlists p ON p.id=pe.playlist_id WHERE p.token=? ORDER BY pe.order_key,pe.id`, saved.PlaylistID)
	if err != nil {
		t.Fatal(err)
	}
	var entries []string
	for rows.Next() {
		var token string
		_ = rows.Scan(&token)
		entries = append(entries, token)
	}
	rows.Close()
	if len(entries) != 4 {
		t.Fatalf("playlist entries %v", entries)
	}
	playlist := map[string]any{"container": map[string]any{"kind": "playlist", "id": saved.PlaylistID}}
	start := func(key string, anchor map[string]any) playbackv1.QueueReply {
		t.Helper()
		var out playbackv1.QueueReply
		f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": key}, map[string]any{"segments": []any{map[string]any{"source": playlist, "anchor": anchor}}}, 201, &out)
		return out
	}
	second := start("occurrence-play-00001", map[string]any{"entryId": entries[2], "itemId": a})
	if second.Queue.Current == nil || second.Queue.Current.Position != 2 || second.Queue.Total != 4 {
		t.Fatalf("the second copy: %+v", second.Queue.Current)
	}
	first := start("occurrence-play-00002", map[string]any{"itemId": a})
	if first.Queue.Current == nil || first.Queue.Current.Position != 0 {
		t.Fatalf("an item anchor: %+v", first.Queue.Current)
	}
	if _, err = f.db.Exec(`DELETE FROM catalog_playlist_entries WHERE token=?`, entries[2]); err != nil {
		t.Fatal(err)
	}
	gone := start("occurrence-play-00003", map[string]any{"entryId": entries[2], "itemId": a})
	if gone.Queue.Current == nil || gone.Queue.Current.Position != 0 || gone.Queue.Total != 3 {
		t.Fatalf("a removed copy starts at its title: %+v", gone.Queue.Current)
	}
	last := start("occurrence-play-00004", map[string]any{"entryId": entries[3]})
	if last.Queue.Current == nil || last.Queue.Current.Position != 2 {
		t.Fatalf("an entry anchor alone: %+v", last.Queue.Current)
	}
	if w := f.raw("POST", "/v1/queues", f.owner.AccessToken, map[string]string{"Idempotency-Key": "occurrence-play-00005"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(a, b), "anchor": map[string]any{"entryId": entries[0]}}}}); w.Code != 422 && w.Code != 400 {
		t.Fatalf("an entry anchor outside a playlist: %d", w.Code)
	}
}
