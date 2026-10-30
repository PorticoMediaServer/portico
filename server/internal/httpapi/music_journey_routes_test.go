package httpapi

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// Stored vectors are declared fixture facts. This tests real mounted queue
// capture and playlist copy, without claiming inference or audio playback.
func TestMusicJourneyMountedFiniteCaptureSurvivesAnalysisChanges(t *testing.T) {
	f, items := mixesHTTPFixture(t)
	journey := map[string]any{"from": items[0].Public, "to": items[5].Public, "length": 6}
	body := map[string]any{"selector": map[string]any{"journey": journey}}
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "journey-finite-capture-0001"}, body, 201, &q)
	if q.Queue.Total != 6 || len(q.Queue.Segments) != 1 || q.Queue.Segments[0].State != "ready" {
		t.Fatal(q)
	}
	expected := []string{}
	for _, item := range items {
		expected = append(expected, item.Public)
	}
	f.catalogTest.Exec(`DELETE FROM music_sonic_embeddings WHERE entity_id IN(SELECT entity_id FROM catalog_songs)`)
	f.catalogTest.Exec(`INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES('mix-music',0,1)`)
	var page playbackv1.EntryPage
	f.call("GET", "/v1/queues/"+q.Queue.ID+"/entries?from=0&limit=200", nil, nil, 200, &page)
	got := []string{}
	for _, entry := range page.Items {
		got = append(got, entry.ItemID)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("re-analysis changed captured Journey", got, expected)
	}
	var saved playbackv1.SavedPlaylist
	f.call("POST", "/v1/queues/"+q.Queue.ID+":save-as-playlist", map[string]string{"Idempotency-Key": "journey-copy-playlist-0001"}, map[string]any{"name": "Captured songs"}, 201, &saved)
	rows, err := f.db.Query(`SELECT pid(item.public_id) FROM catalog_playlist_entries entry JOIN catalog_playlists playlist ON playlist.id=entry.playlist_id JOIN catalog_entities item ON item.id=entry.item_id WHERE playlist.token=? ORDER BY entry.order_key,entry.id`, saved.PlaylistID)
	if err != nil {
		t.Fatal(err)
	}
	actual := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		actual = append(actual, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal("playlist did not copy captured permitted keys", actual, expected, err)
	}
	w := f.raw("POST", "/v1/queues", f.owner.AccessToken, map[string]string{"Idempotency-Key": "journey-unavailable-0001"}, body)
	if w.Code < 400 || w.Code >= 500 || !strings.Contains(w.Body.String(), `"unsupported_selector"`) {
		t.Fatal("Off or unavailable endpoints fell back", w.Code, w.Body.String())
	}
}
