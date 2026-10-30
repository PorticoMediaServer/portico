package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"strconv"
)

func videoOf(s DeliverySource) *SourceVideo { return s.Video }

// Compare transports after selecting audio, so fragmented MP4 can keep a
// lossless or wider layout even when MPEG-TS appeared first.
func bestAudioConversionTransport(transports []ClientTransport, client ClientProfile, video *SourceVideo, audio *SourceAudio, target QualityTarget, outputs ConversionOutputs) (string, []RouteRejection) {
	name := ""
	var best convertedAudio
	var nearest []RouteRejection
	for _, t := range transports {
		_, rejects := bestCopyTransport([]ClientTransport{t}, client, video, nil, target.AllowHDR, true)
		if len(rejects) > 0 {
			if name == "" && nearest == nil {
				nearest = rejects
			}
			continue
		}
		out := audioOutputFor(audio, client, target, &t, outputs)
		if out.NoRate {
			if !out.NoOutput && audio != nil {
				nearest = append(nearest, RouteRejection{RejectAudioSampleRate, strconv.Itoa(audio.SampleRate)})
			}
			continue
		}
		better := name == "" || out.Channels > best.Channels || out.Channels == best.Channels && out.LosslessCodec && !best.LosslessCodec
		if better {
			name, best = t.Transport, out
		}
	}
	if name == "" {
		if nearest == nil {
			nearest = []RouteRejection{{RejectServerOutput, "no supported audio target"}}
		}
		return "", nearest
	}
	return name, nil
}

// measuredConversionProbe uses the same selected-bundle/device codec cell as
// output ranking. A generic available backend cannot label or execute an
// unmeasured output as hardware. This reads cached facts, never probes a device.
func measuredConversionProbe(facts *decoder.ToolchainFacts, probe decoder.HardwareProbe, codec string, depth int) decoder.HardwareProbe {
	for _, output := range decoder.AvailableConversionOutputsForHardware(facts, probe).Video {
		if output.Codec == codec && output.BitDepth == depth && output.Hardware {
			return probe
		}
	}
	probe.Available = false
	return probe
}

// Complete and validate the contract before persisting it for the producer.
func finishConversionTarget(p *DeliveryPlan, video *SourceVideo, audio *SourceAudio, client ClientProfile, probe decoder.HardwareProbe, facts *decoder.ToolchainFacts) error {
	t := decoder.ConversionTarget{VideoCodec: p.OutputVideoCodec, BitDepth: p.OutputBitDepth, DynamicRange: p.OutputRange, ToneMap: p.ToneMap, Unmapped: p.ToneMapOff, Width: p.TargetWidth, Height: p.TargetHeight, FrameRate: p.MaxFrameRate, VideoBitrateBPS: p.VideoBitrateBPS, AudioCodec: p.OutputAudioCodec, AudioChannels: p.AudioChannels, AudioSampleRate: p.AudioSampleRate, AudioBitrateBPS: p.AudioBitrateBPS, Packaging: p.OutputContainer}
	if p.VideoAction == "convert" {
		t.RateControl = "bitrate"
		if p.QualityID == "original" || p.QualityID == "automatic" || p.QualityID == "" {
			t.RateControl = "quality"
		}
	}
	if video != nil {
		t.SourceRange = video.DynamicRange
		if p.VideoAction == "copy" {
			t.BitDepth = video.BitDepth
			t.DynamicRange = effectiveRange(video, client.videoCodec(video.Codec), client)
			if video.DolbyVisionProfile == 7 && !profile7CopyThrough(video, client, p.OutputContainer) {
				// Without a declared profile 7 the repackage keeps the HDR10
				// base picture, or the checked 8.1 rewrite below.
				t.DynamicRange = assets.RangeHDR10
			}
			t.DolbyVisionProfile, t.DolbyVisionCompatibility = video.DolbyVisionProfile, video.DolbyVisionCompatibility
			t.CopyDolbyVision = t.DynamicRange == assets.RangeDolbyVision
			if p.OutputContainer == "fmp4_hls" && profile81CopyPicture(video, client, facts) != nil {
				t.DynamicRange, t.CopyDolbyVision = assets.RangeDolbyVision, true
				t.DolbyVisionConversion = decoder.DolbyVisionProfile7To81
			}
		} else {
			t.DolbyVisionProfile, t.DolbyVisionCompatibility = video.DolbyVisionProfile, video.DolbyVisionCompatibility
			boundConvertedPicture(p, video, client.videoCodec(p.OutputVideoCodec))
			t.Width, t.Height, t.FrameRate = p.TargetWidth, p.TargetHeight, p.MaxFrameRate
			w, h := convertedDimensions(*p)
			rate := video.FrameRate
			if t.FrameRate > 0 {
				rate = min(rate, t.FrameRate)
			}
			if p.OutputVideoCodec == "h264" {
				t.VideoLevel = decoder.H264OutputLevel(w, h, rate)
				t.VideoProfile = convertedH264Profile(client.videoCodec("h264"))
			}
			if p.OutputVideoCodec == "hevc" {
				t.VideoLevel = decoder.HEVCOutputLevel(w, h, rate)
			}
			// A picture kept in HDR10 keeps the source's mastering description,
			// so the display maps it as the original would have been mapped.
			if t.DynamicRange == assets.RangeHDR10 {
				t.MasterDisplay, t.MaxCLL = video.MasterDisplay, video.MaxCLL
			}
		}
	}
	if audio != nil {
		if p.AudioAction == "copy" {
			t.AudioChannels, t.AudioSampleRate, t.AudioBitrateBPS = audio.Channels, audio.SampleRate, 0
			if t.AudioCodec == "flac" {
				t.AudioBitrateBPS = 0
			}
		}
		if p.AudioAction == "copy" || t.AudioChannels == audio.Channels && (audio.Layout == "5.1" || audio.Layout == "5.1(side)" || audio.Layout == "7.1" || audio.Layout == "mono" || audio.Layout == "stereo") {
			t.AudioChannelLayout = audio.Layout
		}
		if t.AudioCodec == "flac" {
			t.AudioBitDepth = flacDepth(audio, client.audioCodec(t.AudioCodec))
			if sourceAudioDepth(audio) > t.AudioBitDepth {
				p.AudioLossless = false
			}
			if t.AudioSampleRate != audio.SampleRate && audio.SampleRate > 0 {
				p.AudioLossless = false
			}
		}
	}
	var err error
	t, err = t.Normalized()
	if err != nil {
		return err
	}
	p.ConversionTarget = &t
	p.ConversionToolchain = facts
	if t.DolbyVisionConversion == decoder.DolbyVisionProfile7To81 {
		p.OutputRange = assets.RangeDolbyVision
		if p.Trace != nil && p.Trace.Video != nil {
			p.Trace.Video.OutputRange = assets.RangeDolbyVision
			p.Trace.Notes = append(p.Trace.Notes, NoteDolbyVisionEnhancementDiscarded)
			if video.DolbyVisionEnhancementType == "FEL" {
				p.Trace.Notes = append(p.Trace.Notes, NoteDolbyVisionFELDiscarded)
			}
		}
	}
	if p.VideoAction == "convert" {
		probe = measuredConversionProbe(facts, probe, t.VideoCodec, t.BitDepth)
	}
	if probe.Available {
		copy := probe
		p.ConversionProbe = &copy
	}
	graph, err := p.conversionGraph(nil)
	if err != nil {
		return err
	}
	p.HardwareBackend = string(graph.Backend)
	if graph.Backend == decoder.BackendSoftware {
		p.HardwareNative = false
	}
	if p.VideoAction == "convert" {
		reasons := p.ReasonCodes[:0]
		for _, reason := range p.ReasonCodes {
			if reason != ReasonHardwareBackend && reason != ReasonSoftwareFallback {
				reasons = append(reasons, reason)
			}
		}
		p.ReasonCodes = reasons
		reason := ReasonHardwareBackend
		if graph.Backend == decoder.BackendSoftware {
			reason = ReasonSoftwareFallback
		}
		p.ReasonCodes = append(p.ReasonCodes, reason)
	}
	if graph.Backend == decoder.BackendSoftware {
		p.HardwareNative = false
		p.ConversionProbe = nil
	}
	return nil
}

func boundConvertedPicture(p *DeliveryPlan, v *SourceVideo, codec *ClientVideoCodec) {
	if codec == nil {
		return
	}
	point := codec.forConversion(p.OutputBitDepth, v.Width, v.Height, v.FrameRate)
	cap := &point
	// Missing facts cannot justify a level for an arbitrarily large picture.
	// Use a bounded conversion until a later scan supplies the source geometry.
	if v.Width <= 0 || v.Height <= 0 {
		if p.TargetWidth == 0 {
			p.TargetWidth = 1920
		}
		if p.TargetHeight == 0 {
			p.TargetHeight = 1080
		}
	}
	// Decoder bounds apply after autorotation and square-pixel scaling, just
	// like the dimensions advertised by the producer's master playlist.
	width, height := convertedDimensions(DeliveryPlan{Trace: &DecisionTrace{Video: &TraceVideo{Width: v.Width, Height: v.Height, Rotation: v.Rotation, SampleAspectRatio: v.SampleAspectRatio}}})
	if v.Width <= 0 || v.Height <= 0 {
		width, height = 1920, 1080
	}
	displayHeight := height
	if p.TargetHeight > 0 {
		height = min(height, p.TargetHeight)
	}
	if cap.MaxHeight > 0 {
		height = min(height, cap.MaxHeight)
	}
	if cap.MaxWidth > 0 {
		if p.TargetWidth == 0 || p.TargetWidth > cap.MaxWidth {
			p.TargetWidth = cap.MaxWidth
		}
		if width > cap.MaxWidth {
			height = min(height, displayHeight*cap.MaxWidth/width)
		}
	}
	rate := v.FrameRate
	if rate == 0 {
		if p.MaxFrameRate == 0 {
			p.MaxFrameRate = 60
		}
		rate = p.MaxFrameRate
	}
	if cap.MaxFrameRate > 0 && (rate == 0 || rate > cap.MaxFrameRate) {
		rate = cap.MaxFrameRate
		p.MaxFrameRate = rate
	}
	if cap.MaxLevel > 0 && (p.OutputVideoCodec == "h264" || p.OutputVideoCodec == "hevc") {
		for height > 2 {
			fit := *p
			fit.TargetHeight = height
			w, h := convertedDimensions(fit)
			level := decoder.H264OutputLevel(w, h, rate)
			if p.OutputVideoCodec == "hevc" {
				level = decoder.HEVCOutputLevel(w, h, rate)
			}
			if level <= cap.MaxLevel {
				break
			}
			height -= 2
		}
	}
	if height < displayHeight || p.TargetHeight > 0 || cap.MaxHeight > 0 {
		p.TargetHeight = height - height%2
	}
}

func baseCopyRejections(video *SourceVideo, client ClientProfile, facts *decoder.ToolchainFacts) []RouteRejection {
	// A device that declares this Dolby Vision profile receives the stream as
	// it is. Profile 7 is the exception handled by the caller: an untouched
	// stream needs fMP4, so this general route is the HDR10 fallback.
	if video == nil || video.DynamicRange != assets.RangeDolbyVision || video.DolbyVisionProfile != 7 && nativeDolbyVision(video, client.videoCodec(video.Codec), client) {
		return nil
	}
	if facts == nil || !facts.BitstreamFilters["dovi_rpu"] || video.DolbyVisionProfile == 7 && !facts.BitstreamFilters["filter_units"] {
		return []RouteRejection{{RejectServerOutput, "Dolby Vision base stripping unavailable"}}
	}
	return nil
}
