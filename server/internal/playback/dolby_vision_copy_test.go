package playback

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func observedProfile7Source(layer string) *DeliverySource {
	return videoSource("mkv", "hevc", assets.StreamDetail{
		Width: 1920, Height: 1080, BitDepth: 10, FrameRate: 24,
		DynamicRange: assets.RangeDolbyVision, DolbyVisionProfile: 7,
		DolbyVisionCompatibility: 6, DolbyVisionLevel: 6, EnhancementLayer: true,
		StreamBreadth: assets.StreamBreadth{DolbyVisionMetadataKnown: true, DolbyVisionRPU: true, DolbyVisionBaseLayer: true, DolbyVisionEnhancementType: layer},
	}, SourceAudio{Codec: "aac", Channels: 2, Default: true, StreamDetail: assets.StreamDetail{Profile: "LC", SampleRate: 48000}})
}

func packetQualifiedUnitFacts() *decoder.ToolchainFacts {
	// Synthetic qualification is confined to this unit fixture. Production
	// obtains these facts only from the selected canonical bundle verifier.
	return &decoder.ToolchainFacts{Identity: "unit-packet-qualified", DolbyVisionProfile7To81: true,
		BitstreamFilters: map[string]bool{"dovi_profile": true, "dovi_rpu": true, "filter_units": true}}
}

func TestProfile7CopyUsesQualifiedRewriteAndDelivered81Signalling(t *testing.T) {
	for _, layer := range []string{"MEL", "FEL"} {
		t.Run(layer, func(t *testing.T) {
			source := observedProfile7Source(layer)
			p := decide(t, source, appleTVProfile(), func(in *DeliveryInput) {
				in.Server, in.FMP4Output = packetQualifiedUnitFacts(), true
			})
			if p.Strategy != DeliveryCopyRemux || p.VideoAction != "copy" || p.OutputContainer != "fmp4_hls" || p.OutputRange != assets.RangeDolbyVision || p.ConversionTarget == nil || p.ConversionTarget.DolbyVisionConversion != decoder.DolbyVisionProfile7To81 {
				t.Fatalf("qualified copy not selected: %+v", p)
			}
			if p.Trace.Video.DolbyVisionProfile != 7 || p.Trace.Video.DolbyVisionCompatibility != 6 || source.Video.DolbyVisionProfile != 7 || !source.Video.EnhancementLayer {
				t.Fatal("delivered metadata overwrote source facts", p.Trace.Video, source.Video)
			}
			if !containsString(p.Trace.Notes, NoteDolbyVisionEnhancementDiscarded) || containsString(p.Trace.Notes, NoteDolbyVisionFELDiscarded) != (layer == "FEL") {
				t.Fatal("enhancement loss not exact", p.Trace.Notes)
			}
			_, args, _, err := p.CodecArgs()
			if err != nil || !strings.Contains(strings.Join(args, " "), "-bsf:v dovi_profile=mode=2 -c:v copy") || strings.Contains(strings.Join(args, " "), "dovi_rpu=mode") {
				t.Fatal("copy producer did not use checked helper", args, err)
			}
			manifest := string(hlsMasterManifest(&p))
			if hevcSampleEntry(&p) != "dvh1" || videoCodecString(&p) != "dvh1.08.06" || !strings.Contains(manifest, `SUPPLEMENTAL-CODECS="dvh1.08.06/db1p"`) || strings.Contains(manifest, "dvh1.07") {
				t.Fatal("HLS signalled source instead of delivered profile", manifest)
			}
		})
	}
}

func TestProfile7RewriteRequiresSourceBundleAndExactClientVariant(t *testing.T) {
	for name, edit := range map[string]func(*DeliverySource, *ClientProfile, *decoder.ToolchainFacts){
		"unqualified bundle": func(_ *DeliverySource, _ *ClientProfile, f *decoder.ToolchainFacts) {
			f.DolbyVisionProfile7To81 = false
		},
		"no identity": func(_ *DeliverySource, _ *ClientProfile, f *decoder.ToolchainFacts) { f.Identity = "" },
		"stock filter only": func(_ *DeliverySource, _ *ClientProfile, f *decoder.ToolchainFacts) {
			delete(f.BitstreamFilters, "dovi_profile")
		},
		"configuration only": func(s *DeliverySource, _ *ClientProfile, _ *decoder.ToolchainFacts) {
			s.Video.DolbyVisionMetadataKnown = false
		},
		"missing RPU": func(s *DeliverySource, _ *ClientProfile, _ *decoder.ToolchainFacts) { s.Video.DolbyVisionRPU = false },
		"missing base": func(s *DeliverySource, _ *ClientProfile, _ *decoder.ToolchainFacts) {
			s.Video.DolbyVisionBaseLayer = false
		},
		"unjoined enhancement": func(s *DeliverySource, _ *ClientProfile, _ *decoder.ToolchainFacts) { s.Video.EnhancementLayer = false },
		"client HLG only": func(_ *DeliverySource, c *ClientProfile, _ *decoder.ToolchainFacts) {
			c.Video[1].DolbyVisionProfile8Compatibility = []int{4}
		},
		"client lacks DV": func(_ *DeliverySource, c *ClientProfile, _ *decoder.ToolchainFacts) {
			c.Video[1].DolbyVisionProfiles = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			source, client, facts := observedProfile7Source("FEL"), appleTVProfile(), packetQualifiedUnitFacts()
			edit(source, &client, facts)
			p := decide(t, source, client, func(in *DeliveryInput) { in.Server, in.FMP4Output = facts, true })
			if p.OutputRange == assets.RangeDolbyVision || p.ConversionTarget != nil && p.ConversionTarget.DolbyVisionConversion != "" || containsString(p.Trace.Notes, NoteDolbyVisionFELDiscarded) {
				t.Fatal("unqualified rewrite selected", p)
			}
		})
	}
}

func TestDeclaredProfile7OriginalRemainsOriginal(t *testing.T) {
	source := observedProfile7Source("FEL")
	source.Container = "mp4"
	client := appleTVProfile()
	client.Video[1].DolbyVisionProfiles = append(client.Video[1].DolbyVisionProfiles, 7)
	p := decide(t, source, client, func(in *DeliveryInput) { in.Server, in.FMP4Output = packetQualifiedUnitFacts(), true })
	if p.Strategy != DeliveryOriginal || p.VideoAction != "original" || p.ConversionTarget != nil || containsString(p.Trace.Notes, NoteDolbyVisionEnhancementDiscarded) {
		t.Fatal("otherwise playable declared source was rewritten", p)
	}
}

// A repackaged profile 7 source keeps its enhancement layer for a device that
// declares profile 7, even when the bundle would be able to strip or rewrite
// it (VIDEO §3 VS-1c). The untouched stream exists only in fMP4, so that
// transport is chosen even when another one is offered first.
func TestDeclaredProfile7RepackageKeepsTheSourceUntouched(t *testing.T) {
	for name, facts := range map[string]*decoder.ToolchainFacts{
		"qualified bundle": packetQualifiedUnitFacts(),
		"no strip filters": {Identity: "unit", BitstreamFilters: map[string]bool{}},
	} {
		t.Run(name, func(t *testing.T) {
			source := observedProfile7Source("FEL")
			client := appleTVProfile()
			client.Video[1].DolbyVisionProfiles = append(client.Video[1].DolbyVisionProfiles, 7)
			client.Video[1].CodecTags = []string{"hvc1", "dvhe"}
			p := decide(t, source, client, func(in *DeliveryInput) { in.Server, in.FMP4Output = facts, true })
			if p.Strategy != DeliveryCopyRemux || p.VideoAction != "copy" || p.OutputContainer != "fmp4_hls" || p.OutputRange != assets.RangeDolbyVision || p.ConversionTarget != nil {
				t.Fatalf("declared profile 7 was not repackaged untouched: %+v", p)
			}
			if containsString(p.Trace.Notes, NoteDolbyVisionEnhancementDiscarded) {
				t.Fatal("enhancement layer was discarded for a device that takes it", p.Trace.Notes)
			}
			_, args, _, err := p.CodecArgs()
			if err != nil || !strings.Contains(strings.Join(args, " "), "-c:v copy") || strings.Contains(strings.Join(args, " "), "dovi_") {
				t.Fatal("profile 7 copy was filtered", args, err)
			}
			manifest := string(hlsMasterManifest(&p))
			if hevcSampleEntry(&p) != "dvhe" || videoCodecString(&p) != "dvhe.07.06" || !strings.Contains(manifest, `CODECS="dvhe.07.06`) || strings.Contains(manifest, "SUPPLEMENTAL-CODECS") {
				t.Fatal("untouched profile 7 was not signalled as such", manifest)
			}
		})
	}
}

// A device may declare profile 7 while the operating point that serves this
// picture does not take it; the untouched route must follow the point.
func TestDeclaredProfile7FollowsTheApplicableOperatingPoint(t *testing.T) {
	source := observedProfile7Source("MEL")
	client := appleTVProfile()
	client.Video[1].DolbyVisionProfiles = append(client.Video[1].DolbyVisionProfiles, 7)
	client.Video[1].CodecTags = []string{"hvc1", "dvhe"}
	client.Video[1].Limits = []ClientVideoLimit{{BitDepths: []int{8}, MaxWidth: 4096, MaxHeight: 2304, MaxFrameRate: 60}}
	if profile7Passthrough(source.Video, client) {
		t.Fatal("an 8-bit operating point opted out of the strip for a ten-bit picture")
	}
	client.Video[1].Limits = []ClientVideoLimit{{BitDepths: []int{10}, MaxWidth: 4096, MaxHeight: 2304, MaxFrameRate: 60}}
	if !profile7Passthrough(source.Video, client) {
		t.Fatal("the ten-bit operating point did not take the declared profile")
	}
}

// Without an fMP4 transport an untouched profile 7 stream cannot exist; the
// plan must fall back to the HDR10 base instead of building an invalid target.
func TestDeclaredProfile7WithoutFMP4FallsBackToTheBasePicture(t *testing.T) {
	source := observedProfile7Source("FEL")
	client := appleTVProfile()
	client.Video[1].DolbyVisionProfiles = append(client.Video[1].DolbyVisionProfiles, 7)
	client.Video[1].CodecTags = []string{"hvc1", "dvhe"}
	client.Transports = []ClientTransport{{Transport: TransportHLSTS, Containers: []string{"mpegts"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac", "ac3", "eac3", "flac"}}}
	facts := packetQualifiedUnitFacts()
	facts.BitstreamFilters = map[string]bool{"dovi_rpu": true, "filter_units": true}
	p := decide(t, source, client, func(in *DeliveryInput) { in.Server, in.FMP4Output = facts, false })
	if p.OutputContainer != "mpegts_hls" || p.OutputRange != assets.RangeHDR10 || hevcSampleEntry(&p) != "hvc1" {
		t.Fatalf("transport without fMP4 did not fall back to HDR10: %+v", p)
	}
}

// Declaring profile 7 says nothing about the sample entry the decoder takes.
func TestDeclaredProfile7WithoutDVHETagIsNotKeptUntouched(t *testing.T) {
	source := observedProfile7Source("FEL")
	client := appleTVProfile()
	client.Video[1].DolbyVisionProfiles = []int{7}
	client.Video[1].CodecTags = []string{"hvc1"}
	facts := packetQualifiedUnitFacts()
	facts.BitstreamFilters = map[string]bool{"dovi_rpu": true, "filter_units": true}
	p := decide(t, source, client, func(in *DeliveryInput) { in.Server, in.FMP4Output = facts, true })
	if hevcSampleEntry(&p) == "dvhe" || p.ConversionTarget != nil && p.ConversionTarget.CopyDolbyVision {
		t.Fatalf("hvc1-only decoder was given an untouched dvhe stream: %+v", p.ConversionTarget)
	}
	if p.OutputRange != assets.RangeHDR10 {
		t.Fatalf("hvc1-only decoder did not fall back to HDR10: %+v", p)
	}
}
