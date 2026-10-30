package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
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

// audioComparisonSource runs in the accepting AudioPlan writer. The format is
// the audio renderer's own fresh measurement, enriched only by a current,
// unambiguous single stream of that same asset. Old metadata never supplies an
// alternate/default track, and a session read never joins today's catalogue.
func audioComparisonSource(ctx context.Context, tx *sql.Tx, asset string, measured audiofacts.Facts) (*PlaybackSoundFacts, error) {
	source, err := loadDeliverySource(ctx, tx, asset)
	if err != nil {
		return nil, err
	}
	if !source.DetailKnown || len(source.Audio) != 1 || audioFamily(source.Audio[0].Codec) != audioFamily(measured.Codec) {
		return nil, nil
	}
	track := source.Audio[0]
	out := &PlaybackSoundFacts{Codec: measured.Codec, Profile: publicAudioText(measured.Profile, 96), Variant: track.AudioVariant,
		ObjectAudio: track.ObjectAudio, Lossless: copiedBool(track.Lossless)}
	if out.Lossless == nil {
		out.Lossless = knownAudioEncodingLossless(measured.Codec, measured.Profile, track.AudioVariant)
	}
	// Keep the existing comparison wire bounds. Unsupported source rates stay
	// absent; a DSD clock/multiple is never derived from a decoder's sample rate.
	if measured.SampleRate > 0 && measured.SampleRate <= 1536000 {
		out.SampleRate = measured.SampleRate
	}
	if measured.Channels > 0 && measured.Channels <= 128 {
		out.Channels = measured.Channels
	}
	if measured.BitDepth > 0 && measured.BitDepth <= 64 {
		out.BitDepth = measured.BitDepth
	}
	if track.Channels == measured.Channels {
		out.Layout = publicAudioText(track.Layout, 64)
	}
	return out, nil
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

// ReadAudioPlaybackInfoTx uses the original authorized V1 session snapshot and
// its committed audio-render target. A replaced media generation, target drift,
// unavailable plan or legacy plan without captured source evidence is unknown.
func ReadAudioPlaybackInfoTx(ctx context.Context, tx *sql.Tx, session string, generation int, expected *AudioPlan) (*PlaybackInfo, error) {
	if tx == nil || session == "" || generation < 1 || expected == nil || expected.Version != 2 || expected.ID != session+"."+itoa(generation) {
		return nil, nil
	}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT a.plan_json FROM playback_audio_plans a JOIN playback_sessions s ON s.id=a.session_id
 WHERE a.session_id=? AND s.generation=? AND s.state NOT IN('stopped','ended','failed')`, session, generation).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p, err := decodeStoredAudioPlan(raw, generation)
	if err != nil || p == nil {
		return nil, err
	}
	if p.Version != 2 || p.ID != expected.ID || p.Mode != expected.Mode || p.Codec != expected.Codec || p.SampleRate != expected.SampleRate || p.Channels != expected.Channels || p.BitDepth != expected.BitDepth {
		return nil, nil
	}
	info := p.comparison
	if info == nil || info.Source.Audio == nil || info.Delivered.Audio == nil || info.Delivered.Audio.Codec != p.Codec || (p.Mode != "direct" && p.Mode != "converted") {
		return nil, nil
	}
	route := "original"
	if p.Mode == "converted" {
		route = "converted"
	}
	if info.Basis != "committed_delivery_plan" || info.Route != route || info.Routes.Sound != route || info.Delivered.Audio.SampleRate != p.SampleRate || info.Delivered.Audio.Channels != p.Channels || info.Delivered.Audio.BitDepth != p.BitDepth {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return info, nil
}
