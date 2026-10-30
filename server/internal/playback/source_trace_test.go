package playback

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func TestCommittedVideoTraceKeepsObservedSourceBreadthAndUnknowns(t *testing.T) {
	source := &SourceVideo{Index: 4, Codec: "hevc", StreamDetail: assets.StreamDetail{
		Profile: "Main 10", BitDepth: 10, Width: 3840, Height: 2160, FrameRate: 23.976, DynamicRange: assets.RangeDolbyVision,
		DolbyVisionProfile: 7, DolbyVisionCompatibility: 6, MasterDisplay: "G(8500,39850)B(6550,2300)R(35400,14600)WP(15635,16450)L(10000000,50)", MaxCLL: "1000,400", HDR10Plus: true,
		StreamBreadth: assets.StreamBreadth{ChromaSubsampling: "420", FrameRateNumerator: 24000, FrameRateDenominator: 1001, FrameRateMode: "vfr", InterlaceAnalysis: assets.InterlaceFieldOrderProgressive, DolbyVisionMetadataKnown: true, DolbyVisionRPU: true, DolbyVisionBaseLayer: true, DolbyVisionEnhancementType: "fel", DolbyVisionCMVersion: "4.0"},
	}}
	trace := traceVideoSource(source)
	if trace.Interlaced == nil || *trace.Interlaced || trace.DolbyVisionRPU == nil || !*trace.DolbyVisionRPU || trace.DolbyVisionBaseLayer == nil || !*trace.DolbyVisionBaseLayer || trace.DolbyVisionEnhancementType != "fel" || trace.DolbyVisionCMVersion != "4.0" || trace.FrameRateNumerator != 24000 || trace.FrameRateDenominator != 1001 || trace.FrameRateMode != "vfr" || trace.ChromaSubsampling != "420" || !trace.HDR10Plus || trace.MasterDisplay != source.MasterDisplay || trace.MaxCLL != "1000,400" {
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
	unknown := traceVideoSource(&SourceVideo{Codec: "hevc", StreamDetail: assets.StreamDetail{DolbyVisionProfile: 7}})
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
	source := videoSource("mp4", "h264", assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: assets.RangeSDR, StreamBreadth: assets.StreamBreadth{InterlaceAnalysis: assets.InterlaceFieldOrderProgressive, ChromaSubsampling: "420", FrameRateNumerator: 24000, FrameRateDenominator: 1001}}, SourceAudio{Codec: "aac", Channels: 2, Layout: "stereo", Language: "jpn", Default: true, StreamDetail: assets.StreamDetail{Profile: "LC", SampleRate: 48000}})
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
	source := &SourceAudio{Codec: "dts", Channels: 8, Layout: "7.1", StreamDetail: assets.StreamDetail{Profile: "DTS-HD MA", SampleRate: 96000, BitsPerSample: 24, StreamBreadth: assets.StreamBreadth{AudioVariant: "dts_hd_ma"}}}
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

func TestSourceTraceSurvivesActualDeliveryPlanStorage(t *testing.T) {
	db, _, session := conversionOutputFixture(t)
	var asset string
	if err := db.QueryRow(`SELECT asset_id FROM playback_sessions WHERE id=?`, session).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	source := &SourceVideo{Codec: "hevc", StreamDetail: assets.StreamDetail{Width: 3840, Height: 2160, BitDepth: 10, DynamicRange: assets.RangeHDR10, HDR10Plus: true, MasterDisplay: "observed mastering", MaxCLL: "1000,400", StreamBreadth: assets.StreamBreadth{InterlaceAnalysis: assets.InterlaceFieldOrderProgressive, FrameRateNumerator: 24000, FrameRateDenominator: 1001}}}
	audio := &SourceAudio{Index: 2, Codec: "dts", Channels: 8, Layout: "7.1", StreamDetail: assets.StreamDetail{Profile: "DTS-HD MA", SampleRate: 96000, BitsPerSample: 24, StreamBreadth: assets.StreamBreadth{AudioVariant: "dts_hd_ma"}}}
	trace := &DecisionTrace{Version: 1, Container: "matroska", Video: traceVideoSource(source), Audio: traceAudioSource(audio, "explicit", 2)}
	plan := DeliveryPlan{Profile: DeliveryProfileLegacy, SourceID: asset, SourceSize: 1, SourceModifiedNS: 1, Mode: "hls", Strategy: DeliveryVideoConversion, SourceContainer: "matroska", SourceVideoCodec: "hevc", SourceAudioCodec: "dts", VideoAction: "convert", AudioAction: "convert", OutputVideoCodec: "h264", OutputAudioCodec: "aac", AudioChannels: 2, HardwareBackend: "software", AudioStream: 2, Trace: trace}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = persistDeliveryPlan(tx, session, &plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A later catalogue enrichment leaves the same pinned source bytes. A read
	// must use the committed source snapshot, not splice in the new stream rows.
	if _, err = db.Exec(`UPDATE catalog_assets SET width=640,height=360 WHERE token=?`, asset); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadDeliveryPlan(context.Background(), db, session)
	if err != nil || loaded == nil || !reflect.DeepEqual(trace, loaded.Trace) {
		t.Fatalf("stored source snapshot changed: %+v %v", loaded, err)
	}
	if loaded.Trace.Video.Width != 3840 || loaded.Trace.Audio.SampleRate != 96000 || loaded.Trace.Audio.Profile != "DTS-HD MA" || loaded.Trace.Video.Interlaced == nil || *loaded.Trace.Video.Interlaced {
		t.Fatal(loaded.Trace)
	}
}
