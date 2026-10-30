package playback

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func TestConversionFitsDisplayedPictureWithinDecoderBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		detail        assets.StreamDetail
		width, height int
	}{
		{"rotated", assets.StreamDetail{Width: 1920, Height: 1080, Rotation: 90, BitDepth: 8, DynamicRange: "sdr", FrameRate: 24}, 1920, 1080},
		{"anamorphic", assets.StreamDetail{Width: 720, Height: 480, SampleAspectRatio: "32:27", BitDepth: 8, DynamicRange: "sdr", FrameRate: 24}, 720, 480},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := chromeProfile()
			client.Video = []ClientVideoCodec{{Codec: "h264", BitDepths: []int{8}, DynamicRanges: []string{"sdr"}, MaxWidth: tc.width, MaxHeight: tc.height}}
			source := videoSource("mkv", "vc1", tc.detail, SourceAudio{Index: 1, Codec: "aac", Channels: 2})
			p := decide(t, source, client, func(in *DeliveryInput) {
				in.Server = plannerToolchain()
				in.Target = QualityTarget{RungID: "original", AllowHDR: true}
			})
			w, h := convertedDimensions(p)
			if w > tc.width || h > tc.height {
				t.Fatalf("planned %dx%d for decoder bounded to %dx%d; target=%+v", w, h, tc.width, tc.height, p.ConversionTarget)
			}
		})
	}
}

func TestConversionKeepsBoundsWhenSourceGeometryIsUnknown(t *testing.T) {
	client := chromeProfile()
	client.Video = []ClientVideoCodec{{Codec: "h264", BitDepths: []int{8}, DynamicRanges: []string{"sdr"}, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRate: 30}}
	source := videoSource("mkv", "vc1", assets.StreamDetail{BitDepth: 8, DynamicRange: "sdr"}, SourceAudio{Index: 1, Codec: "aac", Channels: 2})
	p := decide(t, source, client, func(in *DeliveryInput) {
		in.Server = plannerToolchain()
		in.Target = QualityTarget{RungID: "original", AllowHDR: true}
	})
	if p.ConversionTarget.Width != 1920 || p.ConversionTarget.Height != 1080 || p.ConversionTarget.FrameRate != 30 || p.ConversionTarget.VideoLevel < 40 {
		t.Fatalf("missing source facts removed output bounds or understated the level: %+v", p.ConversionTarget)
	}
}

func TestAudioRenditionsUseTheAdmittedTransport(t *testing.T) {
	client := chromeProfile()
	client.Transports = []ClientTransport{{Transport: TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{"h264"}, Audio: []string{"aac"}}}
	source := videoSource("mkv", "vc1", avcDetail, SourceAudio{Index: 1, Codec: "aac", Channels: 2}, SourceAudio{Index: 2, Codec: "aac", Channels: 2})
	p := decide(t, source, client, func(in *DeliveryInput) { in.FMP4Output = true; in.Server = plannerToolchain() })
	if len(p.Renditions) != 2 {
		t.Fatalf("renditions: %+v", p.Renditions)
	}
	for _, r := range p.Renditions {
		if !strings.HasSuffix(renditionSegmentFile(r, 0), ".m4s") {
			t.Fatalf("fMP4-only device was given a TS rendition: %+v", r)
		}
	}
}

func TestAudioConversionCanCopyTenBitH264(t *testing.T) {
	client := chromeProfile()
	client.Video = []ClientVideoCodec{{Codec: "h264", BitDepths: []int{8, 10}, DynamicRanges: []string{"sdr"}, MaxWidth: 1920, MaxHeight: 1080}}
	source := videoSource("mkv", "h264", assets.StreamDetail{Width: 640, Height: 360, BitDepth: 10, DynamicRange: "sdr", FrameRate: 24}, SourceAudio{Index: 1, Codec: "truehd", Channels: 2})
	p, err := planDelivery(DeliveryInput{Source: source, Client: client, Server: plannerToolchain(), Policy: openPolicy(), Target: QualityTarget{RungID: "original", AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.VideoAction != "copy" || p.AudioAction != "convert" || p.ConversionTarget.BitDepth != 10 {
		t.Fatalf("should preserve ten-bit picture while converting unsupported audio: %+v", p)
	}
	if _, _, _, err := p.CodecArgs(); err != nil {
		t.Fatal(err)
	}
}

func TestPictureConversionCanCopyQuadAudio(t *testing.T) {
	source := videoSource("mkv", "vc1", avcDetail, SourceAudio{Index: 1, Codec: "aac", Channels: 4, Layout: "quad"})
	p, err := planDelivery(DeliveryInput{Source: source, Client: allOutputClient(), Server: plannerToolchain(), FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "original", AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.AudioAction != "copy" || p.ConversionTarget.AudioChannels != 4 || p.ConversionTarget.AudioChannelLayout != "quad" {
		t.Fatalf("copy changed audio layout: %+v", p.ConversionTarget)
	}
	if _, _, _, err := p.CodecArgs(); err != nil {
		t.Fatal(err)
	}
}
