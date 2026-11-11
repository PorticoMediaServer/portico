package scan

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/disc"
)

// probeDisc reads a film disc kept as a folder (BDMV or VIDEO_TS) with the disc reader, which
// is confined and handed the folder itself, never its path. The disc is one title that plays
// its main feature: the longest title of at least a minute. A copy-protected disc is refused
// as one; a disc with no title long enough is not a film.
func probeDisc(ctx context.Context, folder string) (Facts, error) {
	dir, err := os.Open(folder)
	if err != nil {
		return Facts{}, err
	}
	defer dir.Close()
	found, err := disc.Tool{}.ProbeDescriptor(ctx, dir, true)
	var exit *exec.ExitError
	switch {
	case errors.Is(err, disc.ErrEncrypted):
		return Facts{}, ErrEncrypted
	case ctx.Err() != nil:
		return Facts{}, ctx.Err()
	case errors.As(err, &exit), errors.Is(err, disc.ErrProtocol):
		// The reader ran and could not make sense of the disc.
		return Facts{}, ErrDamaged
	case err != nil:
		// The reader could not be run (it is not installed, or there is no sandbox to run it
		// in): the disc keeps the title its folder gave it and is tried again next scan.
		return Facts{}, err
	}
	return discFacts(found)
}

// discCodecs names what a disc's stream coding numbers are.
var discCodecs = map[int]string{0x01: "mpeg1video", 0x02: "mpeg2video", 0x1b: "h264", 0x24: "hevc", 0xea: "vc1",
	0x03: "mp1", 0x04: "mp2", 0x80: "pcm_bluray", 0x81: "ac3", 0x82: "dts", 0x83: "truehd", 0x84: "eac3", 0x85: "dts", 0x86: "dts",
	0xa1: "eac3", 0xa2: "dts", 0x90: "hdmv_pgs_subtitle", 0x92: "hdmv_text_subtitle"}

// discFacts is the main feature of a disc as the scanner stores a file's facts: its length,
// its picture, sound and subtitle tracks with their languages, and its chapters.
func discFacts(found disc.Probe) (Facts, error) {
	if found.Encryption != "none" && found.Encryption != "" {
		return Facts{}, ErrEncrypted
	}
	main, ok := found.MainTitle()
	if !ok {
		return Facts{}, ErrNotMedia
	}
	facts := Facts{Container: found.Kind, DurationMS: main.DurationMs}
	first := -1
	for _, s := range main.Streams {
		if first == -1 {
			first = s.ClipSequence
		}
		// The first clip's tracks stand for the title: later clips repeat them.
		if s.ClipSequence != first || s.DolbyVisionEnhancementLayer {
			continue
		}
		stream := db.Stream{Codec: discCodecs[s.Coding], Language: s.Language}
		switch s.Kind {
		case "video":
			stream.Kind = db.StreamVideo
		case "audio":
			stream.Kind = db.StreamAudio
		case "subtitle":
			stream.Kind = db.StreamSubtitle
		default:
			continue
		}
		facts.Streams = append(facts.Streams, stream)
	}
	for n, chapter := range main.Chapters {
		end := main.DurationMs
		if n+1 < len(main.Chapters) {
			end = main.Chapters[n+1].StartMs
		}
		facts.Chapters = append(facts.Chapters, db.Marker{StartMS: chapter.StartMs, EndMS: end})
	}
	return facts, nil
}

// discOf is the disc folder a file stands for, when the file is the one the scanner lists for
// a disc (BDMV/index.bdmv, VIDEO_TS/VIDEO_TS.IFO).
func discOf(file string) (folder string, ok bool) {
	folder = filepath.Dir(file)
	return folder, discFolder(filepath.Base(folder)) && discTitle(filepath.Base(folder), filepath.Base(file))
}
