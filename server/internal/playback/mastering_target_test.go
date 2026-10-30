package playback

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// A conversion that keeps HDR10 carries the source's mastering description to
// the encoder; one that tone-maps to SDR does not.
func TestKeptHDR10CarriesMasteringMetadata(t *testing.T) {
	detail := assets.StreamDetail{BitDepth: 10, Width: 3840, Height: 2160, FrameRate: 24, DynamicRange: assets.RangeHDR10, MasterDisplay: "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)", MaxCLL: "1000,400"}
	source := videoSource("mkv", "vc1", detail, SourceAudio{Index: 1, Codec: "aac", Channels: 2})
	client := allOutputClient()
	client.Video = []ClientVideoCodec{{Codec: "hevc", BitDepths: []int{8, 10}, DynamicRanges: []string{"sdr", "hdr10"}, MaxWidth: 4096, MaxHeight: 2160}}
	client.Display.DynamicRanges = []string{"sdr", "hdr10"}
	p, err := planDelivery(DeliveryInput{Source: source, Client: client, Server: plannerToolchain(), FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
	if err != nil || p.ConversionTarget == nil || p.ConversionTarget.DynamicRange != "hdr10" || p.ConversionTarget.MasterDisplay != detail.MasterDisplay || p.ConversionTarget.MaxCLL != "1000,400" {
		t.Fatalf("%+v %v", p.ConversionTarget, err)
	}
	_, args, _, err := p.CodecArgs()
	if err != nil || !strings.Contains(strings.Join(args, " "), "master-display="+detail.MasterDisplay) || !strings.Contains(strings.Join(args, " "), "max-cll=1000,400") {
		t.Fatal(args, err)
	}
	client.Video[0].DynamicRanges = []string{"sdr"}
	client.Display.DynamicRanges = []string{"sdr"}
	p, err = planDelivery(DeliveryInput{Source: source, Client: client, Server: plannerToolchain(), FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
	if err != nil || p.ConversionTarget == nil || p.ConversionTarget.MasterDisplay != "" {
		t.Fatalf("%+v %v", p.ConversionTarget, err)
	}
}
