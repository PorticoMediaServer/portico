package streaming

import (
	"testing"
)

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
