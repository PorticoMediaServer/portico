package playback

// traceVideoSource captures the actual selected source before any target graph
// changes it. The stored trace survives later catalogue refreshes, so a session
// report need not join today's file facts onto yesterday's presentation.
func traceVideoSource(video *SourceVideo) *TraceVideo {
	if video == nil {
		return nil
	}
	out := &TraceVideo{
		Rotation: video.Rotation, SampleAspectRatio: video.SampleAspectRatio,
		Codec: video.Codec, Profile: video.Profile, Level: video.Level,
		BitDepth: video.BitDepth, Width: video.Width, Height: video.Height,
		FrameRate: video.FrameRate, Interlaced: video.ObservedInterlaced(),
		DynamicRange: video.DynamicRange, DolbyVisionProfile: video.DolbyVisionProfile,
		DolbyVisionLevel: video.DolbyVisionLevel, DolbyVisionCompatibility: video.DolbyVisionCompatibility,
		BitRate: video.BitRate, ChromaSubsampling: video.ChromaSubsampling,
		FrameRateNumerator: video.FrameRateNumerator, FrameRateDenominator: video.FrameRateDenominator,
		FrameRateMode: video.FrameRateMode, DolbyVisionEnhancementType: video.DolbyVisionEnhancementType,
		DolbyVisionCMVersion: video.DolbyVisionCMVersion, HDR10Plus: video.HDR10Plus,
		MasterDisplay: video.MasterDisplay, MaxCLL: video.MaxCLL,
	}
	if video.DolbyVisionMetadataKnown {
		rpu, base := video.DolbyVisionRPU, video.DolbyVisionBaseLayer
		out.DolbyVisionRPU, out.DolbyVisionBaseLayer = &rpu, &base
	}
	return out
}
