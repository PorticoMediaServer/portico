package assets

import (
	"context"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func canonicalMusicCodec(codec string) string {
	switch codec {
	case "musepack7":
		return "mpc7"
	case "musepack8":
		return "mpc8"
	}
	return codec
}

func (p Probe) completeMusepackTimeline(ctx context.Context, path string, f *Facts) error {
	if f.AudioCodec != "mpc7" && f.AudioCodec != "mpc8" {
		return nil
	}
	if strings.Contains(path, "://") {
		return audiofacts.ErrInconsistent
	}
	if p.ReadGuard != nil {
		if err := p.ReadGuard(ctx, path); err != nil {
			return err
		}
	}
	prefix, err := storage.ReadScanAudioPrefix(ctx, path)
	if err != nil {
		return err
	}
	h, err := audiofacts.ParseMusepackHeader(f.AudioCodec, prefix)
	if err != nil {
		return fmt.Errorf("invalid Musepack timeline: %w", err)
	}
	var matched bool
	for _, stream := range f.Streams {
		if stream.Type != "audio" {
			continue
		}
		if stream.Codec != h.Codec || stream.Detail.SampleRate != h.SampleRate || stream.Channels != h.Channels {
			return audiofacts.ErrInconsistent
		}
		matched = true
		break
	}
	if !matched {
		return audiofacts.ErrInconsistent
	}
	f.Duration = float64(h.DurationFrames) / float64(h.SampleRate)
	return nil
}
