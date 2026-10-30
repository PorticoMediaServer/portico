package decoder

import (
	"errors"
	"strconv"
)

func buildAudioTarget(t ConversionTarget, downmix bool, facts *ToolchainFacts) ([]string, error) {
	if t.AudioChannels != 1 && t.AudioChannels != 2 && t.AudioChannels != 6 && t.AudioChannels != 8 {
		return nil, ErrInvalidConfiguration
	}
	if t.AudioChannelLayout != map[int]string{1: "mono", 2: "stereo", 6: "5.1", 8: "7.1"}[t.AudioChannels] && !(t.AudioChannels == 6 && t.AudioChannelLayout == "5.1(side)") {
		return nil, ErrInvalidConfiguration
	}
	codec := t.AudioCodec
	encoder := codec
	if codec == "opus" {
		encoder = "libopus"
	}
	if codec != "aac" && codec != "ac3" && codec != "eac3" && codec != "opus" && codec != "flac" {
		return nil, ErrInvalidConfiguration
	}
	// Upstream AC-3/E-AC-3 encoders only encode up to 5.1. Eight-channel
	// E-AC-3 can still be copied, preserving its dependent substreams.
	if (codec == "ac3" || codec == "eac3") && t.AudioChannels > 6 {
		return nil, errors.New("audio_encoder_channel_layout_unavailable")
	}
	if facts != nil && facts.Encoders != nil && !facts.Encoders[encoder] {
		return nil, errors.New("audio_encoder_unavailable")
	}
	rate := t.AudioSampleRate
	if rate == 0 {
		rate = 48000
	}
	if codec == "aac" && !aacTargetRate(rate) {
		return nil, ErrInvalidConfiguration
	}
	if codec != "flac" && rate > 48000 {
		return nil, ErrInvalidConfiguration
	}
	if codec == "ac3" || codec == "eac3" {
		if rate != 48000 && rate != 44100 && rate != 32000 {
			return nil, ErrInvalidConfiguration
		}
	}
	if codec == "opus" && rate != 48000 && rate != 24000 && rate != 16000 && rate != 12000 && rate != 8000 {
		return nil, ErrInvalidConfiguration
	}
	out := []string{"-c:a", encoder, "-ac", strconv.Itoa(t.AudioChannels), "-ar", strconv.Itoa(rate), "-channel_layout", t.AudioChannelLayout}
	filters := "aresample=rematrix_maxval=1.0,aformat=channel_layouts=" + t.AudioChannelLayout
	if downmix {
		filters = DownmixFilter(t.AudioChannels)
		if t.AudioChannels == 6 {
			filters = "aresample=rematrix_maxval=1.0,aformat=channel_layouts=" + t.AudioChannelLayout
		}
		if t.AudioChannels == 1 {
			filters = "aresample=rematrix_maxval=1.0,aformat=channel_layouts=mono"
		}
		if t.AudioChannels == 8 {
			filters = "aresample=rematrix_maxval=1.0,aformat=channel_layouts=7.1"
		}
	}
	out = append(out, "-af", filters)
	if codec == "flac" {
		depth := t.AudioBitDepth
		if depth == 0 {
			depth = 24
		}
		sample := "s32"
		if depth == 16 {
			sample = "s16"
		}
		return append(out, "-sample_fmt", sample, "-bits_per_raw_sample", strconv.Itoa(depth), "-compression_level", "5", "-strict", "-2"), nil
	}
	bitrate := t.AudioBitrateBPS
	if bitrate == 0 {
		bitrate = 192000
		if t.AudioChannels >= 6 {
			bitrate = 640000
			if codec == "aac" {
				bitrate = 512000
			}
			if codec == "opus" {
				bitrate = 384000
			}
		}
	}
	if codec == "ac3" && bitrate > 640000 {
		return nil, ErrInvalidConfiguration
	}
	out = append(out, "-b:a", strconv.Itoa(bitrate))
	if codec == "opus" {
		out = append(out, "-mapping_family", "0")
		if t.AudioChannels > 2 {
			out[len(out)-1] = "1"
		}
	}
	return out, nil
}

func aacTargetRate(rate int) bool {
	for _, allowed := range []int{7350, 8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000} {
		if rate == allowed {
			return true
		}
	}
	return false
}
