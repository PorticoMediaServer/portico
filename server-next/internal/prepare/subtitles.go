package prepare

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitleoffer"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// A smaller copy carries no subtitle track: the encoder writes picture and sound only. So
// that someone watching offline keeps subtitles that were inside the file, each text subtitle
// of the version's first file is written beside the copy as WebVTT. Subtitles made of
// pictures (PGS, VobSub, DVB) are not: they would have to be drawn into the picture.

// SubtitleFile is where a copy keeps the nth subtitle track (counting every subtitle track
// inside the version's first file, in its order, from 0) once written out.
func (s *Service) SubtitleFile(p db.Prepared, n int) string {
	return filepath.Join(s.Folder, p.PublicID+".sub"+strconv.Itoa(n)+".vtt")
}

// TextSubtitle says whether a subtitle track's codec is text, which a copy carries beside it.
func TextSubtitle(codec string) bool {
	return subtitleoffer.TextFormat(codec) || subtitleoffer.TextFormat(subtitles.CodecFormat(codec))
}

// writeSubtitles writes out the text subtitles inside a part. The streams the scanner
// recorded say which subtitle tracks are text; a fresh reading of the file gives each one's
// number in the file, matched by its place among the subtitle tracks. A track that cannot be
// written out is left out: the copy still plays.
func (s *Service) writeSubtitles(ctx context.Context, p db.Prepared, part db.Part) {
	var text []int // places among the subtitle tracks
	place := 0
	for _, stream := range part.Streams {
		if stream.Kind != db.StreamSubtitle {
			continue
		}
		if TextSubtitle(stream.Codec) {
			text = append(text, place)
		}
		place++
	}
	if len(text) == 0 {
		return
	}
	source, size, _, err := s.OpenFile(ctx, part.FileID)
	if err != nil {
		return
	}
	defer source.Close()
	facts, err := inputbridge.Inspect(ctx, s.processes, mediatools.Resolve("ffprobe"), source, size)
	if err != nil {
		s.Log.Warn("the subtitles inside a file could not be read for its copy", "copy", p.PublicID, "error", err)
		return
	}
	var numbers []int // each subtitle track's number in the file, in order
	for _, stream := range facts.Streams {
		if stream.Type == "subtitle" {
			numbers = append(numbers, stream.Index)
		}
	}
	for _, n := range text {
		if n >= len(numbers) {
			continue
		}
		if err := s.writeSubtitle(ctx, p, source, size, n, numbers[n]); err != nil && ctx.Err() == nil {
			s.Log.Warn("a subtitle inside a file could not be written beside its copy", "copy", p.PublicID, "track", numbers[n], "error", err)
		}
	}
}

func (s *Service) writeSubtitle(ctx context.Context, p db.Prepared, source Source, size int64, n, number int) error {
	bridge, err := inputbridge.Open(ctx, source, size)
	if err != nil {
		return err
	}
	defer bridge.Close()
	reservation, err := bridge.Reserve()
	if err != nil {
		return err
	}
	vtt, err := decoder.RunSubtitleExtract(ctx, s.processes, fmt.Sprintf("%s:sub%d", p.PublicID, n), s.FFmpeg, bridge.URL(), reservation, number, "vtt")
	if err != nil {
		return err
	}
	if len(vtt) == 0 {
		return fmt.Errorf("the subtitle track is empty")
	}
	return os.WriteFile(s.SubtitleFile(p, n), vtt, 0o600)
}
