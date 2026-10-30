package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// profile7Passthrough reports whether this device should receive profile 7 as
// it is in the file: the operating point that serves this picture declares
// profile 7, shows Dolby Vision and accepts the dvhe sample entry. A repackage
// must then keep the enhancement layer instead of stripping to the base picture
// (VIDEO §3 VS-1c).
func profile7Passthrough(video *SourceVideo, client ClientProfile) bool {
	// An untouched stream is the CopyDolbyVision target: HEVC, ten bits.
	if video == nil || video.Codec != "hevc" || video.BitDepth != 10 || video.DynamicRange != assets.RangeDolbyVision || video.DolbyVisionProfile != 7 {
		return false
	}
	c := client.videoCodec(video.Codec)
	if c == nil {
		return false
	}
	point, decoded := c.forStream(video.BitDepth, video.Width, video.Height, video.FrameRate)
	if !decoded {
		return false
	}
	c = &point
	// The nearest point is still the point that must take this picture.
	if len(c.BitDepths) > 0 && video.BitDepth > 0 && !containsInt(c.BitDepths, video.BitDepth) {
		return false
	}
	if !nativeDolbyVision(video, c, client) {
		return false
	}
	// The copy route does not consult the codec tag list, so an untouched
	// profile 7 stream must name the entry it is packaged as.
	if len(c.CodecTags) > 0 && !containsString(c.CodecTags, "dvhe") {
		return false
	}
	return true
}

// profile7CopyThrough reports whether this copy keeps profile 7 untouched. Only
// the fragmented MP4 packaging can carry it; every other transport keeps the
// HDR10 base picture.
func profile7CopyThrough(video *SourceVideo, client ClientProfile, container string) bool {
	return container == "fmp4_hls" && profile7Passthrough(video, client)
}

// profile81CopyPicture is a prospective delivery fact, never a replacement for
// source metadata. Both observed source layers and the selected packet-qualified
// bundle are required before evaluating the client's exact 8.1 eligibility.
func profile81CopyPicture(video *SourceVideo, client ClientProfile, facts *decoder.ToolchainFacts) *SourceVideo {
	if video == nil || video.Codec != "hevc" || video.BitDepth != 10 || video.DynamicRange != assets.RangeDolbyVision || video.DolbyVisionProfile != 7 || video.DolbyVisionCompatibility != 6 || !video.DolbyVisionMetadataKnown || !video.DolbyVisionRPU || !video.DolbyVisionBaseLayer || !video.EnhancementLayer {
		return nil
	}
	// A device that takes profile 7 gets it untouched; the rewrite is only for
	// devices that cannot take it themselves.
	if profile7Passthrough(video, client) {
		return nil
	}
	target := decoder.ConversionTarget{VideoCodec: "hevc", BitDepth: 10, SourceRange: assets.RangeDolbyVision, DynamicRange: assets.RangeDolbyVision, DolbyVisionProfile: 7, DolbyVisionCompatibility: 6, CopyDolbyVision: true, DolbyVisionConversion: decoder.DolbyVisionProfile7To81, Packaging: "fmp4_hls"}
	if _, err := decoder.DolbyVisionCopyBitstreamFilter(target, facts); err != nil {
		return nil
	}
	copy := *video
	copy.DolbyVisionProfile, copy.DolbyVisionCompatibility = target.DeliveredDolbyVision()
	copy.EnhancementLayer = false
	if !nativeDolbyVision(&copy, client.videoCodec(copy.Codec), client) {
		return nil
	}
	return &copy
}

func fmp4CopyTransports(transports []ClientTransport) []ClientTransport {
	out := []ClientTransport{}
	for _, transport := range transports {
		if transport.Transport == TransportHLSFMP4 {
			out = append(out, transport)
		}
	}
	return out
}

func bestSourceCopyTransport(transports []ClientTransport, client ClientProfile, video *SourceVideo, audio *SourceAudio, allowHDR bool, facts *decoder.ToolchainFacts) (string, []RouteRejection) {
	// An untouched profile 7 stream exists only in the fragmented MP4
	// packaging, so try that transport before any other.
	if allowHDR && profile7Passthrough(video, client) {
		if name, rejects := bestCopyTransport(fmp4CopyTransports(transports), client, video, audio, allowHDR, true); len(rejects) == 0 {
			return name, nil
		}
	}
	if delivered := profile81CopyPicture(video, client, facts); delivered != nil && allowHDR {
		if name, rejects := bestCopyTransport(fmp4CopyTransports(transports), client, delivered, audio, allowHDR, true); len(rejects) == 0 {
			return name, nil
		}
	}
	name, rejects := bestCopyTransport(transports, client, video, audio, allowHDR, true)
	return name, append(rejects, baseCopyRejections(video, client, facts)...)
}

func bestSourceAudioConversionTransport(transports []ClientTransport, client ClientProfile, video *SourceVideo, audio *SourceAudio, target QualityTarget, outputs ConversionOutputs, facts *decoder.ToolchainFacts) (string, []RouteRejection) {
	if target.AllowHDR && profile7Passthrough(video, client) {
		if name, rejects := bestAudioConversionTransport(fmp4CopyTransports(transports), client, video, audio, target, outputs); len(rejects) == 0 {
			return name, nil
		}
	}
	if delivered := profile81CopyPicture(video, client, facts); delivered != nil && target.AllowHDR {
		if name, rejects := bestAudioConversionTransport(fmp4CopyTransports(transports), client, delivered, audio, target, outputs); len(rejects) == 0 {
			return name, nil
		}
	}
	name, rejects := bestAudioConversionTransport(transports, client, video, audio, target, outputs)
	return name, append(rejects, baseCopyRejections(video, client, facts)...)
}

// deliveredDolbyVision is used only by HLS signalling. The trace continues to
// describe the source, including profile 7 after an actual 7 -> 8.1 rewrite.
func deliveredDolbyVision(p *DeliveryPlan) (profile, compatibility int) {
	if p == nil || p.Trace == nil || p.Trace.Video == nil || p.Trace.Video.OutputRange != assets.RangeDolbyVision {
		return 0, 0
	}
	if p.ConversionTarget != nil {
		return p.ConversionTarget.DeliveredDolbyVision()
	}
	return p.Trace.Video.DolbyVisionProfile, p.Trace.Video.DolbyVisionCompatibility
}
