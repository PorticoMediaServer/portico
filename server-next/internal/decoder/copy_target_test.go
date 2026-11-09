package decoder

import "testing"

func TestCopiedSourceFactsDoNotWidenEncoderAdmission(t *testing.T) {
	for _, channels := range []int{3, 4, 5, 7, 16} {
		target := ConversionTarget{VideoCodec: "h264", BitDepth: 10, AudioCodec: "aac", AudioChannels: channels}
		if _, err := BuildConversion(ConversionRequest{Target: &target, CopyVideo: true, CopyAudio: true}); err != nil {
			t.Fatal(channels, err)
		}
		if _, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, CopyAudio: true}); err == nil {
			t.Fatal("encoder admitted ten-bit H.264")
		}
		target.BitDepth = 8
		if _, err := BuildConversion(ConversionRequest{Target: &target, CopyVideo: true, ConvertAudio: true}); err == nil {
			t.Fatal("encoder admitted an unsupported layout", channels)
		}
	}
	for _, layout := range []string{"stereo,volume=0", "5.1;anull", "quad\n", "../../font"} {
		target := ConversionTarget{AudioChannelLayout: layout}
		if _, err := BuildConversion(ConversionRequest{Target: &target, CopyAudio: true}); err == nil {
			t.Fatal("accepted unbounded layout token", layout)
		}
	}
}
