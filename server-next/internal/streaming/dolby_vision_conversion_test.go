package streaming

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// A device without Dolby Vision but with an HDR10 display gets a reshaped
// HDR10 picture from profile 5, not SDR (VIDEO §3 VS-1e).
func TestProfile5ForAnHDRDisplayConvertsToHD(t *testing.T) {
	detail := hdr10Detail
	detail.DynamicRange = mediafacts.RangeDolbyVision
	detail.DolbyVisionProfile = 5
	detail.CodecTag = "dvh1"
	source := videoSource("mp4", "hevc", detail, SourceAudio{Index: 1, Codec: "eac3", Channels: 6})
	client := appleTVProfile()
	client.Video[1].DolbyVisionProfiles = nil
	facts := plannerToolchain()
	facts.Filters["libplacebo"] = true
	facts.VulkanAvailable = true
	outputs := decoder.AvailableConversionOutputs(facts)
	plan, err := planDelivery(
		DeliveryInput{
			Source:             source,
			Client:             client,
			Server:             facts,
			Outputs:            &outputs,
			FMP4Output:         true,
			Policy:             openPolicy(),
			Target:             QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true},
			Config:             DefaultDeliveryConfiguration(),
			TranscodingEnabled: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != DeliveryVideoConversion || plan.OutputRange != mediafacts.RangeHDR10 || plan.ToneMap || plan.ConversionTarget == nil ||
		plan.ConversionTarget.DynamicRange != mediafacts.RangeHDR10 {
		t.Fatalf("profile 5 did not convert to HDR10: %+v", plan.ConversionTarget)
	}
	graph, err := plan.conversionGraph(nil)
	if err != nil || !strings.Contains(strings.Join(graph.Video, " "), "libplacebo=") {
		t.Fatal("profile 5 HDR10 conversion does not reshape", graph, err)
	}
}
