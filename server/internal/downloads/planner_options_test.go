package downloads

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/testauth"
	"testing"
)

func TestDownloadMenuUsesDeviceDocumentAndConverterInventory(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	testauth.InsertSession(t, h.db, h.viewer.Hash, h.viewer.AccountID, h.viewer.ProfileID, h.viewer.Authority, h.viewer.Role, "2099-01-01T00:00:00Z")
	player := playback.New(h.db)
	doc := playback.BaselineCapabilityDocument()
	doc.Engines.Native.Evidence = playback.EvidenceDeclared
	doc.Engines.Native.Video[0].MaxHeight = 480
	raw, _ := json.Marshal(doc)
	if _, err := player.PublishCapabilities(ctx, h.viewer, raw); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.Options(ctx, h.viewer, h.item, true)
	if err != nil {
		t.Fatal(err)
	}
	saw480 := false
	for _, option := range view.Options {
		if option.Quality == "480p" {
			saw480 = true
		}
		if option.Available && option.EffectiveQuality != "original" && option.TargetHeight > 480 {
			t.Fatal("offered a file this decoder cannot play", option)
		}
	}
	if !saw480 {
		t.Fatal(view.Options)
	}
	old := decoder.CurrentToolchain()
	decoder.ConfigureToolchain(decoder.ToolchainFacts{Encoders: map[string]bool{"libx265": true, "aac": true}})
	defer decoder.RestoreToolchain(old)
	view, err = h.service.Options(ctx, h.viewer, h.item, true)
	if err != nil || len(view.Options) != 9 || view.Options[0].Quality != "original" {
		t.Fatal(view, err)
	}
}

// A Dolby Vision profile 5 picture has no base layer a prepared copy could
// show: only the original is offered for it.
func TestDownloadMenuOffersOnlyTheOriginalForDolbyVisionWithoutABaseLayer(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	before, err := h.service.Options(ctx, h.viewer, h.item, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`INSERT OR IGNORE INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) SELECT token,1,size,modified_ns,'f' FROM catalog_assets WHERE token=?`, h.asset); err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`DELETE FROM asset_streams WHERE asset_id=? AND type='video'`, h.asset); err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,detail_json) VALUES(?,0,'video','hevc','','',0,'',1,0,?)`, h.asset, `{"width":3840,"height":2160,"bitDepth":10,"dynamicRange":"dolby_vision","dolbyVisionProfile":5}`); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.Options(ctx, h.viewer, h.item, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Options) != 9 || len(view.Options) != 9 || view.Options[0].Quality != "original" {
		t.Fatalf("offered before %d, for profile 5: %+v", len(before.Options), view.Options)
	}
	for _, o := range view.Options[1:] {
		if o.Kind == "optimized" && o.Available {
			t.Fatalf("Dolby Vision without base layer offered conversion %+v", o)
		}
	}

}
