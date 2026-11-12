package streaming

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// A device without Dolby Vision but with an HDR10 display gets a reshaped
// HDR10 picture from profile 5, not SDR (VIDEO §3 VS-1e). This is the conversion-device
// setting at Automatic: the Vulkan filter is used, as a hardware job.
// profile5Plan plans a Dolby Vision profile 5 film for a device with an HDR10 display and no
// Dolby Vision, on a server whose FFmpeg has the reshaping filter and a working Vulkan device.
func profile5Plan(config DeliveryConfiguration) (DeliveryPlan, error) {
	facts := plannerToolchain()
	facts.Filters["libplacebo"] = true
	facts.VulkanAvailable = true
	return planDelivery(profile5Input(facts, config))
}

func profile5Input(facts *decoder.ToolchainFacts, config DeliveryConfiguration) DeliveryInput {
	detail := hdr10Detail
	detail.DynamicRange = mediafacts.RangeDolbyVision
	detail.DolbyVisionProfile = 5
	detail.CodecTag = "dvh1"
	source := videoSource("mp4", "hevc", detail, SourceAudio{Index: 1, Codec: "eac3", Channels: 6})
	client := appleTVProfile()
	client.Video[1].DolbyVisionProfiles = nil
	outputs := decoder.AvailableConversionOutputs(facts)
	return DeliveryInput{
		Source:             source,
		Client:             client,
		Server:             facts,
		Outputs:            &outputs,
		FMP4Output:         true,
		Policy:             openPolicy(),
		Target:             QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true},
		Config:             config,
		TranscodingEnabled: true,
	}
}

func TestProfile5ForAnHDRDisplayConvertsToHD(t *testing.T) {
	plan, err := profile5Plan(DefaultDeliveryConfiguration())
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
	// The picture is decoded and encoded in software, and the job still gets the graphics
	// device the reshaping filter runs on.
	if !conversionJobHardware(&plan, graph.Stages) {
		t.Fatal("a profile 5 conversion planned in software does not reach the graphics device", graph.Stages)
	}
}

// The conversion-device setting means what it says (console rule 32). With software encoding
// chosen, a working Vulkan device is not used: a profile 5 film is not converted, and the
// device that cannot play it as it is gets the refusal with Dolby Vision as its one reason.
func TestSoftwareEncodingNeverUsesTheGraphicsDevice(t *testing.T) {
	config := DefaultDeliveryConfiguration()
	config.HardwareBackend = string(decoder.BackendSoftware)
	plan, err := profile5Plan(config)
	var refused ErrDeliveryRefused
	if !errors.As(err, &refused) || plan.ConversionTarget != nil {
		graph, _ := plan.conversionGraph(nil)
		t.Fatalf("profile 5 with software encoding chosen: %v, planned %q %v %v", err, plan.Strategy, graph.Input, graph.Video)
	}
	if refused.Error() != "This server cannot prepare a version of this title that this device can play." {
		t.Fatalf("what the person is told: %q", refused.Error())
	}
	// The same answer, with the same reason code, as a server that has no graphics device.
	none := plannerToolchain()
	none.Filters["libplacebo"] = true
	_, without := planDelivery(profile5Input(none, DefaultDeliveryConfiguration()))
	var noDevice ErrDeliveryRefused
	if !errors.As(without, &noDevice) || noDevice.Code != refused.Code {
		t.Fatalf("software encoding answers %q, a server without a graphics device %v", refused.Code, without)
	}
}
