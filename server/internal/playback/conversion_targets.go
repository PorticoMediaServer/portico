package playback

import (
	"sort"
	"strings"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
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
	if v == nil || v.DynamicRange == "" || v.DynamicRange == assets.RangeSDR {
		return ""
	}
	r := v.DynamicRange
	if r == "hdr10plus" || r == "hdr10_plus" {
		r = assets.RangeHDR10
	}
	if r == assets.RangeDolbyVision {
		r = dolbyVisionFallback(v)
		if r == "" && v.DolbyVisionProfile == 5 {
			// Profile 5 has no base layer, so a conversion is the only route.
			// A display that shows HDR10 gets a reshaped HDR10 picture, not SDR
			// (VIDEO §3 VS-1e).
			r = assets.RangeHDR10
		}
	}
	if r != assets.RangeHDR10 && r != assets.RangeHLG {
		return ""
	}
	if codec == nil || !containsString(codec.DynamicRanges, r) || !client.presents(r) {
		return ""
	}
	return r
}

// chooseVideoOutput picks the picture a conversion produces for this device:
// the first of the server's outputs the engine takes on one of its HLS
// transports, keeping the source's HDR where both sides can. ok is false when
// no output of this server reaches the device.
func chooseVideoOutput(v *SourceVideo, client ClientProfile, transports []ClientTransport, allowHDR bool, outputs ConversionOutputs, audio *SourceAudio, target QualityTarget) (videoOutput, bool) {
	preferredDepth := 8
	if v != nil && v.BitDepth >= 10 {
		preferredDepth = 10
	}
	ordered := append([]ConversionVideoOutput{}, outputs.Video...)
	// Retain source depth before reducing it, without promoting an 8-bit source.
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].BitDepth == preferredDepth && ordered[j].BitDepth != preferredDepth
	})
	for _, o := range ordered {
		codec := client.videoCodec(o.Codec)
		if codec == nil || !codec.decodesDepth(o.BitDepth) {
			continue
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
			continue
		}
		ranges := []string{}
		if kept := keptRange(v, client, codec); kept != "" && allowHDR && containsString(o.Ranges, kept) && o.BitDepth >= 10 {
			ranges = append(ranges, kept)
		}
		if containsString(o.Ranges, assets.RangeSDR) && (len(codec.DynamicRanges) == 0 || containsString(codec.DynamicRanges, assets.RangeSDR)) {
			ranges = append(ranges, assets.RangeSDR)
		}
		for _, r := range ranges {
			var best videoOutput
			score := -1
			for _, name := range o.Transports {
				for _, t := range transports {
					if t.Transport == name && containsString(t.Video, o.Codec) {
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
			}
			if score >= 0 {
				return best, true
			}
		}
	}
	return videoOutput{}, false
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
