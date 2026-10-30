package playbackv1

import (
	"context"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// audioChoice prepares an audio presentation's version 2 plan (spec §18.1):
// the device's audioDecode, and the asset's facts, measured now (bounded) when
// the background backfill hasn't reached it yet. Other kinds are untouched.
func (s *Service) audioChoice(ctx context.Context, c Caller, item, kind string, choice *playback.V1Choice) error {
	if kind != "song" && kind != "track" && kind != "audiobook_file" {
		return nil
	}
	choice.AudioPlanV2 = true
	doc := s.deviceDocument(ctx, c.DeviceID)
	choice.AudioDecode = doc.DecodeCaps()
	// Receivers use their ordinary, declared media decoder. They do not have a
	// client gain stage, so the server pins a stream with gain already applied.
	if len(choice.AudioDecode) == 0 && (doc.Client.Family == "cast" || doc.Client.Family == "roku") {
		choice.AudioServerRender = true
		p := doc.Profile(playback.EngineNative)
		for _, a := range p.Audio {
			containers := []string{}
			for _, t := range p.Transports {
				if t.Transport == playback.TransportDirect && slices.Contains(t.Audio, a.Codec) {
					containers = append(containers, t.Containers...)
				}
			}
			if slices.Contains(containers, "aac") {
				containers = append(containers, "adts")
			}
			choice.AudioDecode = append(choice.AudioDecode, playback.AudioDecodeCap{Codec: a.Codec, Containers: containers, MaxChannels: a.MaxChannels, MaxSampleRate: a.MaxSampleRate, SampleRates: a.SampleRates, MaxBitDepth: a.MaxBitDepth})
		}
	}
	asset, err := s.Playback.SelectedAsset(ctx, item, choice)
	if err != nil {
		return nil // the start itself reports a missing version
	}
	budget := s.AudioMeasureBudget
	if budget <= 0 {
		budget = 5 * time.Second
	}
	_, _, err = audiofacts.Ensure(ctx, s.DB, s.MeasureAudio, item, asset, budget)
	return err
}
