package streaming

import (
	"encoding/json"
)

// This envelope extends only the existing private plan_json. AudioPlan's public
// serialization and the audio-render DTO do not gain a second comparison port.
type storedAudioPlan struct {
	AudioPlan
	Comparison *PlaybackInfo `json:"comparison,omitempty"`
}

func decodeStoredAudioPlan(raw string, generation int) (*AudioPlan, error) {
	var stored storedAudioPlan
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, err
	}
	if stored.Generation != generation {
		return nil, nil
	}
	p := stored.AudioPlan
	p.comparison = stored.Comparison
	return &p, nil
}

// audioPlanComparison describes the pinned audio bytes, not the general VOD
// recipe or the renderer/receiver. Output, applied gain and sink rate remain
// separate observations. Only playable plans have a delivered comparison.
func audioPlanComparison(source *PlaybackSoundFacts, p *AudioPlan, remote, qualityLimited bool) *PlaybackInfo {
	if source == nil || p == nil || p.Version != 2 || (p.Mode != "direct" && p.Mode != "converted") || p.Codec == "" {
		return nil
	}
	if p.SampleRate <= 0 || p.SampleRate > 1536000 || p.Channels <= 0 || p.Channels > 128 || p.BitDepth < 0 || p.BitDepth > 64 {
		return nil
	}
	src := *source
	src.Lossless = copiedBool(source.Lossless)
	delivered := src
	delivered.Lossless = copiedBool(src.Lossless)
	route, reason := "original", "compatible"
	if p.Mode == "converted" {
		route = "converted"
		if qualityLimited {
			reason = "quality"
		} else if audioFamily(p.Codec) != audioFamily(src.Codec) {
			reason = "format"
		}
		delivered = PlaybackSoundFacts{Codec: p.Codec, SampleRate: p.SampleRate, Channels: p.Channels,
			BitDepth: p.BitDepth, Lossless: knownAudioEncodingLossless(p.Codec, "", "")}
	}
	info := &PlaybackInfo{Route: route, Routes: PlaybackRoutes{Sound: route}, Reason: PlaybackReason{Kind: reason},
		Source: PlaybackMediaFacts{Audio: &src}, Delivered: PlaybackMediaFacts{Audio: &delivered},
		Losses: []PlaybackLoss{}, Network: "home", Basis: "committed_delivery_plan"}
	if remote {
		info.Network = "away"
	}
	if p.Mode == "converted" && (src.ObjectAudio == "atmos" || src.ObjectAudio == "dtsx") {
		info.Losses = append(info.Losses, PlaybackLoss{Feature: src.ObjectAudio,
			Replacement: PlaybackReplacement{AudioCodec: delivered.Codec, Channels: delivered.Channels}})
	}
	return info
}
