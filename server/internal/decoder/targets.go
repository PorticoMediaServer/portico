package decoder

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ConversionTarget is the planner's complete output contract. Empty packaging
// selects MPEG-TS for H.264/AAC/AC-3 and fragmented MP4 for the other codecs.
// Quality mode preserves source dimensions and uses a quality target rather
// than a fixed ladder rate. BitrateBPS, when supplied, still caps that mode.
// Copy actions remain on ConversionRequest; a copy target names source codecs.
type ConversionTarget struct {
	VideoCodec   string `json:"videoCodec"`
	BitDepth     int    `json:"bitDepth"`
	DynamicRange string `json:"dynamicRange"`
	ToneMap      bool   `json:"toneMap"`
	// Unmapped says an HDR picture is written as a standard one without tone
	// mapping, because the owner turned tone mapping off. It is the one way a
	// conversion from HDR to SDR may go without it: left out by accident, the
	// target is refused.
	Unmapped                 bool    `json:"unmapped,omitempty"`
	Width                    int     `json:"width"`
	Height                   int     `json:"height"`
	FrameRate                float64 `json:"frameRate"`
	VideoBitrateBPS          int     `json:"videoBitrateBps"`
	RateControl              string  `json:"rateControl"`
	Quality                  int     `json:"quality"`
	AudioCodec               string  `json:"audioCodec"`
	AudioChannels            int     `json:"audioChannels"`
	AudioChannelLayout       string  `json:"audioChannelLayout"`
	AudioBitrateBPS          int     `json:"audioBitrateBps"`
	AudioBitDepth            int     `json:"audioBitDepth"`
	AudioSampleRate          int     `json:"audioSampleRate"`
	Packaging                string  `json:"packaging"`
	SourceRange              string  `json:"sourceRange"`
	DolbyVisionProfile       int     `json:"dolbyVisionProfile"`
	DolbyVisionCompatibility int     `json:"dolbyVisionCompatibility"`
	CopyDolbyVision          bool    `json:"copyDolbyVision,omitempty"`
	// A packet rewrite, separate from source profile facts and image conversion.
	DolbyVisionConversion string `json:"dolbyVisionConversion,omitempty"`
	VideoProfile          string `json:"videoProfile,omitempty"`
	VideoLevel            int    `json:"videoLevel"`
	MasterDisplay         string `json:"masterDisplay,omitempty"`
	MaxCLL                string `json:"maxCLL,omitempty"`
}

var masteringDisplayPattern = regexp.MustCompile(`^G\([0-9]{1,5},[0-9]{1,5}\)B\([0-9]{1,5},[0-9]{1,5}\)R\([0-9]{1,5},[0-9]{1,5}\)WP\([0-9]{1,5},[0-9]{1,5}\)L\([0-9]{1,10},[0-9]{1,10}\)$`)
var maxCLLPattern = regexp.MustCompile(`^[0-9]{1,5},[0-9]{1,5}$`)
var audioLayoutPattern = regexp.MustCompile(`^[a-zA-Z0-9_.()+ -]{1,64}$`)

func (t ConversionTarget) Normalized() (ConversionTarget, error) {
	if t.SourceRange == "hdr10plus" {
		t.SourceRange = "hdr10_plus"
	}
	if t.VideoCodec == "" {
		t.VideoCodec = "h264"
	}
	if t.BitDepth == 0 {
		t.BitDepth = 8
	}
	if t.DynamicRange == "" {
		t.DynamicRange = "sdr"
	}
	if t.AudioCodec == "" {
		t.AudioCodec = "aac"
	}
	if t.AudioChannels == 0 {
		t.AudioChannels = 2
	}
	if t.RateControl == "" {
		t.RateControl = "bitrate"
	}
	if t.Packaging == "" {
		t.Packaging = "mpegts_hls"
		if (t.VideoCodec != "h264" && t.VideoCodec != "mpeg2video") || t.AudioCodec == "flac" || t.AudioCodec == "opus" || t.AudioCodec == "truehd" {
			t.Packaging = "fmp4_hls"
		}
	}
	if t.VideoProfile != "" && (t.VideoCodec != "h264" || t.VideoProfile != "high" && t.VideoProfile != "main" && t.VideoProfile != "baseline") {
		return t, ErrInvalidConfiguration
	}
	if !validVideoLevel(t.VideoCodec, t.VideoLevel) {
		return t, ErrInvalidConfiguration
	}
	if t.AudioBitDepth != 0 && t.AudioBitDepth != 16 && t.AudioBitDepth != 24 && t.AudioBitDepth != 32 {
		return t, ErrInvalidConfiguration
	}
	if t.AudioCodec != "flac" && t.AudioBitDepth != 0 {
		return t, ErrInvalidConfiguration
	}
	if t.AudioCodec == "flac" && t.AudioBitrateBPS > 0 {
		return t, ErrInvalidConfiguration
	}
	if t.VideoCodec != "h264" && t.VideoCodec != "hevc" && t.VideoCodec != "av1" && t.VideoCodec != "mpeg2video" && t.VideoCodec != "vp9" {
		return t, ErrInvalidConfiguration
	}
	// Targets also describe copied streams. Encoder restrictions are checked
	// by BuildConversion only for the streams it actually encodes.
	if t.BitDepth < 8 || t.BitDepth > 16 {
		return t, ErrInvalidConfiguration
	}
	if t.DynamicRange != "sdr" && t.DynamicRange != "hdr10" && t.DynamicRange != "hlg" && !(t.CopyDolbyVision && t.DynamicRange == "dolby_vision") {
		return t, ErrInvalidConfiguration
	}
	if t.DynamicRange != "sdr" && (t.BitDepth < 10 || t.ToneMap) {
		return t, ErrInvalidConfiguration
	}
	if t.SourceRange != "" && t.SourceRange != "sdr" && t.SourceRange != "hdr10" && t.SourceRange != "hdr10_plus" && t.SourceRange != "hlg" && t.SourceRange != "dolby_vision" {
		return t, ErrInvalidConfiguration
	}
	if base := DolbyVisionBaseRange(t.DolbyVisionProfile, t.DolbyVisionCompatibility); !t.CopyDolbyVision && base != "" && t.SourceRange == "dolby_vision" {
		t.SourceRange = base
	}
	// Profile 5 has no base layer. A reshaped conversion can deliver HDR10
	// from its IPT-PQ picture, so that source/target pair is valid.
	reshapedProfile5 := !t.CopyDolbyVision && t.DolbyVisionProfile == 5 && t.SourceRange == "dolby_vision" && t.DynamicRange == "hdr10"
	if t.SourceRange != "" && t.DynamicRange != "sdr" && t.SourceRange != t.DynamicRange && !(t.SourceRange == "hdr10_plus" && t.DynamicRange == "hdr10") && !reshapedProfile5 {
		return t, errors.New("dynamic_range_conversion_unavailable")
	}
	if t.CopyDolbyVision && (t.VideoCodec != "hevc" || t.BitDepth != 10 || t.DynamicRange != "dolby_vision" || t.SourceRange != "dolby_vision" || t.DolbyVisionProfile == 0 || t.Packaging != "fmp4_hls") {
		return t, ErrInvalidConfiguration
	}
	if t.DolbyVisionConversion != "" && (t.DolbyVisionConversion != DolbyVisionProfile7To81 || !t.CopyDolbyVision || t.DolbyVisionProfile != 7 || t.DolbyVisionCompatibility != 6) {
		return t, ErrInvalidConfiguration
	}
	if t.Unmapped && (t.ToneMap || t.DynamicRange != "sdr") {
		return t, ErrInvalidConfiguration
	}
	if t.SourceRange != "" && t.SourceRange != "sdr" && t.DynamicRange == "sdr" && !t.ToneMap && !t.Unmapped {
		return t, errors.New("hdr_requires_tone_mapping")
	}
	if t.RateControl != "bitrate" && t.RateControl != "quality" || t.Quality < 0 || t.Quality > 51 {
		return t, ErrInvalidConfiguration
	}
	if t.Width < 0 || t.Width > 32768 || t.Height < 0 || t.Height > 4320 || t.VideoBitrateBPS < 0 || t.VideoBitrateBPS > 200_000_000 || t.AudioBitrateBPS < 0 || t.AudioBitrateBPS > 4_096_000 || math.IsNaN(t.FrameRate) || math.IsInf(t.FrameRate, 0) || t.FrameRate < 0 || t.FrameRate > 240 {
		return t, ErrInvalidConfiguration
	}
	if t.AudioChannels < 1 || t.AudioChannels > 64 {
		return t, ErrInvalidConfiguration
	}
	switch t.AudioCodec {
	case "aac", "ac3", "eac3", "opus", "flac", "dts", "truehd", "mp3", "mp2", "alac":
	default:
		return t, ErrInvalidConfiguration
	}
	if t.AudioSampleRate < 0 || t.AudioSampleRate > 192000 {
		return t, ErrInvalidConfiguration
	}
	if t.AudioChannelLayout == "" {
		t.AudioChannelLayout = map[int]string{1: "mono", 2: "stereo", 6: "5.1", 8: "7.1"}[t.AudioChannels]
	}
	if t.AudioChannelLayout != "" && !audioLayoutPattern.MatchString(t.AudioChannelLayout) {
		return t, ErrInvalidConfiguration
	}
	if t.Packaging != "mpegts_hls" && t.Packaging != "fmp4_hls" {
		return t, ErrInvalidConfiguration
	}
	if t.Packaging == "mpegts_hls" && ((t.VideoCodec != "h264" && t.VideoCodec != "hevc" && t.VideoCodec != "mpeg2video") || t.AudioCodec == "flac" || t.AudioCodec == "opus" || t.AudioCodec == "truehd") {
		return t, errors.New("codec_requires_fragmented_mp4")
	}
	if t.MasterDisplay != "" && !masteringDisplayPattern.MatchString(t.MasterDisplay) || t.MaxCLL != "" && !maxCLLPattern.MatchString(t.MaxCLL) {
		return t, ErrInvalidConfiguration
	}
	if t.DolbyVisionProfile < 0 || t.DolbyVisionProfile > 9 || t.DolbyVisionCompatibility < 0 || t.DolbyVisionCompatibility > 6 {
		return t, ErrInvalidConfiguration
	}
	return t, nil
}

// CodecVideoBitrate supplies deliberately conservative ladders. It never changes
// a viewer's selected codec, range or dimensions in response to server load.
func CodecVideoBitrate(codec string, height int) int {
	rate := 35_000_000
	switch {
	case height <= 480:
		rate = 1_500_000
	case height <= 720:
		rate = 3_000_000
	case height <= 1080:
		rate = 8_000_000
	case height <= 1440:
		rate = 14_000_000
	}
	if codec == "hevc" {
		rate = rate * 7 / 10
	}
	if codec == "av1" {
		rate = rate * 6 / 10
	}
	return rate
}

func softwareVideoEncoder(codec string, f *ToolchainFacts) (string, error) {
	candidates := map[string][]string{"h264": {"libx264"}, "hevc": {"libx265"}, "av1": {"libsvtav1", "libaom-av1", "librav1e"}}[codec]
	for _, encoder := range candidates {
		if f == nil && codec != "av1" || f != nil && f.Encoders[encoder] {
			return encoder, nil
		}
	}
	return "", errors.New("video_encoder_unavailable")
}

func videoColorArgs(t ConversionTarget) []string {
	// Scaling SDR does not transform its colour space. Inherit the decoder's
	// description rather than relabelling BT.601 or full-range samples.
	if t.DynamicRange == "sdr" && !t.ToneMap {
		return nil
	}
	primaries, transfer, matrix := "bt709", "bt709", "bt709"
	if t.DynamicRange != "sdr" {
		primaries, matrix = "bt2020", "bt2020nc"
		transfer = "smpte2084"
		if t.DynamicRange == "hlg" {
			transfer = "arib-std-b67"
		}
	}
	return []string{"-color_primaries", primaries, "-color_trc", transfer, "-colorspace", matrix, "-color_range", "tv"}
}

func videoRateArgs(t ConversionTarget, encoder, preset string) []string {
	quality := t.Quality
	if quality == 0 {
		quality = 20
		if t.VideoCodec == "av1" {
			quality = 28
		}
	}
	out := []string{}
	switch encoder {
	case "libx264", "libx265":
		out = append(out, "-preset", preset)
	case "libsvtav1":
		out = append(out, "-preset", "8", "-svtav1-params", "lp=2"+svtHDRParams(t))
	case "libaom-av1":
		out = append(out, "-cpu-used", "6", "-row-mt", "1")
	case "librav1e":
		out = append(out, "-speed", "8")
	}
	if t.RateControl == "quality" {
		switch {
		case encoder == "librav1e":
			out = append(out, "-qp", strconv.Itoa(quality*5), "-b:v", "0")
		case encoder == "libaom-av1" || encoder == "libsvtav1":
			out = append(out, "-crf", strconv.Itoa(quality), "-b:v", "0")
		case encoder == "libx264" || encoder == "libx265":
			out = append(out, "-crf", strconv.Itoa(quality))
		default:
			out = append(out, hardwareQualityArgs(encoder, quality)...)
		}
		if t.VideoBitrateBPS > 0 {
			out = append(out, "-maxrate", strconv.Itoa(t.VideoBitrateBPS), "-bufsize", strconv.Itoa(t.VideoBitrateBPS*2))
		}
	} else {
		rate := t.VideoBitrateBPS
		if rate == 0 {
			height := t.Height
			if height == 0 {
				height = 1080
			}
			rate = CodecVideoBitrate(t.VideoCodec, height)
		}
		if encoder == "libsvtav1" {
			// SVT accepts a maximum bitrate only in CRF mode; -b:v with -maxrate
			// selects an unsupported CBR mode in FFmpeg. Keep the requested ceiling.
			out = append(out, "-crf", strconv.Itoa(quality), "-b:v", "0")
		} else {
			out = append(out, "-b:v", strconv.Itoa(rate))
		}
		out = append(out, "-maxrate", strconv.Itoa(rate), "-bufsize", strconv.Itoa(rate*2))
	}
	return out
}

func validVideoLevel(codec string, level int) bool {
	if level == 0 {
		return true
	}
	levels := []int{}
	switch codec {
	case "h264":
		levels = []int{10, 11, 12, 13, 20, 21, 22, 30, 31, 32, 40, 41, 42, 50, 51, 52, 60, 61, 62}
	case "hevc":
		levels = []int{30, 60, 63, 90, 93, 120, 123, 150, 153, 156, 180, 183, 186}
	}
	for _, allowed := range levels {
		if level == allowed {
			return true
		}
	}
	return false
}

func DolbyVisionBaseRange(profile, compatibility int) string {
	if profile == 7 {
		return "hdr10"
	}
	if profile == 8 {
		switch compatibility {
		case 1:
			return "hdr10"
		case 2:
			return "sdr"
		case 4:
			return "hlg"
		}
	}
	return ""
}

func hardwareQualityArgs(encoder string, quality int) []string {
	switch {
	case strings.HasSuffix(encoder, "_nvenc"):
		return []string{"-rc", "vbr", "-cq", strconv.Itoa(quality), "-b:v", "0"}
	case strings.HasSuffix(encoder, "_qsv"):
		return []string{"-global_quality", strconv.Itoa(quality), "-b:v", "0"}
	case strings.HasSuffix(encoder, "_vaapi"):
		return []string{"-rc_mode", "CQP", "-qp", strconv.Itoa(quality), "-b:v", "0"}
	case strings.HasSuffix(encoder, "_amf"):
		return []string{"-rc", "cqp", "-qp_i", strconv.Itoa(quality), "-qp_p", strconv.Itoa(quality), "-qp_b", strconv.Itoa(quality), "-b:v", "0"}
	case strings.HasSuffix(encoder, "_videotoolbox"):
		return []string{"-q:v", strconv.Itoa(max(1, 100-quality*2)), "-b:v", "0"}
	}
	return nil
}

func probeQualityArgs(spec backendSpec, device string, depth int) []string {
	args := probeArgs(spec, device, "")
	filtered := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "-b:v" {
			i++
			continue
		}
		filtered = append(filtered, args[i])
	}
	if depth == 10 {
		for i := 0; i+1 < len(filtered); i++ {
			if filtered[i] == "-vf" {
				filtered[i+1] = strings.NewReplacer("format=nv12", "format=p010le", "format=yuv420p", "format=p010le").Replace(filtered[i+1])
			}
		}
		if spec.upload == "" {
			filtered = append(filtered[:len(filtered)-4], append([]string{"-pix_fmt", "yuv420p10le"}, filtered[len(filtered)-4:]...)...)
		}
	}
	return append(filtered[:len(filtered)-4], append(hardwareQualityArgs(spec.encoder, 20), filtered[len(filtered)-4:]...)...)
}

// svtHDRParams carries HDR10 static metadata into SVT-AV1, which takes the
// same ST 2086 layout as x265 but in plain units (0.0001-precise chromaticity,
// cd/m² luminance). Values are only ever ones Normalized accepted.
func svtHDRParams(t ConversionTarget) string {
	if t.DynamicRange != "hdr10" {
		return ""
	}
	out := ""
	if t.MasterDisplay != "" && masteringDisplayPattern.MatchString(t.MasterDisplay) {
		numbers := regexp.MustCompile(`[0-9]+`).FindAllString(t.MasterDisplay, -1)
		if len(numbers) == 10 {
			unit := func(raw string, per float64) string {
				v, _ := strconv.ParseFloat(raw, 64)
				return strconv.FormatFloat(v/per, 'f', 4, 64)
			}
			out += ":mastering-display=G(" + unit(numbers[0], 50000) + "," + unit(numbers[1], 50000) + ")B(" + unit(numbers[2], 50000) + "," + unit(numbers[3], 50000) + ")R(" + unit(numbers[4], 50000) + "," + unit(numbers[5], 50000) + ")WP(" + unit(numbers[6], 50000) + "," + unit(numbers[7], 50000) + ")L(" + unit(numbers[8], 10000) + "," + unit(numbers[9], 10000) + ")"
		}
	}
	if t.MaxCLL != "" && maxCLLPattern.MatchString(t.MaxCLL) {
		out += ":content-light=" + t.MaxCLL
	}
	return out
}
