package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestHLSUnknownArtifactsAreTypedNotFound(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"segment-invalid.ts", "segment-1.ts", "init-0.mp4", "../segment-000000.ts", "audio-100-000000.ts", "segment-000000.ts?x=1"} {
		if _, err := (&HLS{}).FileContext(ctx, "unused", name); !errors.Is(err, ErrHLSArtifactNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	db, h, id := conversionOutputFixture(t)
	if _, err := db.Exec(`UPDATE playback_sessions SET grant_hash=? WHERE id=?`, identity.Digest("token"), id); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.root, id)); err != nil {
		t.Fatal(err)
	}
	// An absent output cannot make an out-of-range name a conversion request.
	if _, err := h.FileContext(ctx, "token", "segment-100000.ts"); !errors.Is(err, ErrHLSArtifactNotFound) {
		t.Errorf("out of range before output: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(h.root, id), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.root, id, "generation"), []byte("3"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.FileContext(ctx, "token", "audio-0.m3u8"); !errors.Is(err, ErrHLSArtifactNotFound) {
		t.Errorf("no audio rendition: %v", err)
	}
	// Preserve the existing end-of-presentation answer once its known grid exists.
	if _, err := h.FileContext(ctx, "token", "segment-100000.ts"); !errors.Is(err, ErrGrantEnded) {
		t.Errorf("known presentation end: %v", err)
	}
	p := &DeliveryPlan{Duration: 100, Renditions: []AudioRenditionPlan{{Ordinal: 0, Packaging: "mpegts_hls"}}}
	for _, name := range []string{"audio-1.m3u8", "audio-1-init.mp4", "audio-0-init.mp4", "audio-1-000000.ts", "audio-0-999999.ts", "audio-0-000000.m4s"} {
		if _, err := h.renditionFile(ctx, id, name, p); !errors.Is(err, ErrHLSArtifactNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := h.renditionFile(ctx, id, "audio-0.m3u8", p); err != nil {
		t.Fatalf("declared rendition playlist: %v", err)
	}
	h.timelines = map[string]*copyTimeline{id: {}}
	if _, err := h.FileContext(ctx, "token", "segment-000000.ts"); err == nil || errors.Is(err, ErrHLSArtifactNotFound) || errors.Is(err, ErrGrantEnded) {
		t.Fatalf("broken empty timeline was hidden: %v", err)
	}

}

func TestFiniteUnknownArtifactsAreTypedNotFound(t *testing.T) {
	ctx, _, s, _, _, _, grant := finiteFixture(t)
	for _, name := range []string{"segment-invalid.ts", "../master.m3u8", "audio-0.m3u8", "text-0.m3u8", "video.m3u8", "init.mp4", "segment-000000.m4s", "segment-000099.ts", "segment-999999.ts", "segment-000000.ts?x=1"} {
		if _, err := s.HLSFileContext(ctx, grant, name); !errors.Is(err, ErrHLSArtifactNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestHLSArtifactMembershipMatchesPublishedRenditions(t *testing.T) {
	for _, packaging := range []string{"mpegts_hls", "fmp4_hls"} {
		plan := &DeliveryPlan{Duration: 100, OutputContainer: packaging, Renditions: []AudioRenditionPlan{{Ordinal: 0, Packaging: packaging}}, TextRenditions: []TextRenditionPlan{{}, {Selected: true}}}
		dir := t.TempDir()
		if err := publishRenditionManifests(dir, plan, hlsTimelineManifest(plan.Duration)); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"master.m3u8", "video.m3u8", "audio-0.m3u8", "text-0.m3u8", "text-1.m3u8", renditionSegmentFile(plan.Renditions[0], 0)} {
			if !hlsArtifactDeclared(name, plan) {
				t.Errorf("%s: declared %s rejected", packaging, name)
			}
		}
		for _, name := range []string{"audio-99.m3u8", "audio-00.m3u8", "audio-99-000000.ts", "audio-0-999999.ts", "text-99.m3u8", "text-00.m3u8", "../master.m3u8"} {
			if hlsArtifactDeclared(name, plan) {
				t.Errorf("%s: undeclared %s accepted", packaging, name)
			}
		}
		if hlsArtifactDeclared("init.mp4", plan) != (packaging == "fmp4_hls") || hlsArtifactDeclared("audio-0-init.mp4", plan) != (packaging == "fmp4_hls") {
			t.Fatal("initialization packaging differs from the published plan")
		}
		plan.AirPlay = true
		if !hlsArtifactDeclared("text-0.m3u8", plan) || hlsArtifactDeclared("text-1.m3u8", plan) {
			t.Fatal("AirPlay subtitle ordinals must use only the selected manifest tracks")
		}
	}
}
