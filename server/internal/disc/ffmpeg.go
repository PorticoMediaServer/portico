package disc

import (
	"bytes"
	"fmt"
	"reflect"
	"strconv"
)

// Input describes input-scoped FFmpeg options. It grants no sandbox access;
// the caller must retain its source/version fence and exactly one ReadPath.
type Input struct {
	Options           []string
	URL               string
	TimestampOffsetMs int64
}

func (p Probe) FFmpegInput(path string, t Title, positionMs int64, seek *Seek) (Input, error) {
	if !cleanLocalPath(path) || p.checkTitleInput(t, positionMs) != nil {
		return Input{}, ErrProtocol
	}
	return p.ffmpegTitleInput(path, t, positionMs, seek)
}

func (p Probe) checkTitleInput(t Title, positionMs int64) error {
	if p.Schema != Schema || p.Encryption != "none" || positionMs < 0 || positionMs >= t.DurationMs || p.Kind != "bluray" && p.Kind != "dvd" {
		return ErrProtocol
	}
	// A valid-looking playlist/title number is not authority to play it. Keep
	// the exact selected title tied to this probe, including its observed
	// duration, clips, tracks and chapters. The caller still owns the live
	// source/version fence before and after probe/seek and at FFmpeg launch.
	matches := 0
	for _, observed := range p.Titles {
		if observed.Key(p.Kind) == t.Key(p.Kind) {
			if !reflect.DeepEqual(observed, t) {
				return ErrProtocol
			}
			matches++
		}
	}
	if matches != 1 {
		return ErrProtocol
	}
	return nil
}

func (p Probe) ffmpegTitleInput(path string, t Title, positionMs int64, seek *Seek) (Input, error) {
	switch p.Kind {
	case "bluray":
		if t.Playlist == nil {
			return Input{}, ErrProtocol
		}
		args := []string{"-protocol_whitelist", "file,pipe,bluray", "-format_whitelist", "mpegts,ffmetadata", "-min_playlist_length", "60", "-playlist", strconv.Itoa(*t.Playlist), "-angle", "0", "-f", "mpegts"}
		offset := int64(0)
		if positionMs > 0 {
			if seek == nil || seek.Schema != Schema || seek.Playlist != *t.Playlist || seek.RequestedMs != positionMs || seek.ByteOffset < 0 || seek.TitleTimeMs < 0 || seek.TitleTimeMs > positionMs {
				return Input{}, ErrProtocol
			}
			args = append(args, "-skip_initial_bytes", strconv.FormatInt(seek.ByteOffset, 10))
			offset = seek.TitleTimeMs
		}
		return Input{Options: args, URL: "bluray:" + path, TimestampOffsetMs: offset}, nil
	case "dvd":
		if t.Title < 1 {
			return Input{}, ErrProtocol
		}
		// dvdvideo opens its MPEG-PS child with the same format whitelist.
		// Keep this additional demuxer confined to the selected DVD job.
		args := []string{"-protocol_whitelist", "file,pipe", "-format_whitelist", "dvdvideo,mpeg,ffmetadata", "-f", "dvdvideo", "-title", strconv.Itoa(t.Title), "-angle", "1", "-preindex", "0"}
		if positionMs > 0 {
			args = append(args, "-ss", strconv.FormatFloat(float64(positionMs)/1000, 'f', 3, 64))
		}
		return Input{Options: args, URL: path, TimestampOffsetMs: positionMs}, nil
	}
	return Input{}, ErrProtocol
}

// ChapterMetadata is fed as an inherited ffmetadata pipe, never a library-path
// sidecar. A chapter's title is ordinal; provider or playlist names cannot
// introduce ffmetadata syntax or disclose local files.
func (t Title) ChapterMetadata() ([]byte, error) {
	if t.DurationMs < 60000 || len(t.Chapters) > 65535 {
		return nil, ErrProtocol
	}
	var out bytes.Buffer
	out.WriteString(";FFMETADATA1\n")
	for i, c := range t.Chapters {
		end := t.DurationMs
		if i+1 < len(t.Chapters) {
			end = t.Chapters[i+1].StartMs
		}
		if c.StartMs < 0 || end <= c.StartMs || end > t.DurationMs {
			return nil, ErrProtocol
		}
		fmt.Fprintf(&out, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=Chapter %d\n", c.StartMs, end, i+1)
		if out.Len() > MaxOutputBytes {
			return nil, ErrProtocol
		}
	}
	return out.Bytes(), nil
}

// LanguagesForPID retains language for TrueHD and its AC-3 presentation, which
// can share the same transport PID. Contradicting clip facts stay unknown.
func (t Title) LanguageForPID(pid int) string {
	value := ""
	for _, s := range t.Streams {
		if s.PID != pid || s.Language == "" {
			continue
		}
		if value != "" && value != s.Language {
			return ""
		}
		value = s.Language
	}
	return value
}
func (t Title) HasUnjoinedDolbyVision() bool {
	for _, s := range t.Streams {
		if s.DolbyVisionEnhancementKnown && s.DolbyVisionEnhancementLayer {
			return true
		}
	}
	return false
}
