package subtitlevideo

import (
	"context"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// ConvertAudio streams an audio asset converted exactly (spec §18.1 converted
// mode): the same fenced input and confined FFmpeg as analysis, with a closed
// recipe (decoder.AnalysisSpec kinds audio_flac, audio_opus and audio_aac).
func (r *Runtime) ConvertAudio(ctx context.Context, item, asset string, spec decoder.AnalysisSpec, out io.Writer) error {
	if spec.Kind != "audio_flac" && spec.Kind != "audio_opus" && spec.Kind != "audio_aac" {
		return subtitles.ErrRendererConfiguration
	}
	input, e := r.OpenSubtitleInput(ctx, item, asset, "")
	if e != nil {
		return e
	}
	defer input.Close()
	return r.AnalysisDecoder(input)(ctx, identity.Token(), spec, func(stream io.Reader) error {
		r.activeAudioConversions.Add(1)
		defer r.activeAudioConversions.Add(-1)
		_, err := io.Copy(out, stream)
		return err
	})
}

// ActiveAudioConversions counts running output producers, excluding planning,
// input acquisition and queued work. These closed audio recipes use software.
func (r *Runtime) ActiveAudioConversions() int64 {
	return r.activeAudioConversions.Load()
}
