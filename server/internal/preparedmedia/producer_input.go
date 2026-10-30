package preparedmedia

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
)

// Linux with a capable bubblewrap uses an fd-bound, read-only mount with no
// network namespace route. Everywhere else jobs read a per-process loopback
// endpoint (sandboxed by sandbox-exec or bubblewrap where available, the
// baseline otherwise). Neither grants a decoder access to an original source
// path or provider URL.
type producerInput struct {
	bridge   *subtitlevideo.MediaBridge
	file     *os.File
	evidence string
}

func (p producerInput) Evidence() string {
	if p.bridge != nil {
		return p.bridge.Evidence()
	}
	return p.evidence
}
func (s *Service) probe(ctx context.Context, w work, custody *attemptCustody, p producerInput) (probed, error) {
	var raw []byte
	var e error
	if p.file != nil {
		raw, e = decoder.RunPreparedFileProbe(ctx, s.supervisor, w.ID, s.sandbox, s.ffprobe, p.file, custody.sandbox, s.libraries...)
	} else {
		e = p.bridge.With(ctx, func(url string, r *decoder.EndpointReservation) error {
			var err error
			raw, err = decoder.RunPreparedProbe(ctx, s.supervisor, w.ID, s.ffprobe, url, r, custody.file, s.libraries...)
			return err
		})
	}
	if e != nil {
		return probed{}, e
	}
	observed, err := parseProbeMode(raw, true)
	musepack := observed.AudioCodec == "mpc7" || observed.AudioCodec == "mpc8"
	if err != nil || observed.Duration > 0 && !musepack {
		return observed, err
	}
	var measured audiofacts.Facts
	if p.file != nil {
		measured, err = decoder.RunPreparedFileAudioFacts(ctx, s.supervisor, w.ID, s.sandbox, s.ffprobe, p.file, custody.sandbox, s.libraries...)
	} else {
		err = p.bridge.With(ctx, func(url string, r *decoder.EndpointReservation) error {
			var e error
			measured, e = decoder.RunPreparedAudioFacts(ctx, s.supervisor, w.ID, s.ffprobe, url, r, custody.file, s.libraries...)
			return e
		})
	}
	if err != nil {
		return probed{}, err
	}
	if musepack {
		var prefix []byte
		if p.file != nil {
			var info os.FileInfo
			info, err = p.file.Stat()
			if err == nil {
				prefix = make([]byte, min(info.Size(), int64(1<<20)))
				_, err = p.file.ReadAt(prefix, 0)
			}
		} else {
			prefix, err = p.bridge.HeaderPrefix(ctx)
		}
		if err == nil {
			measured, err = audiofacts.CompleteCodecHeaders(measured, prefix)
		}
		if err != nil {
			return probed{}, err
		}
	}
	return measuredMusicProbe(observed, measured)
}

func measuredMusicProbe(observed probed, measured audiofacts.Facts) (probed, error) {
	if measured.DurationFrames <= 0 || measured.SampleRate != observed.AudioSampleRate || measured.Channels != observed.AudioChannelCount || measured.Codec != observed.AudioCodec || measured.BitDepth != observed.AudioBitDepth {
		return probed{}, ErrOutput
	}
	observed.Duration = float64(measured.DurationFrames) / float64(measured.SampleRate)
	return observed, nil
}
func (s *Service) encode(ctx context.Context, w work, custody *attemptCustody, p producerInput, r decoder.PreparedRecipe, out io.Writer) error {
	if p.file != nil {
		return decoder.RunPreparedFileEncode(ctx, s.supervisor, w.ID, s.sandbox, s.ffmpeg, p.file, custody.sandbox, r, out, s.libraries...)
	}
	return p.bridge.With(ctx, func(url string, res *decoder.EndpointReservation) error {
		return decoder.RunPreparedEncode(ctx, s.supervisor, w.ID, s.ffmpeg, url, res, custody.file, r, out, s.libraries...)
	})
}
func (s *Service) validateOutput(ctx context.Context, w work, custody *attemptCustody, p producerInput) error {
	if p.file != nil {
		return decoder.RunPreparedFileValidate(ctx, s.supervisor, w.ID, s.sandbox, s.ffmpeg, p.file, custody.sandbox, s.libraries...)
	}
	return p.bridge.With(ctx, func(url string, res *decoder.EndpointReservation) error {
		return decoder.RunPreparedValidate(ctx, s.supervisor, w.ID, s.ffmpeg, url, res, custody.file, s.libraries...)
	})
}
func inputKey(id string, generation int64) string {
	return fmt.Sprintf("%s.%d", hash("prepared-input:"+id), generation)
}
func (s *Service) removeAttempt(id string, generation int64) error {
	if e := s.artifacts.RemoveRetained(fmt.Sprintf("%s.%d", id, generation)); e != nil {
		return e
	}
	return s.artifacts.RemoveRetained(inputKey(id, generation))
}

// The retained source acquisition validates extents/revision. A complete spool
// is its own observation, never promoted into an origin-supplied strong version.
func (s *Service) spool(ctx context.Context, w work, input subtitles.RenderInput) (*mediaartifact.Writer, mediaartifact.Object, error) {
	var object mediaartifact.Object
	size := input.Size()
	if size <= 0 || size > s.maxBytes {
		return nil, object, ErrUnsupported
	}
	if e := s.phase(ctx, w, "reading source"); e != nil {
		return nil, object, e
	}
	writer, e := s.artifacts.BeginRetained(inputKey(w.ID, w.Generation), s.maxBytes)
	if e != nil {
		return nil, object, e
	}
	ok := false
	defer func() {
		if !ok {
			writer.Abort()
		}
	}()
	progress := &progressWriter{s: s, w: w, out: writer, ctx: ctx}
	for offset := int64(0); offset < size; {
		if e = ctx.Err(); e != nil {
			return nil, object, e
		}
		count := min(int64(1<<20), size-offset)
		b, err := input.ReadExtent(ctx, offset, count)
		if err != nil {
			return nil, object, err
		}
		if int64(len(b)) != count {
			return nil, object, io.ErrUnexpectedEOF
		}
		if _, e = progress.Write(b); e != nil {
			return nil, object, e
		}
		offset += count
	}
	if e = input.Validate(ctx); e != nil {
		return nil, object, e
	}
	object, e = writer.Checkpoint()
	if e != nil {
		return nil, object, e
	}
	ok = true
	return writer, object, nil
}
func (s *Service) outputInput(reader io.ReaderAt, size int64) (producerInput, error) {
	if s.fileSandbox() {
		file, ok := reader.(*os.File)
		if !ok {
			return producerInput{}, ErrConfiguration
		}
		return producerInput{file: file}, nil
	}
	return producerInput{bridge: subtitlevideo.NewMediaBridge(&closedInput{reader: reader, size: size})}, nil
}
