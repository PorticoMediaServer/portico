package playbackruntime

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

type linearProbeStream struct {
	FieldOrder    string `json:"field_order"`
	Level         int    `json:"level"`
	FrameRate     string `json:"avg_frame_rate"`
	RealFrameRate string `json:"r_frame_rate"`
	Kind          string `json:"codec_type"`
	Codec         string `json:"codec_name"`
	Profile       string `json:"profile"`
	PixelFormat   string `json:"pix_fmt"`
	Transfer      string `json:"color_transfer"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Channels      int    `json:"channels"`
	SampleRate    string `json:"sample_rate"`
	Layout        string `json:"channel_layout"`
	Bitrate       string `json:"bit_rate"`
	AudioDepth    string `json:"bits_per_raw_sample"`
	videoDetail   *assets.StreamDetail
}

func (s *linearProbeStream) UnmarshalJSON(raw []byte) error {
	type plain linearProbeStream
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if decoded.Kind == "video" {
		detail, err := assets.VideoStreamDetailFromProbeJSON(raw)
		if err != nil {
			return err
		}
		decoded.videoDetail = &detail
	}
	*s = linearProbeStream(decoded)
	return nil
}

// Plan from probed elementary streams and the admitted engine's declarations.
// A conversion must produce a supported tuple, not merely a supported codec.
func linearPlan(raw []byte, w playback.LinearWork, packaging playback.LinearPackaging) (decoder.LinearEncoding, error) {
	plan, _, err := linearEnginePlan(raw, w, packaging, "")
	return plan, err
}

// linearEnginePlan also says which of the device's engines the stream is made
// for; pinned keeps the engine a buffer is already played with.
func linearEnginePlan(raw []byte, w playback.LinearWork, packaging playback.LinearPackaging, pinned string) (decoder.LinearEncoding, string, error) {
	engine := playback.EngineNative
	var facts struct {
		Streams []linearProbeStream `json:"streams"`
		Format  struct {
			Bitrate string `json:"bit_rate"`
		} `json:"format"`
	}
	var plan decoder.LinearEncoding
	if len(raw) > decoder.MaxProbeOutputBytes || json.Unmarshal(raw, &facts) != nil || len(facts.Streams) == 0 || len(facts.Streams) > 128 {
		return plan, engine, channelFault("source_probe_invalid", 422)
	}
	var video, audio *linearProbeStream
	for i := range facts.Streams {
		s := &facts.Streams[i]
		if s.Kind == "video" && video == nil {
			video = s
		}
		if s.Kind == "audio" && audio == nil {
			audio = s
		}
	}
	if video == nil && audio == nil {
		return plan, engine, channelFault("source_has_no_playable_stream", 422)
	}
	client := w.ClientProfile
	if client.Version == 0 {
		client = playback.BaselineClientProfile()
	}
	transportFor := func(client playback.ClientProfile) bool {
		return slices.ContainsFunc(client.Transports, func(t playback.ClientTransport) bool {
			return t.Transport == playback.TransportHLSTS && packaging != playback.LinearFragmentedOnly || t.Transport == playback.TransportHLSFMP4 && packaging != playback.LinearTSOnly
		})
	}
	if !transportFor(client) && (w.MPVProfile == nil || !transportFor(*w.MPVProfile)) {
		return plan, engine, channelFault("engine_transport_unavailable", 422)
	}
	sourceVideo := linearVideoSource(video)
	var sourceAudio *playback.SourceAudio
	if audio != nil {
		rate, _ := strconv.Atoi(audio.SampleRate)
		depth, _ := strconv.Atoi(audio.AudioDepth)
		bitrate, _ := strconv.ParseInt(audio.Bitrate, 10, 64)
		sourceAudio = &playback.SourceAudio{Index: 1, Codec: audio.Codec, Channels: audio.Channels, Layout: audio.Layout, StreamDetail: assets.StreamDetail{Profile: audio.Profile, SampleRate: rate, BitRate: bitrate, BitDepth: depth}}
	}
	if sourceVideo != nil && (sourceVideo.Width < 1 || sourceVideo.Height < 1 || sourceVideo.Width > 32768 || sourceVideo.Height > 32768) {
		return plan, engine, channelFault("source_video_invalid", 422)
	}
	source := playback.DeliverySource{Container: "mpegts", Video: sourceVideo, DetailKnown: false}
	if sourceAudio != nil {
		source.Audio = []playback.SourceAudio{*sourceAudio}
	}
	source.BitRate, _ = strconv.ParseInt(facts.Format.Bitrate, 10, 64)
	engines := playback.LinearEngines{Native: client, MPV: w.MPVProfile, Setting: w.EngineSetting}
	selected, engine, err := playback.PlanLinearEngines(source, engines, w.Selection.Quality, w.TranscodingEnabled, packaging, pinned)
	if err != nil {
		return plan, engine, channelFault("engine_codec_unavailable", 422)
	}
	plan.Fragmented = selected.OutputContainer == "fmp4_hls"
	plan.AudioCodec = selected.OutputAudioCodec
	plan.CopyAAC = plan.Fragmented && audio != nil && audio.Codec == "aac" && selected.AudioAction == "copy"
	plan.CopyHEVC = sourceVideo != nil && sourceVideo.Codec == "hevc" && selected.VideoAction == "copy"
	if plan.CopyHEVC && selected.OutputRange == assets.RangeDolbyVision {
		plan.CopyDolbyVisionProfile = sourceVideo.DolbyVisionProfile
		if selected.ConversionTarget != nil {
			plan.CopyDolbyVisionProfile, _ = selected.ConversionTarget.DeliveredDolbyVision()
		}
	}
	q := w.Selection.Quality
	plan.Video, plan.Audio = video != nil, audio != nil
	plan.ConvertVideo, plan.ConvertAudio = selected.VideoAction == "convert", selected.AudioAction == "convert"
	if (plan.ConvertVideo || plan.ConvertAudio) && !q.AllowLossy && (plan.ConvertVideo || !selected.AudioLossless) {
		return plan, engine, channelFault("channel_quality_requires_original", 422)
	}
	if selected.ToneMap && !q.AllowHDRToSDR {
		return plan, engine, channelFault("hdr_conversion_not_allowed", 422)
	}
	plan.HDRToSDR, plan.Deinterlace, plan.MaxFrameRate = selected.ToneMap, selected.Deinterlace, selected.MaxFrameRate
	plan.MaxHeight, plan.VideoBitrate = selected.TargetHeight, selected.VideoBitrateBPS
	plan.Target = selected.ConversionTarget
	if plan.Target != nil {
		plan.Level = plan.Target.VideoLevel
	}
	return plan, engine, nil
}

func linearVideoSource(video *linearProbeStream) *playback.SourceVideo {
	if video == nil {
		return nil
	}
	if video.videoDetail != nil {
		return &playback.SourceVideo{Codec: video.Codec, StreamDetail: *video.videoDetail}
	}
	fps, numerator, denominator := assets.VideoProbeFrameRate(video.FrameRate, video.RealFrameRate)
	bitrate, _ := strconv.ParseInt(video.Bitrate, 10, 64)
	interlaced, analysis := assets.VideoProbeInterlace(video.FieldOrder)
	return &playback.SourceVideo{Codec: video.Codec, StreamDetail: assets.StreamDetail{
		BitRate: bitrate, Profile: video.Profile, Level: video.Level,
		Width: video.Width, Height: video.Height,
		BitDepth:  assets.VideoProbeBitDepth(video.AudioDepth, video.PixelFormat),
		FrameRate: fps, DynamicRange: assets.DynamicRangeOf(video.Transfer, 0),
		Interlaced: interlaced, StreamBreadth: assets.StreamBreadth{InterlaceAnalysis: analysis, FrameRateNumerator: numerator, FrameRateDenominator: denominator},
	}}
}
