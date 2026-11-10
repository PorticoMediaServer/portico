package streaming

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

func TestCommittedVideoTraceKeepsObservedSourceBreadthAndUnknowns(t *testing.T) {
	source := &SourceVideo{Index: 4, Codec: "hevc", StreamDetail: mediafacts.StreamDetail{
		Profile: "Main 10", BitDepth: 10, Width: 3840, Height: 2160, FrameRate: 23.976, DynamicRange: mediafacts.RangeDolbyVision,
		DolbyVisionProfile: 7, DolbyVisionCompatibility: 6, MasterDisplay: "G(8500,39850)B(6550,2300)R(35400,14600)WP(15635,16450)L(10000000,50)", MaxCLL: "1000,400", HDR10Plus: true,
		StreamBreadth: mediafacts.StreamBreadth{
			ChromaSubsampling:          "420",
			FrameRateNumerator:         24000,
			FrameRateDenominator:       1001,
			FrameRateMode:              "vfr",
			InterlaceAnalysis:          mediafacts.InterlaceFieldOrderProgressive,
			DolbyVisionMetadataKnown:   true,
			DolbyVisionRPU:             true,
			DolbyVisionBaseLayer:       true,
			DolbyVisionEnhancementType: "fel",
			DolbyVisionCMVersion:       "4.0",
		},
	}}
	trace := traceVideoSource(source)
	if trace.Interlaced == nil || *trace.Interlaced || trace.DolbyVisionRPU == nil || !*trace.DolbyVisionRPU || trace.DolbyVisionBaseLayer == nil || !*trace.DolbyVisionBaseLayer ||
		trace.DolbyVisionEnhancementType != "fel" ||
		trace.DolbyVisionCMVersion != "4.0" ||
		trace.FrameRateNumerator != 24000 ||
		trace.FrameRateDenominator != 1001 ||
		trace.FrameRateMode != "vfr" ||
		trace.ChromaSubsampling != "420" ||
		!trace.HDR10Plus ||
		trace.MasterDisplay != source.MasterDisplay ||
		trace.MaxCLL != "1000,400" {
		t.Fatalf("source facts lost: %+v", trace)
	}
	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	var stored TraceVideo
	if err = json.Unmarshal(raw, &stored); err != nil || !reflect.DeepEqual(*trace, stored) {
		t.Fatal("stored snapshot", err)
	}
	source.InterlaceAnalysis = ""
	source.DolbyVisionMetadataKnown = false
	source.DolbyVisionEnhancementType = ""
	source.FrameRateNumerator = 30
	if *trace.Interlaced || !*trace.DolbyVisionRPU || trace.FrameRateNumerator != 24000 || trace.DolbyVisionEnhancementType != "fel" {
		t.Fatal("source refresh changed committed facts", trace)
	}
	unknown := traceVideoSource(&SourceVideo{Codec: "hevc", StreamDetail: mediafacts.StreamDetail{DolbyVisionProfile: 7}})
	raw, _ = json.Marshal(unknown)
	for _, key := range []string{"interlaced", "dolbyVisionRpu", "dolbyVisionBaseLayer", "chromaSubsampling", "hdr10Plus", "frameRateNumerator"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatal("unobserved fact invented", string(raw))
		}
	}
	source.DolbyVisionMetadataKnown = true
	source.DolbyVisionRPU = false
	source.DolbyVisionBaseLayer = false
	negative := traceVideoSource(source)
	if negative.DolbyVisionRPU == nil || *negative.DolbyVisionRPU || negative.DolbyVisionBaseLayer == nil || *negative.DolbyVisionBaseLayer {
		t.Fatal("explicit negatives lost", negative)
	}
	if traceVideoSource(nil) != nil {
		t.Fatal("invented video")
	}
}

func TestPlanStoresActualSelectedSourceTechnicalFacts(t *testing.T) {
	source := videoSource(
		"mp4",
		"h264",
		mediafacts.StreamDetail{
			Width:         1920,
			Height:        1080,
			BitDepth:      8,
			DynamicRange:  mediafacts.RangeSDR,
			StreamBreadth: mediafacts.StreamBreadth{InterlaceAnalysis: mediafacts.InterlaceFieldOrderProgressive, ChromaSubsampling: "420", FrameRateNumerator: 24000, FrameRateDenominator: 1001},
		},
		SourceAudio{Codec: "aac", Channels: 2, Layout: "stereo", Language: "jpn", Default: true, StreamDetail: mediafacts.StreamDetail{Profile: "LC", SampleRate: 48000}},
	)
	plan := decide(t, source, appleTVProfile(), nil)
	if plan.Trace == nil || plan.Trace.Video == nil || plan.Trace.Audio == nil {
		t.Fatal(plan)
	}
	v, a := plan.Trace.Video, plan.Trace.Audio
	if v.Interlaced == nil || *v.Interlaced || v.ChromaSubsampling != "420" || v.FrameRateNumerator != 24000 || a.Profile != "LC" || a.Layout != "stereo" || a.SampleRate != 48000 || a.BitDepth != 0 {
		t.Fatalf("plan omitted actual technical source: video=%+v audio=%+v", v, a)
	}
}

func TestAudioSourceTraceRetainsHDCarrierWithoutInventingObjects(t *testing.T) {
	source := &SourceAudio{
		Codec:        "dts",
		Channels:     8,
		Layout:       "7.1",
		StreamDetail: mediafacts.StreamDetail{Profile: "DTS-HD MA", SampleRate: 96000, BitsPerSample: 24, StreamBreadth: mediafacts.StreamBreadth{AudioVariant: "dts_hd_ma"}},
	}
	trace := traceAudioSource(source, "explicit", 2)
	if trace.Profile != "DTS-HD MA" || trace.Variant != "dts_hd_ma" || trace.Layout != "7.1" || trace.SampleRate != 96000 || trace.BitDepth != 24 || trace.ObjectAudio != "" {
		t.Fatal(trace)
	}
	source.Profile = "DTS"
	source.Layout = "stereo"
	source.SampleRate = 48000
	if trace.Profile != "DTS-HD MA" || trace.Layout != "7.1" || trace.SampleRate != 96000 {
		t.Fatal("source refresh changed snapshot", trace)
	}
}
