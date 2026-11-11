package streaming

import (
	"sort"
	"strings"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// Planner and producer share one output inventory.
type ConversionVideoOutput = decoder.ConversionVideoOutput
type ConversionAudioOutput = decoder.ConversionAudioOutput
type ConversionOutputs = decoder.ConversionOutputs

func DefaultConversionOutputs() ConversionOutputs {
	return decoder.AvailableConversionOutputs(decoder.CurrentToolchain())
}

var conversionOutputs atomic.Pointer[ConversionOutputs]

// SetConversionOutputs replaces what the planner may ask the converter for.
// The composition root calls it once the converter's own abilities are known
// (HEVC and HDR-preserving pictures, lossless and wider audio).
func SetConversionOutputs(o ConversionOutputs) {
	conversionOutputs.Store(&o)
}

func currentConversionOutputs() ConversionOutputs {
	if o := conversionOutputs.Load(); o != nil {
		return *o
	}
	return DefaultConversionOutputs()
}

// videoOutput is the picture a conversion is planned to produce.
type videoOutput struct {
	Codec     string
	BitDepth  int
	Range     string
	Transport string
}

// keptRange is the HDR range a conversion could keep for this source on this
// device: the range the device would be shown from the original, when it is
// HDR10 or HLG. Dolby Vision is kept through its base layer.
func keptRange(v *SourceVideo, client ClientProfile, codec *ClientVideoCodec) string {
	if v == nil || v.DynamicRange == "" || v.DynamicRange == mediafacts.RangeSDR {
		return ""
	}
	r := v.DynamicRange
	if r == "hdr10plus" || r == "hdr10_plus" {
		r = mediafacts.RangeHDR10
	}
	if r == mediafacts.RangeDolbyVision {
		r = dolbyVisionFallback(v)
		if r == "" && v.DolbyVisionProfile == 5 {
			// Profile 5 has no base layer, so a conversion is the only route.
			// A display that shows HDR10 gets a reshaped HDR10 picture, not SDR
			// (VIDEO §3 VS-1e).
			r = mediafacts.RangeHDR10
		}
	}
	if r != mediafacts.RangeHDR10 && r != mediafacts.RangeHLG {
		return ""
	}
	if codec == nil || !containsString(codec.DynamicRanges, r) || !client.presents(r) {
		return ""
	}
	return r
}

// chooseVideoOutput picks the picture a conversion produces for this device. ok is
// false when no output of this server reaches the device.
//
// The source's HDR is kept through a conversion only where the device shows it and
// the chip's own encoder makes the 10-bit output: converting 4K HDR in software is
// not real time, and a film that plays beats HDR that stutters. Otherwise the
// picture is standard range, and what matters is that it converts in real time:
// the chip's encoders first, then software HEVC and H.264, then software AV1. Within each of those, a picture tone-mapped from HDR is made at
// 8 bits before 10 (ten bits keep nothing once it is standard range), and a 10-bit
// standard-range source keeps its 10 bits where the device plays them, so animation
// does not band. Software AV1 at 10 bits, which a browser was given for every HDR
// film before, is several times too slow for a 4K film.
//
// For a browser, a tone-mapped picture is made on software H.264 before software HEVC,
// and until AV1 after a seek is settled (playback proof finding 1 on #2604) the chip's
// AV1 comes after the chip's other outputs. Keeping HDR outranks that: a browser shown
// HDR whose only chip 10-bit output it plays is AV1 gets the chip's AV1.
func chooseVideoOutput(v *SourceVideo, client ClientProfile, transports []ClientTransport, allowHDR bool, outputs ConversionOutputs, audio *SourceAudio, target QualityTarget) (videoOutput, bool) {
	browser := client.Client.Family == "web" || client.Client.Form == "browser"
	toneMapped := v != nil && v.DynamicRange != "" && v.DynamicRange != mediafacts.RangeSDR
	ranked := func(keep func(ConversionVideoOutput) bool, deeperFirst bool) []ConversionVideoOutput {
		out := []ConversionVideoOutput{}
		for _, o := range outputs.Video {
			if keep(o) {
				out = append(out, o)
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			a, b := realTimeRank(out[i], browser, toneMapped), realTimeRank(out[j], browser, toneMapped)
			if a != b {
				return a < b
			}
			if deeperFirst {
				return out[i].BitDepth > out[j].BitDepth
			}
			return out[i].BitDepth < out[j].BitDepth
		})
		return out
	}
	if allowHDR {
		hdr := ranked(func(o ConversionVideoOutput) bool { return o.BitDepth >= 10 && o.Hardware }, true)
		for _, o := range hdr {
			codec := client.videoCodec(o.Codec)
			if kept := keptRange(v, client, codec); kept != "" && containsString(o.Ranges, kept) {
				if chosen, ok := tryVideoOutput(o, kept, client, transports, outputs, audio, target); ok {
					return chosen, true
				}
			}
		}
	}
	deepSDR := !toneMapped && v != nil && v.BitDepth >= 10
	for _, o := range ranked(func(ConversionVideoOutput) bool { return true }, deepSDR) {
		if chosen, ok := tryVideoOutput(o, mediafacts.RangeSDR, client, transports, outputs, audio, target); ok {
			return chosen, true
		}
	}
	return videoOutput{}, false
}

// realTimeRank orders outputs by how surely they convert in real time: the chip's
// encoders, then software HEVC and H.264 (in the inventory's order), then software
// AV1, whose encoders are several times slower. For a browser the chip's AV1 comes
// after the chip's other outputs, and a picture tone-mapped from HDR (most often 4K,
// the heaviest case) is made on software H.264 before software HEVC.
func realTimeRank(o ConversionVideoOutput, browser, toneMapped bool) int {
	switch {
	case o.Hardware && browser && o.Codec == "av1":
		return 1
	case o.Hardware:
		return 0
	case browser && toneMapped && o.Codec == "h264":
		return 2
	case o.Codec != "av1":
		return 3
	}
	return 4
}

// tryVideoOutput is one output in one range, if the device decodes it and one of
// its HLS transports takes it, with the transport whose sound suits the device best.
func tryVideoOutput(o ConversionVideoOutput, r string, client ClientProfile, transports []ClientTransport, outputs ConversionOutputs, audio *SourceAudio, target QualityTarget) (videoOutput, bool) {
	codec := client.videoCodec(o.Codec)
	if codec == nil || !codec.decodesDepth(o.BitDepth) || !containsString(o.Ranges, r) {
		return videoOutput{}, false
	}
	if r == mediafacts.RangeSDR && len(codec.DynamicRanges) > 0 && !containsString(codec.DynamicRanges, mediafacts.RangeSDR) {
		return videoOutput{}, false
	}
	profile := "high"
	if o.Codec == "hevc" {
		profile = "main"
		if o.BitDepth == 10 {
			profile = "main 10"
		}
	}
	if o.Codec == "av1" {
		profile = "main"
	}
	if o.Codec == "h264" {
		profile = convertedH264Profile(codec)
	}
	if profile == "" || len(codec.Profiles) > 0 && !convertedProfileAllowed(profile, codec.Profiles) {
		return videoOutput{}, false
	}
	var best videoOutput
	score := -1
	for _, name := range o.Transports {
		for _, t := range transports {
			if t.Transport != name || !containsString(t.Video, o.Codec) {
				continue
			}
			audioScore := 0
			if audio != nil {
				converted := audioOutputFor(audio, client, target, &t, outputs)
				if !converted.NoRate {
					audioScore = converted.Channels * 10
					if converted.LosslessCodec {
						audioScore++
					}
				}
				if len(audioRejections(audio, client, t)) == 0 && serverCopyAudio(name, audio.Codec) && !bitrateOver(audio.BitRate, target.MaxAudioBitrateBPS) {
					audioScore = 1000
				}
				if audioScore == 0 {
					continue
				}
			}
			if audioScore > score {
				best, score = videoOutput{Codec: o.Codec, BitDepth: o.BitDepth, Range: r, Transport: name}, audioScore
			}
		}
	}
	return best, score >= 0
}

// losslessAudio reports whether a source track is losslessly coded, so that
// converting it to a lossy codec gives something up.
func losslessAudio(a *SourceAudio) bool {
	if a == nil {
		return false
	}
	switch a.Codec {
	case "flac", "alac", "truehd", "pcm", "mlp", "wavpack", "ape", "tta":
		return true
	case "dts":
		profile := strings.ToLower(a.Profile)
		return strings.Contains(profile, "ma") && strings.Contains(profile, "hd")
	}
	return false
}

func convertedProfileAllowed(profile string, profiles []string) bool {
	for _, p := range profiles {
		if profileName(p) == profileName(profile) || profile == "baseline" && profileName(p) == "constrained_baseline" {
			return true
		}
	}
	return false
}

func convertedH264Profile(c *ClientVideoCodec) string {
	if c == nil || len(c.Profiles) == 0 {
		return "high"
	}
	for _, p := range []string{"high", "main", "baseline"} {
		if convertedProfileAllowed(p, c.Profiles) || p == "baseline" && convertedProfileAllowed("constrained baseline", c.Profiles) {
			return p
		}
	}
	return ""
}
