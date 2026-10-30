package localmetadata

import (
	"bytes"
	"errors"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// localText decodes a small text sidecar (a cue sheet, desc.txt, reader.txt):
// UTF-8 with or without a byte-order mark, UTF-16 with one, and otherwise
// Windows-1252, the superset of Latin-1 that older rippers and taggers wrote.
func localText(raw []byte) (string, error) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		raw = raw[3:]
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}), bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		decoded, e := unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder().Bytes(raw)
		if e != nil {
			return "", e
		}
		raw = decoded
	}
	if !utf8.Valid(raw) {
		decoded, e := charmap.Windows1252.NewDecoder().Bytes(raw)
		if e != nil {
			return "", e
		}
		raw = decoded
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", errors.New("binary text sidecar")
	}
	return string(raw), nil
}

type cueTrack struct {
	title string
	start float64
}
type cueFile struct {
	name   string
	tracks []cueTrack
}

// parseCue reads a cue sheet's files and the tracks that start in each. Only
// what chapters need is kept: a track's title and where it starts in its file
// (INDEX 01, or INDEX 00 when a track has no 01), in mm:ss:ff at 75 frames a
// second. Unknown commands are ignored, as players do.
func parseCue(raw []byte) ([]cueFile, error) {
	if len(raw) == 0 || len(raw) > 256<<10 {
		return nil, errors.New("cue size")
	}
	text, e := localText(raw)
	if e != nil {
		return nil, e
	}
	var files []cueFile
	var track *cueTrack
	var pregap float64
	haveStart := false
	finish := func() {
		if track != nil && len(files) > 0 {
			if !haveStart {
				track.start = pregap
			}
			if haveStart || pregap >= 0 {
				last := &files[len(files)-1]
				last.tracks = append(last.tracks, *track)
			}
		}
		track, haveStart, pregap = nil, false, -1
	}
	pregap = -1
	lines := 0
	for _, line := range strings.Split(text, "\n") {
		if lines++; lines > 20000 {
			return nil, errors.New("cue budget")
		}
		command, rest := cueCommand(strings.TrimSpace(line))
		switch command {
		case "FILE":
			finish()
			name := cueValue(rest, true)
			if name == "" || len(files) >= 256 {
				return nil, errors.New("cue file")
			}
			files = append(files, cueFile{name: name})
		case "TRACK":
			finish()
			if len(files) == 0 {
				return nil, errors.New("cue track before file")
			}
			track = &cueTrack{}
		case "TITLE":
			if track != nil {
				track.title = assets.CleanAudioTag("title", cueValue(rest, false))
			}
		case "INDEX":
			if track == nil {
				continue
			}
			fields := strings.Fields(rest)
			if len(fields) != 2 {
				return nil, errors.New("cue index")
			}
			at, ok := cueTime(fields[1])
			if !ok {
				return nil, errors.New("cue time")
			}
			switch fields[0] {
			case "01", "1":
				track.start, haveStart = at, true
			case "00", "0":
				pregap = at
			}
		}
	}
	finish()
	tracks := 0
	for _, f := range files {
		tracks += len(f.tracks)
	}
	if tracks == 0 || tracks > 4096 {
		return nil, errors.New("cue tracks")
	}
	return files, nil
}

func cueCommand(line string) (string, string) {
	command, rest, _ := strings.Cut(line, " ")
	return strings.ToUpper(command), strings.TrimSpace(rest)
}

// cueValue is a quoted or bare value. FILE's value is followed by its type
// ("WAVE", "MP3"), which is not part of the name.
func cueValue(rest string, typed bool) string {
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return rest[1 : end+1]
		}
		return strings.Trim(rest, `"`)
	}
	if typed {
		if i := strings.LastIndex(rest, " "); i > 0 {
			return strings.TrimSpace(rest[:i])
		}
	}
	return rest
}

// cueTime is mm:ss:ff. Minutes may pass 99 in a long audiobook.
func cueTime(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	var n [3]int
	for i, p := range parts {
		v, e := strconv.Atoi(p)
		if e != nil || v < 0 || len(p) > 5 {
			return 0, false
		}
		n[i] = v
	}
	if n[1] > 59 || n[2] > 74 {
		return 0, false
	}
	return float64(n[0])*60 + float64(n[1]) + float64(n[2])/75, true
}

// cueChapters are the chapters a cue sheet gives one audio file: the tracks of
// the FILE entry that names it. A sheet named after the file that lists one
// FILE is that file's, whatever its FILE line says (a sheet written for the
// FLAC kept beside the M4B).
func cueChapters(files []cueFile, audio string, ownSheet bool) []assets.Chapter {
	base := strings.ToLower(path.Base(strings.ReplaceAll(audio, `\`, "/")))
	stem := strings.TrimSuffix(base, path.Ext(base))
	var chosen *cueFile
	for i := range files {
		name := strings.ToLower(path.Base(strings.ReplaceAll(files[i].name, `\`, "/")))
		if name == base {
			chosen = &files[i]
			break
		}
		if chosen == nil && strings.TrimSuffix(name, path.Ext(name)) == stem {
			chosen = &files[i]
		}
	}
	if chosen == nil && ownSheet && len(files) == 1 {
		chosen = &files[0]
	}
	if chosen == nil {
		return nil
	}
	out := make([]assets.Chapter, 0, len(chosen.tracks))
	for _, t := range chosen.tracks {
		out = append(out, assets.Chapter{Title: t.title, Start: t.start})
	}
	return out
}
