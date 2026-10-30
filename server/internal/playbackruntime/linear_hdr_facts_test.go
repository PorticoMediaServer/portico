package playbackruntime

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func TestLinearVideoProbeRetainsCanonicalHDRFacts(t *testing.T) {
	raw := []byte(`{"codec_type":"video","codec_name":"hevc","profile":"Main 10","pix_fmt":"yuv420p10le","width":3840,"height":2160,"avg_frame_rate":"24000/1001","field_order":"progressive","color_transfer":"smpte2084","side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":5,"dv_level":6,"rpu_present_flag":1,"bl_present_flag":1,"dv_bl_signal_compatibility_id":0},{"side_data_type":"Content light level metadata","max_content":1000,"max_average":400}]}`)
	var probe linearProbeStream
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	source := linearVideoSource(&probe)
	if source.DynamicRange != assets.RangeDolbyVision || source.DolbyVisionProfile != 5 || source.DolbyVisionLevel != 6 || source.MaxCLL != "1000,400" {
		t.Fatalf("actual HDR/Dolby configuration lost: %+v", source.StreamDetail)
	}
	if source.BitDepth != 10 || source.ChromaSubsampling != "4:2:0" || source.FrameRateNumerator != 24000 || source.FrameRateDenominator != 1001 || source.ObservedInterlaced() == nil || *source.ObservedInterlaced() {
		t.Fatal("canonical measured source facts lost", source.StreamDetail)
	}
	// The configuration record does not prove decoded packet/frame presence,
	// HDR10+ or the enhancement layer's actual type.
	if source.DolbyVisionMetadataKnown || source.DolbyVisionRPU || source.DolbyVisionBaseLayer || source.HDR10Plus || source.DolbyVisionEnhancementType != "" {
		t.Fatal("configuration became a packet/frame observation", source.StreamBreadth)
	}
}

func TestLinearPlanDoesNotCopyDolbyVisionAsSDR(t *testing.T) {
	client := playback.BaselineClientProfile()
	client.Video = []playback.ClientVideoCodec{{Codec: "hevc", BitDepths: []int{10}, DynamicRanges: []string{assets.RangeSDR}, Evidence: playback.EvidenceBuiltin}}
	for i := range client.Transports {
		if client.Transports[i].Transport == playback.TransportHLSTS {
			client.Transports[i].Video = []string{"hevc"}
		}
	}
	work := playback.LinearWork{ClientProfile: client, TranscodingEnabled: false}
	work.Selection.Quality.Mode = "original"
	for _, dolby := range []bool{false, true} {
		side := ""
		if dolby {
			side = `,"side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":5,"rpu_present_flag":1,"bl_present_flag":1}]`
		}
		raw := []byte(fmt.Sprintf(`{"streams":[{"codec_type":"video","codec_name":"hevc","pix_fmt":"yuv420p10le","width":1920,"height":1080,"field_order":"progressive"%s}]}`, side))
		plan, err := linearPlan(raw, work, playback.LinearTSOnly)
		if dolby && err == nil {
			t.Fatal("Dolby Vision5 mislabeled/copied as SDR", plan)
		}
		if !dolby && (err != nil || plan.ConvertVideo) {
			t.Fatal("compatible SDR original stopped copying", plan, err)
		}
	}
}

func TestLinearMalformedVideoSideDataRefusesBeforePlanning(t *testing.T) {
	raw := []byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","pix_fmt":"yuv420p10le","width":1920,"height":1080,"side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":"five"}]}]}`)
	if _, err := linearPlan(raw, playback.LinearWork{ClientProfile: playback.BaselineClientProfile()}, playback.LinearTSOnly); err == nil {
		t.Fatal("malformed Dolby configuration silently became SDR")
	}
}

func TestLinearPlanCarriesUntouchedDolbyVisionProfile(t *testing.T) {
	client := playback.BaselineClientProfile()
	client.Video = []playback.ClientVideoCodec{{Codec: "hevc", BitDepths: []int{10}, DynamicRanges: []string{assets.RangeDolbyVision, assets.RangeHDR10}, DolbyVisionProfiles: []int{5, 7, 8}, CodecTags: []string{"hvc1", "dvh1", "dvhe"}, MaxWidth: 3840, MaxHeight: 2160}}
	client.Display.DynamicRanges = []string{assets.RangeDolbyVision, assets.RangeHDR10}
	client.Transports = []playback.ClientTransport{{Transport: playback.TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{"hevc"}}}
	work := playback.LinearWork{ClientProfile: client, TranscodingEnabled: false}
	work.Selection.Quality.Mode = "original"
	for _, profile := range []int{5, 7, 8} {
		compatibility := map[int]int{5: 0, 7: 6, 8: 1}[profile]
		raw := []byte(fmt.Sprintf(`{"streams":[{"codec_type":"video","codec_name":"hevc","profile":"Main 10","pix_fmt":"yuv420p10le","width":1920,"height":1080,"avg_frame_rate":"25/1","field_order":"progressive","side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":%d,"dv_level":6,"rpu_present_flag":1,"bl_present_flag":1,"dv_bl_signal_compatibility_id":%d}]}]}`, profile, compatibility))
		plan, err := linearPlan(raw, work, playback.LinearFragmentedOnly)
		if err != nil || !plan.Fragmented || !plan.CopyHEVC || plan.ConvertVideo || plan.Target != nil || plan.CopyDolbyVisionProfile != profile {
			t.Fatalf("profile %d lost its copied signalling without a conversion target: %+v %v", profile, plan, err)
		}
	}
}
