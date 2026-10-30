package decoder

import "testing"

// An HDR picture is written as a standard one without tone mapping only when
// the plan says the owner turned tone mapping off. A target that merely
// forgets the tone map is still refused, and "unmapped" never rides along
// with a tone map or an HDR output.
func TestUnmappedConversionNeedsTheOwnerSwitch(t *testing.T) {
	base := ConversionTarget{VideoCodec: "h264", BitDepth: 8, DynamicRange: "sdr", SourceRange: "hdr10", RateControl: "bitrate", VideoBitrateBPS: 8_000_000, AudioCodec: "aac", AudioChannels: 2, AudioSampleRate: 48000, AudioBitrateBPS: 192_000, Packaging: "mpegts_hls"}
	if _, err := base.Normalized(); err == nil || err.Error() != "hdr_requires_tone_mapping" {
		t.Fatalf("an HDR picture with no tone map and no owner switch: %v", err)
	}
	mapped := base
	mapped.ToneMap = true
	if _, err := mapped.Normalized(); err != nil {
		t.Fatal("a tone-mapped conversion was refused", err)
	}
	unmapped := base
	unmapped.Unmapped = true
	if _, err := unmapped.Normalized(); err != nil {
		t.Fatal("the owner's unmapped conversion was refused", err)
	}
	both := unmapped
	both.ToneMap = true
	if _, err := both.Normalized(); err == nil {
		t.Fatal("a target both mapped and unmapped was accepted")
	}
	hdrOut := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", SourceRange: "hdr10", Unmapped: true, RateControl: "bitrate", VideoBitrateBPS: 8_000_000, AudioCodec: "aac", AudioChannels: 2, AudioSampleRate: 48000, AudioBitrateBPS: 192_000, Packaging: "fmp4_hls"}
	if _, err := hdrOut.Normalized(); err == nil {
		t.Fatal("an HDR output marked unmapped was accepted")
	}
}
