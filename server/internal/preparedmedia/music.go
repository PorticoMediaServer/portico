package preparedmedia

import (
	"math"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// MusicProfile names immutable recipes independently from video recipes. The
// original recipe is only for an inventoried cue track that needs extraction.
func MusicProfile(id string) (quality, codec string, ok bool) {
	switch id {
	case "music-original-flac-v1":
		return "original", "flac", true
	case "music-cd-quality-v1":
		return "cd-quality", "flac", true
	case "music-opus-320-v1":
		return "320", "opus", true
	case "music-opus-96-v1":
		return "96", "opus", true
	case "music-aac-320-v1":
		return "320", "aac", true
	case "music-aac-96-v1":
		return "96", "aac", true
	}
	return "", "", false
}

func musicRecipe(p Profile) (decoder.PreparedRecipe, bool) {
	q, c, ok := MusicProfile(p.ID)
	if !ok {
		return decoder.PreparedRecipe{}, false
	}
	return decoder.PreparedRecipe{AudioOnly: true, MusicQuality: q, MusicCodec: c, Source: decoder.PreparedSource{AudioCodec: "flac", AudioSampleRate: 44100, AudioBitDepth: 16, AudioChannels: []int{2}}}, true
}

func acceptMusicOutput(original, output probed, p Profile) error {
	r, ok := musicRecipe(p)
	if !ok {
		return ErrOutput
	}
	r.Source = original.recipeSource()
	target, e := decoder.PreparedMusicFormat(r)
	if e != nil || output.VideoStreams != 0 || output.OtherStreams != 0 || output.AudioTracks != 1 || len(output.AudioChannels) != 1 || output.AudioChannels[0] != target.Channels || output.AudioCodec != target.Codec || output.AudioSampleRate != target.Rate {
		return ErrOutput
	}
	if target.Depth > 0 && output.AudioBitDepth != target.Depth {
		return ErrOutput
	}
	container := output.Container
	switch target.Container {
	case "m4a":
		if !strings.Contains(","+container+",", ",mov,") && !strings.Contains(","+container+",", ",mp4,") {
			return ErrOutput
		}
	default:
		if container != target.Container {
			return ErrOutput
		}
	}
	if math.Abs(original.Duration-output.Duration) > math.Max(0.15, original.Duration*0.001) {
		return ErrOutput
	}
	return nil
}
