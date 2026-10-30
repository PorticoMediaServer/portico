package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// The real HTTP/queue/authority fixture uses declared media facts, not an audio
// decoder. These tests qualify selection, persistence and session transfer only.
func radioHTTPFixture(t *testing.T, counts ...int) (*v1Fixture, []catalogtest.Item) {
	t.Helper()
	f := newV1Fixture(t, 0)
	c := f.catalogTest
	root := t.TempDir()
	library := c.Library("radio-music", "Music", "music", root)
	album := c.Album(c.Artist(library, "Radio Artist"), "Radio album", 2026)
	items := []catalogtest.Item{}
	count := 3
	if len(counts) > 0 {
		count = counts[0]
	}
	for n := 0; n < count; n++ {
		path := filepath.Join(root, fmt.Sprintf("song-%d.mp4", n))
		if err := os.WriteFile(path, make([]byte, 1000), 0600); err != nil {
			t.Fatal(err)
		}
		item := c.Song(album, n+1, path, fmt.Sprintf("Radio song %d", n))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		c.Exec(`UPDATE catalog_assets SET video_codec='',width=0,height=0,size=?,modified_ns=? WHERE id=?`, info.Size(), info.ModTime().UnixNano(), item.Asset)
		c.Write(func(_ context.Context, tx *sql.Tx) error {
			return assets.PersistStreams(tx, item.Token, info.Size(), info.ModTime().UnixNano(), []assets.Stream{{Index: 0, Type: "audio", Codec: "aac", Channels: 2, Detail: assets.StreamDetail{SampleRate: 44100}, Default: true}})
		})
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT ?,src.id,a.token,src.incarnation,a.path,'r','{}',a.size,a.modified_ns FROM catalog_assets a JOIN library_sources src ON src.library_id='radio-music' WHERE a.id=? LIMIT 1`, fmt.Sprint(item.ID), item.Asset)
		confirmPlaybackInventory(t, f, fmt.Sprint(item.ID))
		c.Exec(`INSERT INTO audio_render_facts(asset_id,size,modified_ns,container,codec,sample_rate,channels,bit_depth,raw_frames,duration_frames,start_frames,end_frames,trim_source,decoder_config,measured_ms) SELECT token,size,modified_ns,'mp4','aac',44100,2,0,238140000,238140000,0,0,'none','EhA=',1 FROM catalog_assets WHERE id=?`, item.Asset)
		c.Genres(item.ID, "local", "Rock")
		items = append(items, item)
	}
	c.Drain()
	declareDecode(t, f, map[string]any{"codec": "aac", "containers": []string{"mp4"}, "maxSampleRate": 96000, "maxChannels": 2})
	f.v1.ReadyCheck = func(context.Context, playback.Session) (bool, error) { return true, nil }
	return f, items
}

func TestMusicRadioHTTPRefillsPastChunkBoundaryAndReopens(t *testing.T) {
	f, items := radioHTTPFixture(t)
	var q playbackv1.QueueReply
	body := map[string]any{"selector": map[string]any{"container": map[string]any{"kind": "radio", "id": items[0].Public}}}
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "radio-create-00000001"}, body, 201, &q)
	if q.Queue.DurationKnown || q.Queue.TotalDurationMs != nil {
		t.Fatal("buffered radio songs were reported as a final duration", q.Queue)
	}
	if q.Queue.Current == nil || len(q.Window) == 0 || q.Window[0].ItemID != items[0].Public || q.Queue.Segments[0].Kind != "radio" {
		t.Fatal(q)
	}
	first := q.Window[0].EntryID
	base := "/v1/queues/" + q.Queue.ID
	stale := q.Queue.Revision
	f.call("POST", base+"/segments", map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"placement": "end", "source": queueItems(items[1].Public)}, 200, &q)
	var inserted playbackv1.EntryPage
	f.call("GET", base+"/entries?from=0&limit=4", nil, nil, 200, &inserted)
	if len(inserted.Items) != 4 || inserted.Items[3].ItemID != items[1].Public {
		t.Fatal("manual end placement changed", inserted.Items)
	}
	manual := inserted.Items[3].EntryID
	for n := 0; n < 265; n++ {
		f.call("POST", base+":advance", map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"reason": "next"}, 200, &q)
		if q.Queue.Current == nil || q.Queue.Current.Position != int64(n+1) {
			t.Fatal(n, q.Queue)
		}
	}
	if q.Queue.Total <= 256 {
		t.Fatal("never crossed stored key chunk", q.Queue)
	}
	var page playbackv1.EntryPage
	f.call("GET", base+"/entries?from=0&limit=4", nil, nil, 200, &page)
	if len(page.Items) != 4 || page.Items[0].EntryID != first || page.Items[3].EntryID != manual {
		t.Fatal("old occurrence identity changed", page)
	}
	before := q.Queue
	beforePosition := q.Queue.Current.Position
	if w := f.raw("POST", base+":advance", f.owner.AccessToken, map[string]string{"If-Match": stale}, map[string]any{"reason": "next"}); w.Code != 412 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Construct a fresh production composition over the same persisted database.
	f.deps.PlaybackV1 = &playbackv1.Service{}
	f.handler = New(f.deps)
	f.v1 = f.deps.PlaybackV1
	f.call("POST", base+":advance", map[string]string{"If-Match": before.Revision}, map[string]any{"reason": "next"}, 200, &q)
	if q.Queue.Current.Position != beforePosition+1 {
		t.Fatal("reopen reset radio", q.Queue)
	}
	if w := f.raw("PATCH", base, f.owner.AccessToken, map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"shuffle": map[string]any{"on": true}}); w.Code < 400 {
		t.Fatal("radio order replaced by finite shuffle")
	}
}

func TestMusicRadioHTTPAdvanceTransfersOneActualSessionAndHonorsRevocation(t *testing.T) {
	f, items := radioHTTPFixture(t)
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "radio-playing-0000001"}, map[string]any{"selector": map[string]any{"container": map[string]any{"kind": "radio", "id": items[0].Public}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	if q.Session == nil || q.Session.ItemID != items[0].Public {
		t.Fatal(q)
	}
	old := q.Session.ID
	base := "/v1/queues/" + q.Queue.ID
	f.call("POST", base+":advance", map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"reason": "completion"}, 200, &q)
	if q.Session == nil || q.Session.ID == old || q.Session.ItemID == items[0].Public {
		t.Fatal(q)
	}
	var ended playbackv1.SessionView
	f.call("GET", "/v1/playback/sessions/"+old, nil, nil, 200, &ended)
	if ended.State != "ended" {
		t.Fatal("old authority not retired", ended)
	}
	// A policy/source retirement after selection cannot mint the next batch.
	f.catalogTest.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='radio-music'`)
	before := q.Queue
	if w := f.raw("POST", base+":advance", f.owner.AccessToken, map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"reason": "next"}); w.Code < 400 {
		t.Fatal("retired source admitted", w.Body.String())
	}
	var unchanged playbackv1.QueueView
	f.call("GET", base, nil, nil, 200, &unchanged)
	if unchanged.Revision != before.Revision || unchanged.Current.EntryID != before.Current.EntryID {
		t.Fatal("refusal changed authority", unchanged)
	}
}

func TestMusicRadioHTTPRechecksSeedAuthorityAfterSelection(t *testing.T) {
	f, items := radioHTTPFixture(t)
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "radio-revocation-0001"}, map[string]any{"selector": map[string]any{"container": map[string]any{"kind": "radio", "id": items[0].Public}}}, 201, &q)
	produce := f.v1.RadioKeys
	f.v1.RadioKeys = func(ctx context.Context, tx *sql.Tx, p identity.Principal, request catalog.RadioBatchRequest) (catalog.RadioBatch, error) {
		batch, err := produce(ctx, tx, p, request)
		if err == nil && !request.Start {
			f.catalogTest.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='radio-music'`)
		}
		return batch, err
	}
	base := "/v1/queues/" + q.Queue.ID
	if w := f.raw("POST", base+":advance", f.owner.AccessToken, map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"reason": "next"}); w.Code < 400 {
		t.Fatal("mid-command source revocation ignored", w.Body.String())
	}
	var unchanged playbackv1.QueueView
	f.call("GET", base, nil, nil, 200, &unchanged)
	if unchanged.Revision != q.Queue.Revision || unchanged.Total != q.Queue.Total || unchanged.Current.EntryID != q.Queue.Current.EntryID {
		t.Fatal("rejected batch changed queue", unchanged)
	}
}

func TestMusicRadioHTTPOneSongPreparedCommitKeepsARealNextOccurrence(t *testing.T) {
	f, items := radioHTTPFixture(t, 1)
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "radio-single-00000001"}, map[string]any{"selector": map[string]any{"container": map[string]any{"kind": "radio", "id": items[0].Public}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	base := "/v1/queues/" + q.Queue.ID
	for n := 0; n < 3; n++ {
		if q.Session == nil || q.Queue.Next == nil || !q.Queue.Next.Available {
			t.Fatal("one-song radio has no real next occurrence", q)
		}
		prepared := prepareNext(t, f, q, fmt.Sprintf("radio-single-prepare-%04d", n))
		if prepared.Presentation.AudioRender == nil || prepared.Presentation.AudioRender.URL == "" {
			t.Fatal(prepared)
		}
		url := prepared.Presentation.AudioRender.URL
		old := q.Session.ID
		f.call("POST", base+":commit-next", nil, map[string]any{"token": prepared.Token}, 200, &q)
		if q.Session == nil || q.Session.ID == old || q.Session.ItemID != items[0].Public || q.Session.Presentation.AudioRender == nil || q.Session.Presentation.AudioRender.URL != url {
			t.Fatal("prepared identity or authority changed", q)
		}
		var active int
		if err := f.db.QueryRow(`SELECT count(*) FROM playback_v1_sessions WHERE ended_ms=0`).Scan(&active); err != nil || active != 1 {
			t.Fatal("parallel active session", active, err)
		}
	}
}

func TestMusicRadioHTTPCreditedArtistSeedStartsAnActualCreditedSong(t *testing.T) {
	f, items := radioHTTPFixture(t)
	guest := f.catalogTest.Artist(f.catalogTest.Handle("radio-music"), "Guest artist")
	f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
		var main int64
		if err := tx.QueryRowContext(ctx, `SELECT album.artist_id FROM catalog_songs song JOIN catalog_albums album ON album.entity_id=song.album_id WHERE song.entity_id=?`, items[1].ID).Scan(&main); err != nil {
			return err
		}
		return compactcatalog.SetSongArtistsTx(ctx, tx, items[1].ID, []int64{main, guest.ID})
	})
	f.catalogTest.Drain()
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "radio-guest-000000001"}, map[string]any{"selector": map[string]any{"container": map[string]any{"kind": "radio", "id": guest.Public}}}, 201, &q)
	if len(q.Window) == 0 || q.Window[0].ItemID != items[1].Public {
		t.Fatal("guest radio substituted an album artist's other song", q.Window)
	}
	f.call("POST", "/v1/queues/"+q.Queue.ID+":advance", map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"reason": "next"}, 200, &q)
	if q.Queue.Current == nil || q.Queue.Current.Position != 1 {
		t.Fatal(q.Queue)
	}
}
