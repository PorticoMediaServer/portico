package decoder

import (
	"sort"
	"strconv"
)

// ConversionOutputs is the target inventory shared by playback and prepared
// media. Transport names are the capability document's names.
type ConversionVideoOutput struct {
	Codec      string   `json:"codec"`
	BitDepth   int      `json:"bitDepth"`
	Ranges     []string `json:"ranges"`
	Transports []string `json:"transports"`
	// Hardware is a private planner hint from the selected device's actual
	// codec/depth probe. It is never inferred from FFmpeg's encoder names.
	Hardware bool `json:"-"`
}
type ConversionAudioOutput struct {
	Codec      string      `json:"codec"`
	Lossless   bool        `json:"lossless,omitempty"`
	Bitrates   map[int]int `json:"bitrates"`
	Transports []string    `json:"transports"`
}
type ConversionOutputs struct {
	Video []ConversionVideoOutput `json:"video"`
	Audio []ConversionAudioOutput `json:"audio"`
}

// AvailableConversionOutputs lists only software outputs the qualified tools
// can produce. Hardware accelerates a selected format; it does not invent a
// format that would lose its software fallback. Nil facts are used by pure
// planning fixtures and assume the bundled H.264/HEVC and audio encoders.
func AvailableConversionOutputs(f *ToolchainFacts) ConversionOutputs {
	hls := []string{"hls_ts", "hls_fmp4"}
	mp4 := []string{"hls_fmp4"}
	o := ConversionOutputs{}
	for _, codec := range []string{"hevc", "av1", "h264"} {
		if _, err := softwareVideoEncoder(codec, f); err != nil {
			continue
		}
		depths, transports := []int{10, 8}, mp4
		if codec == "hevc" {
			transports = []string{"hls_fmp4", "hls_ts"}
		}
		if codec == "h264" {
			depths, transports = []int{8}, hls
		}
		for _, depth := range depths {
			ranges := []string{"sdr"}
			if depth == 10 {
				ranges = append(ranges, "hdr10", "hlg")
			}
			o.Video = append(o.Video, ConversionVideoOutput{Codec: codec, BitDepth: depth, Ranges: ranges, Transports: transports})
		}
	}
	for _, codec := range []string{"flac", "eac3", "ac3", "opus", "aac"} {
		encoder := codec
		if codec == "opus" {
			encoder = "libopus"
		}
		if f != nil && !f.Encoders[encoder] {
			continue
		}
		rates := map[int]int{1: 96000, 2: 192000, 6: 512000, 8: 768000}
		transports := hls
		switch codec {
		case "flac":
			rates, transports = map[int]int{1: 0, 2: 0, 6: 0, 8: 0}, mp4
		case "opus":
			rates, transports = map[int]int{1: 64000, 2: 128000, 6: 384000, 8: 512000}, mp4
		case "ac3", "eac3":
			rates = map[int]int{1: 192000, 2: 384000, 6: 640000}
		}
		o.Audio = append(o.Audio, ConversionAudioOutput{codec, codec == "flac", rates, transports})
	}
	return o
}

// AvailableConversionOutputsForHardware keeps the software fallback inventory
// and stably promotes only outputs measured on this selected device. It does
// not advertise formats that would lose their software fallback, nor turn a
// generic backend success into an unmeasured codec or bit-depth claim.
func AvailableConversionOutputsForHardware(f *ToolchainFacts, probe HardwareProbe) ConversionOutputs {
	out := AvailableConversionOutputs(f)
	spec, known := backendSpecs[probe.Backend]
	if f == nil || f.Identity == "" || f.Identity != probe.Identity || !probe.Available || !known || !spec.supported() || probe.Backend == BackendSoftware {
		return out
	}
	for i := range out.Video {
		video := &out.Video[i]
		video.Hardware = probe.OutputEncoders[video.Codec+":"+strconv.Itoa(video.BitDepth)]
	}
	sort.SliceStable(out.Video, func(i, j int) bool { return out.Video[i].Hardware && !out.Video[j].Hardware })
	return out
}

func (o ConversionOutputs) AudioOutput(codec string) *ConversionAudioOutput {
	for i := range o.Audio {
		if o.Audio[i].Codec == codec {
			return &o.Audio[i]
		}
	}
	return nil
}

// SupportsConversion checks a prepared recipe against the same inventory used
// for interactive playback, instead of maintaining another codec classifier.
func (o ConversionOutputs) SupportsConversion(video string, depth int, audio string, channels int) bool {
	v := video == ""
	for _, output := range o.Video {
		if output.Codec == video && output.BitDepth == depth {
			v = true
		}
	}
	a := audio == ""
	if output := o.AudioOutput(audio); output != nil {
		_, a = output.Bitrates[channels]
	}
	return v && a
}
