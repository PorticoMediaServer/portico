package streaming

import (
	"errors"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

func traceAudioSource(audio *SourceAudio, chosenBy string, tracks int) *TraceAudio {
	if audio == nil {
		return nil
	}
	return &TraceAudio{
		Lossless:    copiedBool(audio.Lossless),
		StreamIndex: audio.Index,
		Codec:       audio.Codec,
		Channels:    audio.Channels,
		Language:    audio.Language,
		ObjectAudio: audio.ObjectAudio,
		Profile:     audio.Profile,
		Variant:     audio.AudioVariant,
		Layout:      audio.Layout,
		SampleRate:  audio.SampleRate,
		BitDepth:    audio.BitsPerSample,
		ChosenBy:    chosenBy,
		Tracks:      tracks,
	}
}

// sourceAudioAt is the source track with the given absolute stream index.
func sourceAudioAt(tracks []SourceAudio, index int) *SourceAudio {
	for i := range tracks {
		if tracks[i].Index == index {
			return &tracks[i]
		}
	}
	return nil
}

func transportNamed(transports []ClientTransport, name string) *ClientTransport {
	for i := range transports {
		if transports[i].Transport == name {
			return &transports[i]
		}
	}
	return nil
}

func routeEvaluation(t *DecisionTrace, route DeliveryStrategy) *RouteEvaluation {
	for i := range t.Routes {
		if t.Routes[i].Route == string(route) {
			return &t.Routes[i]
		}
	}
	return nil
}

func hasRejection(rejections []RouteRejection, codes ...string) bool {
	for _, r := range rejections {
		for _, c := range codes {
			if r.Code == c {
				return true
			}
		}
	}
	return false
}

func rejectionCodes(rejections []RouteRejection) []string {
	out := []string{}
	for _, r := range rejections {
		if !containsString(out, r.Code) {
			out = append(out, r.Code)
		}
	}
	return out
}

// bestCopyTransport finds the HLS transport that can carry the video (and, when
// audio is given, that audio track) untouched, both for the device and for this
// server's producers. It returns the transport with the fewest objections, so
// the trace names the nearest miss rather than an arbitrary one.
func bestCopyTransport(transports []ClientTransport, client ClientProfile, video *SourceVideo, audio *SourceAudio, allowHDR bool, copying bool) (string, []RouteRejection) {
	if len(transports) == 0 {
		return "", []RouteRejection{{RejectTransport, "hls"}}
	}
	name, best, found := "", []RouteRejection(nil), false
	for _, t := range transports {
		var r []RouteRejection
		if video != nil {
			r = append(r, videoRejections(video, client, t, allowHDR, copying)...)
			if !serverCopyVideo(t.Transport, video.Codec) && !hasRejection(r, RejectVideoCodec) {
				r = append(r, RouteRejection{RejectServerOutput, video.Codec + " in " + t.Transport})
			}
		}
		if audio != nil {
			r = append(r, audioRejections(audio, client, t)...)
			if !serverCopyAudio(t.Transport, audio.Codec) && !hasRejection(r, RejectAudioCodec) {
				r = append(r, RouteRejection{RejectServerOutput, audio.Codec + " in " + t.Transport})
			}
		}
		if !found || len(r) < len(best) {
			name, best, found = t.Transport, r, true
		}
		if len(r) == 0 {
			break
		}
	}
	return name, best
}

func audioBudget(t QualityTarget) int {
	if t.MaxAudioBitrateBPS > 0 {
		return t.MaxAudioBitrateBPS
	}
	return 192_000
}

// CodecArgs is consumed by every HLS producer.
func (p DeliveryPlan) CodecArgs() ([]string, []string, []decoder.HardwareStage, error) {
	graph, err := p.conversionGraph(nil)
	if err != nil {
		return nil, nil, nil, err
	}
	return graph.Input, append(append([]string{}, graph.Video...), graph.Audio...), graph.Stages, nil
}

func (p DeliveryPlan) conversionGraph(burn *decoder.BurnIn) (decoder.ConversionGraph, error) {
	if p.Profile != DeliveryProfileLegacy || p.Mode != "hls" {
		return decoder.ConversionGraph{}, errors.New("delivery plan does not describe ordinary HLS")
	}
	request := decoder.ConversionRequest{
		Target:    p.ConversionTarget,
		BurnIn:    burn,
		Toolchain: decoder.CurrentToolchain(),
		Probe: decoder.HardwareProbe{
			Backend:       decoder.HardwareBackend(p.HardwareBackend),
			Available:     p.HardwareBackend != "" && p.HardwareBackend != string(decoder.BackendSoftware),
			NativeFilters: p.HardwareNative,
			Device:        p.HardwareDevice,
		},
		ToneMap:          p.ToneMap,
		ToneMapAlgorithm: p.ToneMapAlgorithm,
		MaxHeight:        p.TargetHeight,
		MaxWidth:         p.TargetWidth,
		VideoBitrateBPS:  p.VideoBitrateBPS,
		AudioBitrateBPS:  p.AudioBitrateBPS,
		AudioChannels:    p.AudioChannels,
		AudioCodec:       p.OutputAudioCodec,
		SoftwarePreset:   p.SoftwarePreset,
		Deinterlace:      p.Deinterlace,
		MaxFrameRate:     p.MaxFrameRate,
		Downmix:          p.Downmix == DownmixITULimited,
		AudioSampleRate:  p.AudioSampleRate,
		SourceVideoCodec: p.SourceVideoCodec,
	}
	if p.ConversionToolchain != nil {
		request.Toolchain = p.ConversionToolchain
	}
	if p.ConversionProbe != nil && p.HardwareBackend == string(p.ConversionProbe.Backend) && p.HardwareBackend != string(decoder.BackendSoftware) {
		request.Probe = *p.ConversionProbe
	}
	switch p.VideoAction {
	case "copy":
		request.CopyVideo = true
	case "convert":
		request.ConvertVideo = true
	case "none":
	default:
		return decoder.ConversionGraph{}, errors.New("invalid delivery video action")
	}
	switch p.AudioAction {
	case "copy":
		request.CopyAudio = true
	case "convert":
		if p.ConversionTarget == nil && ((p.OutputAudioCodec != "aac" && p.OutputAudioCodec != "ac3" && p.OutputAudioCodec != "eac3") || (p.AudioChannels != 2 && p.AudioChannels != 6)) {
			return decoder.ConversionGraph{}, errors.New("invalid delivery audio action")
		}
		request.ConvertAudio = true
	case "none":
	default:
		return decoder.ConversionGraph{}, errors.New("invalid delivery audio action")
	}
	if request.ConvertVideo && p.Trace != nil && p.Trace.Video != nil && p.Trace.Video.DolbyVisionProfile > 0 {
		t := request.Target
		if t == nil {
			t = &decoder.ConversionTarget{
				VideoCodec:      p.OutputVideoCodec,
				BitDepth:        8,
				DynamicRange:    "sdr",
				ToneMap:         p.ToneMap,
				Unmapped:        p.ToneMapOff,
				Width:           p.TargetWidth,
				Height:          p.TargetHeight,
				FrameRate:       p.MaxFrameRate,
				VideoBitrateBPS: p.VideoBitrateBPS,
				AudioCodec:      p.OutputAudioCodec,
				AudioChannels:   p.AudioChannels,
				AudioSampleRate: p.AudioSampleRate,
				AudioBitrateBPS: p.AudioBitrateBPS,
				Packaging:       p.OutputContainer,
			}
		}
		copy := *t
		copy.DolbyVisionProfile = p.Trace.Video.DolbyVisionProfile
		copy.DolbyVisionCompatibility = p.Trace.Video.DolbyVisionCompatibility
		request.Target = &copy
	}
	if request.ConvertVideo && p.OutputVideoCodec == "h264" && request.Target == nil && p.Trace != nil && p.Trace.Video != nil {
		v := p.Trace.Video
		w, h := convertedDimensions(p)
		rate := v.FrameRate
		if p.MaxFrameRate > 0 {
			rate = min(rate, p.MaxFrameRate)
		}
		request.Target = &decoder.ConversionTarget{
			VideoCodec:      "h264",
			BitDepth:        8,
			DynamicRange:    "sdr",
			ToneMap:         p.ToneMap,
			Unmapped:        p.ToneMapOff,
			Width:           p.TargetWidth,
			Height:          p.TargetHeight,
			FrameRate:       p.MaxFrameRate,
			VideoBitrateBPS: p.VideoBitrateBPS,
			VideoLevel:      decoder.H264OutputLevel(w, h, rate),
			AudioCodec:      p.OutputAudioCodec,
			AudioChannels:   p.AudioChannels,
			AudioSampleRate: p.AudioSampleRate,
			AudioBitrateBPS: p.AudioBitrateBPS,
			Packaging:       p.OutputContainer,
		}
	}
	if request.Target != nil && (request.Target.VideoCodec == "hevc" || request.Target.VideoCodec == "h264") && request.Target.VideoLevel == 0 {
		target := *request.Target
		w, h := convertedDimensions(p)
		rate := target.FrameRate
		if p.Trace != nil && p.Trace.Video != nil {
			rate = p.Trace.Video.FrameRate
			if target.FrameRate > 0 {
				rate = min(rate, target.FrameRate)
			}
		}
		target.VideoLevel = decoder.HEVCOutputLevel(w, h, rate)
		if target.VideoCodec == "h264" {
			target.VideoLevel = decoder.H264OutputLevel(w, h, rate)
		}
		request.Target = &target
	}
	if p.Trace != nil && p.Trace.Video != nil {
		v := p.Trace.Video
		request.SourceBitDepth = v.BitDepth
		request.DisableHardwareDecode = v.Rotation != 0 || v.SampleAspectRatio != "" && v.SampleAspectRatio != "1:1" && v.SampleAspectRatio != "0:1"
		if request.Target != nil && request.Target.SourceRange == "" {
			target := *request.Target
			target.SourceRange = v.DynamicRange
			request.Target = &target
		}
	}
	return decoder.BuildConversion(request)
}

func serverConversionRejections(f *decoder.ToolchainFacts, hardware decoder.HardwareProbe, video *SourceVideo, audio *SourceAudio, videoOutput, audioEncoder string, toneMap bool) []RouteRejection {
	if f == nil {
		return nil
	}
	out := []RouteRejection{}
	if video != nil {
		encoder := map[string]string{"h264": "libx264", "hevc": "libx265", "av1": "libsvtav1"}[videoOutput]
		if videoOutput == "av1" {
			for _, name := range []string{"libsvtav1", "libaom-av1", "librav1e"} {
				if f.Encoders[name] {
					encoder = name
					break
				}
			}
		}
		if videoOutput == "h264" && hardware.Available && hardware.Encoder != "" {
			encoder = hardware.Encoder
		}
		if !f.Encoders[encoder] {
			out = append(out, RouteRejection{RejectServerOutput, videoOutput + " encoder unavailable"})
		}
	}
	for _, codec := range []string{func() string {
		if video != nil {
			return video.Codec
		}
		return ""
	}(), func() string {
		if audio != nil {
			if audio.NativeCodec != "" {
				return audio.NativeCodec
			}
			return audio.Codec
		}
		return ""
	}()} {
		if codec == "" {
			continue
		}
		if codec == "dts" {
			codec = "dca"
		}
		if !f.Decoders[codec] {
			out = append(out, RouteRejection{"source_codec_not_decodable", codec})
		}
	}
	if audioEncoder == "opus" {
		audioEncoder = "libopus"
	}
	if audio != nil && !f.Encoders[audioEncoder] {
		out = append(out, RouteRejection{RejectServerOutput, audioEncoder + " encoder unavailable"})
	}
	_, toneErr := decoder.BuildConversion(decoder.ConversionRequest{ConvertVideo: video != nil, ToneMap: toneMap, Probe: hardware, Toolchain: f})
	if toneMap && toneErr != nil {
		out = append(out, RouteRejection{"hdr_tone_mapping_filter_unavailable", ""})
	}
	return out
}
