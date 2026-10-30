package downloads

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/testauth"
	"testing"
)

func TestQualityR24AboveSourceStoresOriginalWithoutOptimization(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// Real catalogue evidence: known dimensions and an observed per-video rate.
	if _, err := h.db.Exec(`UPDATE catalog_assets SET duration=1,size=100000,height=720 WHERE token=(SELECT a.token FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_entities i ON i.id=l.entity_id WHERE i.public_id=pid_blob(?))`, h.item); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.Options(ctx, h.viewer, h.item, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Options) != 9 {
		t.Fatal(view.Options)
	}
	for _, option := range view.Options[2:7] {
		if !option.SameAsOriginal || option.RequiresPrepare || option.Estimated || option.EffectiveQuality != "original" {
			t.Fatalf("above source %+v", option)
		}
		batch, err := h.service.Submit(ctx, h.viewer, Request{OperationID: "above-" + option.Quality, MediaID: h.item, Quality: RequestQuality(option.Quality)}, nil)
		if err != nil || len(batch.Items) != 1 || batch.Items[0].Quality != "original" {
			t.Fatalf("created conversion %+v %v", batch, err)
		}
	}
	var converted int
	if err = h.db.QueryRow(`SELECT count(*) FROM download_preparations WHERE quality<>'original' OR recipe<>''`).Scan(&converted); err != nil || converted != 0 {
		t.Fatal(converted, err)
	}
}
func TestQualityR24UnknownAndAudioNeverClaimSourceEquivalence(t *testing.T) {
	for _, s := range []source{{Height: 1080}, {VideoBitrateBPS: 1000000}, {Kind: "song", Height: 1080, VideoBitrateBPS: 1000000}} {
		if sameAsSource("2160p", s) {
			t.Fatal("fabricated source quality", s)
		}
	}
	h := newHarness(t)
	g, err := dbwork.BeginSnapshot(context.Background(), h.db)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Rollback()
	if q := resolvedQuality(context.Background(), g.Tx(), playback.CapabilityDocument{}, "2160p", source{Height: 720, VideoBitrateBPS: 500000}); q != "original" {
		t.Fatal(q)
	}
	for _, id := range []string{"1080p", "720p", "1440p", "360p", "auto"} {
		if preparationQuality(id) == nil {
			t.Fatal("old alias", id)
		}
	}
}

// A canonical video ceiling must not mislabel the surround audio retained by
// the actual device-compatible recipe.
func TestQualityR24OptionsDescribeRetainedSurroundBudget(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	old := decoder.CurrentToolchain()
	decoder.ConfigureToolchain(decoder.ToolchainFacts{Encoders: map[string]bool{"libx265": true, "libx264": true, "aac": true}})
	defer decoder.RestoreToolchain(old)
	testauth.InsertSession(t, h.db, h.viewer.Hash, h.viewer.AccountID, h.viewer.ProfileID, h.viewer.Authority, h.viewer.Role, "2099-01-01T00:00:00Z")
	if _, err := h.db.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) SELECT token,1,size,modified_ns,'f' FROM catalog_assets WHERE token=?`, h.asset); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,detail_json) VALUES(?,0,'video','hevc','','',0,'',1,0,?)`, h.asset, `{"width":3840,"height":2160,"bitDepth":10,"frameRate":24,"dynamicRange":"hdr10","bitRate":30000000}`); err != nil {
		t.Fatal(err)
	}
	// The source's own 5.1 audio is the fact that makes the rich recipe keep it.
	// The device's six-channel ceiling is not a source fact and cannot stand in
	// for one: without observed channels the recipe must assume 7.1 (FLAC).
	if _, err := h.db.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,detail_json) VALUES(?,1,'audio','aac','eng','',6,'5.1',0,0,'{}')`, h.asset); err != nil {
		t.Fatal(err)
	}
	device := playback.BaselineCapabilityDocument()
	device.Client.Family = "apple"
	device.Display.DynamicRanges = []string{"sdr", "hdr10"}
	device.AudioOutput.MaxChannels = 6
	device.Engines.Native.Evidence = playback.EvidenceDeclared
	device.Engines.Native.Video = append(device.Engines.Native.Video, playback.ClientVideoCodec{Codec: "hevc", BitDepths: []int{8, 10}, DynamicRanges: []string{"sdr", "hdr10", "hlg"}, MaxWidth: 3840, MaxHeight: 2160, MaxFrameRate: 60})
	device.Engines.Native.Audio = []playback.ClientAudioCodec{{Codec: "aac", MaxChannels: 6}}
	device.Engines.Native.Transports = []playback.ClientTransport{{Transport: playback.TransportDirect, Containers: []string{"mp4"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac"}}}
	raw, _ := json.Marshal(device)
	if _, err := playback.New(h.db).PublishCapabilities(ctx, h.viewer, raw); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.Options(ctx, h.viewer, h.item, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range view.Options {
		if option.Quality == "1080p-low" {
			if option.PreparedProfileID != "hevc-1080p-low-v4" || option.MaxVideoBitrateBPS != 8000000 || option.MaxAudioBitrateBPS != 384000 {
				t.Fatalf("recipe metadata %+v", option)
			}
			return
		}
	}
	t.Fatal("missing canonical quality")
}
