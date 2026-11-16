package streaming

import (
	"context"
	"io"

	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// readPictureOriginal keeps bitmap packets and their source timestamps. A
// VobSub sidecar is a pair of inventoried files; embedded DVD packets are
// rewrapped from the decoder's single-track Matroska capsule without OCR.
func (s *Streams) readPictureOriginal(ctx context.Context, st *stream, track TextRenditionPlan) (data, companion []byte, err error) {
	kind := subtitles.RenderingFormat(track.Format)
	if track.Path != "" {
		source := track.Source
		if source == nil {
			source = libraryFile{path: track.Path, size: track.Size, modified: track.Modified, roots: s.libraries}
		}
		data, err = readPictureSource(ctx, source)
		if err != nil || kind != "vobsub" {
			return data, nil, err
		}
		source = track.Companion
		if source == nil {
			return nil, nil, ErrSourceUnavailable
		}
		companion, err = readPictureSource(ctx, source)
		return data, companion, err
	}
	format, mux := kind, "matroska"
	if kind == "pgs" {
		mux = "sup"
	}
	if source, ok := st.source.(remoteFile); ok {
		data, err = s.remoteSubtitleOriginal(ctx, source, track.Track, format)
	} else {
		data, err = s.extractTrack(ctx, st.source, track.Track, subtitles.BinaryLimitBytes(), "-copyts", "-start_at_zero", "-c:s", "copy", "-f", mux)
	}
	if err != nil || kind != "vobsub" {
		return data, nil, err
	}
	return subtitles.VobSubOriginal(data)
}

func readPictureSource(ctx context.Context, source Source) ([]byte, error) {
	file, _, _, err := source.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	limit := subtitles.BinaryLimitBytes()
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err == nil && len(raw) > limit {
		err = subtitles.ErrCapacity
	}
	return raw, err
}
