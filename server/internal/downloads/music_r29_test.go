package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/testauth"
)

func TestMusicDownloadLadderDefaultsAndReplayR29(t *testing.T) {
	h := newHarness(t)
	ctx := playback.WithDeliveryContext(context.Background(), playback.DeliveryContext{DeviceClass: "web"})
	if _, e := h.db.Exec(`UPDATE libraries SET name='Music',kind='music' WHERE id='lib'`); e != nil {
		t.Fatal(e)
	}
	song, id := addCompactFixtureItem(t, h.db, "lib", compactcatalog.Track, "song", "Song", 0, "", nil)
	addCompactFixtureLink(t, h.db, id, h.assetID)
	if _, e := h.db.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) SELECT token,1,size,modified_ns,'music-r29' FROM catalog_assets WHERE token=?`, h.asset); e != nil {
		t.Fatal(e)
	}
	if _, e := h.db.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,detail_json) VALUES(?,0,'audio','flac','','',2,'stereo',1,0,'{"sampleRate":88200,"bitDepth":24,"bitRate":2000000}')`, h.asset); e != nil {
		t.Fatal(e)
	}
	caps := playback.BaselineCapabilityDocument()
	caps.Engines.Native.Evidence = playback.EvidenceDeclared
	caps.Engines.Native.Audio = []playback.ClientAudioCodec{{Codec: "flac", MaxChannels: 2, MaxSampleRate: 192000, MaxBitDepth: 32}, {Codec: "opus", MaxChannels: 2, MaxSampleRate: 48000}, {Codec: "aac", MaxChannels: 2, MaxSampleRate: 96000}}
	caps.Engines.Native.Transports = []playback.ClientTransport{{Transport: playback.TransportDirect, Containers: []string{"mp4", "flac", "ogg"}, Audio: []string{"flac", "opus", "aac"}}}
	testauth.InsertSession(t, h.db, h.viewer.Hash, h.viewer.AccountID, h.viewer.ProfileID, h.viewer.Authority, h.viewer.Role, "2099-01-01T00:00:00Z")
	raw, _ := json.Marshal(caps)
	if _, e := playback.New(h.db).PublishCapabilities(ctx, h.viewer, raw); e != nil {
		t.Fatal(e)
	}
	settleDownloadsProjection(t, h.db)
	view, e := h.service.Options(ctx, h.viewer, song, true)
	if e != nil {
		t.Fatal(e)
	}
	if view.DefaultQuality != "320" || len(view.Options) != 4 {
		t.Fatal(view)
	}
	for i, q := range []string{"original", "cd-quality", "320", "96"} {
		if view.Options[i].Quality != q {
			t.Fatal(view.Options)
		}
	}
	batch, e := h.service.Submit(ctx, h.viewer, Request{OperationID: "music-omission", MediaID: song}, nil)
	if e != nil || len(batch.Items) != 1 || batch.Items[0].Quality != "320" {
		t.Fatal(batch, e)
	}
	if e = dbwork.WithWriteTx(ctx, h.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO preference_scopes(authority,account_id,profile_id,scope,device_key,revision,updated_ms) VALUES(?,?,?,?,?,1,1)`, h.viewer.Authority, h.viewer.AccountID, h.viewer.ProfileID, operations.ScopeDeviceClass, "web"); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO preference_values(authority,account_id,profile_id,scope,device_key,key,kind,text_value) VALUES(?,?,?,?,?,'music.qualityDownloads','string','96')`, h.viewer.Authority, h.viewer.AccountID, h.viewer.ProfileID, operations.ScopeDeviceClass, "web")
		return err
	}); e != nil {
		t.Fatal(e)
	}
	replay, e := h.service.Submit(ctx, h.viewer, Request{OperationID: "music-omission", MediaID: song}, nil)
	if e != nil || !replay.Duplicate || replay.BatchID != batch.BatchID || replay.Items[0].Quality != "320" {
		t.Fatal(replay, e)
	}
	if _, e = h.service.Submit(ctx, h.viewer, Request{OperationID: "music-omission", MediaID: song, Quality: "original"}, nil); !errors.Is(e, operations.ErrIdempotencyKeyReused) {
		t.Fatal("explicit original replaced omission", e)
	}
	next, e := h.service.Submit(ctx, h.viewer, Request{OperationID: "music-new-default", MediaID: song}, nil)
	if e != nil || len(next.Items) != 1 || next.Items[0].Quality != "96" {
		t.Fatal(next, e)
	}
	original, e := h.service.Submit(ctx, h.viewer, Request{OperationID: "music-explicit-original", MediaID: song, Quality: "original"}, nil)
	if e != nil || len(original.Items) != 1 || original.Items[0].Quality != "original" {
		t.Fatal(original, e)
	}
}
func TestMusicDownloadSourceEquivalenceR29(t *testing.T) {
	lossless, lossy := true, false
	for _, tc := range []struct {
		q    string
		s    source
		want bool
	}{
		{"cd-quality", source{Kind: "song", AudioCodec: "flac", AudioRate: 44100, AudioDepth: 16, AudioLossless: &lossless}, true},
		{"cd-quality", source{Kind: "song", AudioCodec: "flac", AudioRate: 192000, AudioDepth: 24, AudioLossless: &lossless}, false},
		{"320", source{Kind: "song", AudioCodec: "mp3", AudioBitrateBPS: 128000, AudioLossless: &lossy}, true},
		{"96", source{Kind: "song", AudioCodec: "mp3", AudioBitrateBPS: 128000, AudioLossless: &lossy}, false},
		{"cd-quality", source{Kind: "song", AudioCodec: "wavpack", AudioRate: 192000, AudioDepth: 24, AudioBitrateBPS: 1000000}, false},
		{"original", source{Kind: "song", Cue: true}, false},
	} {
		if got := musicSourceEquivalent(tc.q, tc.s); got != tc.want {
			t.Fatal(tc.q, tc.s, got)
		}
	}
}
