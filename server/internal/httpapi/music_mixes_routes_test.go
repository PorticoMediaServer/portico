package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// These are declared stored numeric/source facts, not model inference or audio
// decoding. Actual HTTP authority, normal queue capture and SQL copy are real.
func mixesHTTPFixture(t *testing.T) (*v1Fixture, []catalogtest.Item) {
	t.Helper()
	f := newV1Fixture(t, 0)
	c := f.catalogTest
	root := filepath.Join(f.root, "mix-music")
	library := c.Library("mix-music", "Music", "music", root)
	album := c.Album(c.Artist(library, "Actual artist"), "Actual album", 2026)
	items := []catalogtest.Item{}
	for n := 0; n < 6; n++ {
		item := c.Song(album, n+1, fmt.Sprintf("%s/mix-%d.flac", root, n), fmt.Sprintf("Mix song %d", n))
		c.SonicEmbedding(item, float32(6-n), float32(n+1))
		items = append(items, item)
	}
	c.Drain()
	at := time.Now().Add(-time.Hour).UnixMilli()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n := 0; n < 12; n++ {
			item := items[n%len(items)]
			if _, err := tx.ExecContext(ctx, `INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms,completed) VALUES(?,?,?,?, 'Fixture',?,'mix-music',7,?,?,?,2000,1)`, fmt.Sprintf("mix-play-%d", n), f.owner.Viewer.Authority, f.owner.Viewer.AccountID, f.owner.Viewer.ProfileID, item.ID, item.Public, at, at); err != nil {
				return err
			}
		}
		return nil
	})
	f.handler = New(f.deps)
	return f, items
}

func TestMusicMixesMountedCurrentProfileFiniteCaptureAndExactPlaylistCopy(t *testing.T) {
	f, items := mixesHTTPFixture(t)
	var first, again projectedViewerEnvelope[MusicMixesView]
	w := f.call("GET", "/v1/music/mixes", nil, nil, 200, &first)
	if w.Header().Get("Cache-Control") != "no-store" || len(first.Data.Mixes) != 1 || first.Scope.ProfileID != f.owner.Viewer.ProfileID {
		t.Fatal(first, w.Header())
	}
	for _, forbidden := range []string{`"tracks"`, `"keys"`, `"historyDigest"`, `"vector"`} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatal("row exposed private selection facts", forbidden)
		}
	}
	f.call("GET", "/v1/music/mixes", nil, nil, 200, &again)
	if !reflect.DeepEqual(first, again) {
		t.Fatal("unchanged refresh changed mixes", first, again)
	}
	mix := first.Data.Mixes[0]
	if mix.NameSource.Kind != "artist" || mix.NameSource.ArtistID != mix.Artists[0].ID || len(mix.Covers) != 3 || mix.SongCount != 6 {
		t.Fatal(mix)
	}
	var q playbackv1.QueueReply
	selector := map[string]any{"selector": map[string]any{"container": map[string]any{"kind": "mix", "id": mix.ID}}}
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "mix-finite-capture-0001"}, selector, 201, &q)
	if q.Queue.Total != 6 || len(q.Queue.Segments) != 1 || q.Queue.Segments[0].State != "ready" {
		t.Fatal(q)
	}
	var page playbackv1.EntryPage
	f.call("GET", "/v1/queues/"+q.Queue.ID+"/entries?from=0&limit=200", nil, nil, 200, &page)
	expected := []string{}
	for _, entry := range page.Items {
		expected = append(expected, entry.ItemID)
	}
	var saved catalog.PlaylistReceipt
	body := map[string]any{"operationId": "copy-mix-playlist-0001", "name": "Actual artist mix"}
	path := "/v1/music/mixes/" + mix.ID + "/playlist"
	f.call("POST", path, nil, body, 200, &saved)
	actual := []string{}
	rows, err := f.db.Query(`SELECT pid(item.public_id) FROM catalog_playlist_entries entry JOIN catalog_playlists playlist ON playlist.id=entry.playlist_id JOIN catalog_entities item ON item.id=entry.item_id WHERE playlist.token=? ORDER BY entry.order_key,entry.id`, saved.PlaylistID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			break
		}
		actual = append(actual, key)
	}
	rows.Close()
	if err != nil || len(expected) != 6 || !reflect.DeepEqual(expected, actual) {
		t.Fatal("copy changed ordinary captured keys", actual, expected, err)
	}
	member, _ := f.member()
	f.callAs(member, "POST", path, nil, map[string]any{"operationId": "cross-profile-copy-0001", "name": "Actual artist mix"}, 404, nil)
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "cross-profile-play-0001"}, selector, 404, nil)
	f.call("GET", "/v1/music/mixes?profileId=another", nil, nil, 400, nil)
	f.catalogTest.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms,completed) VALUES('mix-new-play',?,?,?,'Fixture',?,'mix-music',7,?,?,?,2000,1)`, f.owner.Viewer.Authority, f.owner.Viewer.AccountID, f.owner.Viewer.ProfileID, items[0].ID, items[0].Public, time.Now().Add(-time.Minute).UnixMilli(), time.Now().Add(-time.Minute).UnixMilli())
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "stale-mix-play-0001"}, selector, 404, nil)
	var replay catalog.PlaylistReceipt
	f.call("POST", path, nil, body, 200, &replay)
	if replay.PlaylistID != saved.PlaylistID {
		t.Fatal("stale recommendation lost durable ordinary copy", replay, saved)
	}
	f.call("GET", "/v1/music/mixes", nil, nil, 200, &again)
	if len(again.Data.Mixes) != 1 || again.Data.Mixes[0].ID == mix.ID {
		t.Fatal("new play did not refresh content-addressed identity", again)
	}
}

// Called by the exhaustive write matrix: a real previously offered mix loses
// its permitted library before capture. It must disclose/mutate no more than
// an identifier that never existed; no route exemption is involved.
func musicMixForbiddenCopyCheck(t *testing.T) {
	f, _ := mixesHTTPFixture(t)
	var row projectedViewerEnvelope[MusicMixesView]
	f.call("GET", "/v1/music/mixes", nil, nil, 200, &row)
	if len(row.Data.Mixes) != 1 {
		t.Fatal(row)
	}
	id := row.Data.Mixes[0].ID
	f.catalogTest.Exec(`UPDATE direct_profiles SET allowed_libraries='[]' WHERE id=?`, f.owner.Viewer.ProfileID)
	request := map[string]any{"operationId": "forbidden-mix-copy-0001", "name": "Actual artist mix"}
	forbidden := f.raw("POST", "/v1/music/mixes/"+id+"/playlist", f.owner.AccessToken, nil, request)
	absent := f.raw("POST", "/v1/music/mixes/mix-"+strings.Repeat("x", 43)+"/playlist", f.owner.AccessToken, nil, request)
	if forbidden.Code != 404 || absent.Code != forbidden.Code || bodyErrorCode(forbidden.Body.String()) != bodyErrorCode(absent.Body.String()) {
		t.Fatal("forbidden/absent mix differed", forbidden.Code, absent.Code)
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM catalog_playlists`).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused copy wrote a playlist", count, err)
	}
	var q playbackv1.QueueReply
	selector := map[string]any{"selector": map[string]any{"container": map[string]any{"kind": "mix", "id": id}}}
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "forbidden-mix-play-0001"}, selector, 404, &q)
}
