package prepare

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitleoffer"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// A smaller copy carries no subtitle track: the encoder writes picture and sound only. So
// that someone watching offline keeps subtitles that were inside the file, each text subtitle
// of each of the version's files is written beside that part's copy as WebVTT. Subtitles made of
// pictures (PGS, VobSub, DVB) are not: they would have to be drawn into the picture.

// SubtitleFile is where a copy keeps the nth subtitle track (counting every subtitle track
// inside that part's file, in its order, from 0) of one part (from 1) once written out.
func (s *Service) SubtitleFile(p db.Prepared, part, n int) string {
	name := p.PublicID
	if part > 1 {
		name += "-" + strconv.Itoa(part)
	}
	return filepath.Join(s.Folder, name+".sub"+strconv.Itoa(n)+".vtt")
}

// TextSubtitle says whether a subtitle track's codec is text, which a copy carries beside it.
func TextSubtitle(codec string) bool {
	return subtitleoffer.TextFormat(codec) || subtitleoffer.TextFormat(subtitles.CodecFormat(codec))
}

// subtitleBundleMax bounds what one file's subtitles may take beside its copy, however many
// tracks it has: the bundle they are read into is refused past it.
const subtitleBundleMax = 64 << 20

// writeSubtitles writes out the text subtitles inside a part and returns the bytes written,
// which count in the copy's size. The streams the scanner recorded say which subtitle tracks
// are text, by place among the file's subtitle tracks, which is how they are taken from it.
// The file is read once for all of them, at background
// priority as the encode is, into a small bundle that each WebVTT file is then made from. A
// track that cannot be written out is left out: the copy still plays.
func (s *Service) writeSubtitles(ctx context.Context, p db.Prepared, part db.Part, number int) int64 {
	var text []int // places among the subtitle tracks
	place := 0
	for _, stream := range part.Streams {
		if stream.Kind != db.StreamSubtitle {
			continue
		}
		if TextSubtitle(stream.Codec) && len(text) < decoder.MaxBundledSubtitles {
			text = append(text, place)
		}
		place++
	}
	if len(text) == 0 {
		return 0
	}
	key := fmt.Sprintf("%s:%d", p.PublicID, number)
	warn := func(err error) {
		if ctx.Err() == nil {
			s.Log.Warn("the subtitles inside a file could not be written beside its copy", "copy", p.PublicID, "part", number, "error", err)
		}
	}
	bundle, err := s.bundle(ctx, part, text, key)
	if err != nil {
		warn(err)
		return 0
	}
	var written int64
	for k, n := range text {
		vtt, err := s.toVTT(ctx, bundle, k, fmt.Sprintf("%s:sub%d", key, n))
		if err == nil && len(vtt) == 0 {
			err = fmt.Errorf("the subtitle track is empty")
		}
		if err == nil {
			err = os.WriteFile(s.SubtitleFile(p, number, n), vtt, 0o600)
		}
		if err != nil {
			warn(err)
			continue
		}
		written += int64(len(vtt))
	}
	return written
}

// bundle reads a part's file once and returns its text subtitles at places (among the
// file's subtitle tracks, as the scanner recorded them), untouched, as one small Matroska file.
func (s *Service) bundle(ctx context.Context, part db.Part, places []int, key string) ([]byte, error) {
	source, size, _, err := s.OpenFile(ctx, part.FileID)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	bridge, err := inputbridge.Open(ctx, source, size)
	if err != nil {
		return nil, err
	}
	defer bridge.Close()
	reservation, err := bridge.Reserve()
	if err != nil {
		return nil, err
	}
	return decoder.RunSubtitleBundle(ctx, s.processes, key, s.FFmpeg, bridge.URL(), reservation, places, subtitleBundleMax)
}

// toVTT writes the kth track of a bundle as WebVTT. It reads only the bundle.
func (s *Service) toVTT(ctx context.Context, bundle []byte, k int, key string) ([]byte, error) {
	bridge, err := inputbridge.Open(ctx, bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return nil, err
	}
	defer bridge.Close()
	reservation, err := bridge.Reserve()
	if err != nil {
		return nil, err
	}
	return decoder.RunSubtitleToVTT(ctx, s.processes, key, s.FFmpeg, bridge.URL(), reservation, k)
}
