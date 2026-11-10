package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// ItemSubtitle is one subtitle a title has: in the file or in a file beside it.
type ItemSubtitle struct {
	PlaybackSubtitle
	// VersionID is the version of the title the subtitle belongs to.
	VersionID string `json:"versionId"`
}

// ItemSubtitles lists a title's subtitles and the timing nudge this profile gave them.
type ItemSubtitles struct {
	Items    []ItemSubtitle `json:"items"`
	OffsetMs int64          `json:"offsetMs"`
}

// SubtitleOffset is a timing nudge in milliseconds: positive shows subtitles later.
type SubtitleOffset struct {
	OffsetMs int64 `json:"offsetMs"`
}

// LyricLine is one line of lyrics. AtMs is absent for lyrics that are not timed.
type LyricLine struct {
	AtMs *int64 `json:"atMs,omitempty"`
	Text string `json:"text"`
}

// Lyrics is one set of lyrics for a song.
type Lyrics struct {
	ID string `json:"id"`
	// Origin is where they came from. For now only sidecar: a file beside the song.
	Origin string `json:"origin"`
	// Format is lrc (timed) or text.
	Format string      `json:"format"`
	Text   string      `json:"text"`
	Lines  []LyricLine `json:"lines"`
	// OffsetMs is the timing offset written in the file itself.
	OffsetMs int64 `json:"offsetMs"`
}

func (s *Server) subtitleRoutes() {
	Handle(s, "GET /v1/items/{id}/subtitles", WithProfile, s.itemSubtitles)
	Handle(s, "PUT /v1/items/{id}/subtitles/offset", WithProfile, s.setSubtitleOffset)
	Handle(s, "GET /v1/items/{id}/lyrics", WithProfile, s.itemLyrics)
}

func (s *Server) itemSubtitles(r *http.Request, _ None) (ItemSubtitles, error) {
	c := CallerFrom(r.Context())
	item, err := s.db.PlaybackItem(r.Context(), db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}, r.PathValue("id"))
	if err != nil {
		return ItemSubtitles{}, playbackError(err)
	}
	out := ItemSubtitles{Items: []ItemSubtitle{}, OffsetMs: item.SubtitleOffsetMS}
	for _, version := range item.Versions {
		for _, subtitle := range versionOut(version).Subtitles {
			out.Items = append(out.Items, ItemSubtitle{PlaybackSubtitle: subtitle, VersionID: version.PublicID})
		}
	}
	return out, nil
}

// maxSubtitleOffset bounds a timing nudge: ten minutes either way is already another file.
const maxSubtitleOffset = 10 * 60 * 1000

func (s *Server) setSubtitleOffset(r *http.Request, req SubtitleOffset) (SubtitleOffset, error) {
	if req.OffsetMs < -maxSubtitleOffset || req.OffsetMs > maxSubtitleOffset {
		return SubtitleOffset{}, ErrField("offsetMs", "A timing nudge is at most ten minutes either way.")
	}
	c := CallerFrom(r.Context())
	err := s.db.SetSubtitleOffset(r.Context(), db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}, r.PathValue("id"), req.OffsetMs, s.now())
	return req, playbackError(err)
}

// maxLyrics is the most lyrics text one file may hold.
const maxLyrics = 256 << 10

var (
	lyricStamp  = regexp.MustCompile(`^\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)
	lyricOffset = regexp.MustCompile(`(?i)^\[offset:\s*([+-]?\d+)\]`)
	lyricTag    = regexp.MustCompile(`^\[[a-zA-Z]+:[^\]]*\]$`)
)

// parseLyrics reads LRC or plain text. A line with several time stamps is sung several times.
func parseLyrics(text string) (lines []LyricLine, offsetMs int64, timed bool) {
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if m := lyricOffset.FindStringSubmatch(line); m != nil {
			offsetMs, _ = strconv.ParseInt(m[1], 10, 64)
			continue
		}
		if lyricTag.MatchString(line) {
			continue // a header such as [ar:Artist]
		}
		var stamps []int64
		for {
			m := lyricStamp.FindStringSubmatch(line)
			if m == nil {
				break
			}
			minutes, _ := strconv.ParseInt(m[1], 10, 64)
			seconds, _ := strconv.ParseInt(m[2], 10, 64)
			fraction, _ := strconv.ParseInt((m[3] + "000")[:3], 10, 64)
			stamps = append(stamps, minutes*60_000+seconds*1000+fraction)
			line = strings.TrimSpace(line[len(m[0]):])
		}
		if len(stamps) == 0 {
			lines = append(lines, LyricLine{Text: line})
			continue
		}
		timed = true
		for _, at := range stamps {
			lines = append(lines, LyricLine{AtMs: &at, Text: line})
		}
	}
	if timed {
		// Untimed leftovers (a title line) stay first; timed lines play in order.
		ordered := make([]LyricLine, 0, len(lines))
		for _, l := range lines {
			if l.AtMs == nil && l.Text != "" {
				ordered = append(ordered, l)
			}
		}
		var sung []LyricLine
		for _, l := range lines {
			if l.AtMs != nil {
				sung = append(sung, l)
			}
		}
		for i := 1; i < len(sung); i++ {
			for j := i; j > 0 && *sung[j].AtMs < *sung[j-1].AtMs; j-- {
				sung[j], sung[j-1] = sung[j-1], sung[j]
			}
		}
		lines = append(ordered, sung...)
	}
	return lines, offsetMs, timed
}

// itemLyrics reads the lyrics files beside a song. Lyrics from inside the file, from a
// provider, or pasted by the owner arrive with the metadata work.
func (s *Server) itemLyrics(r *http.Request, _ None) (Page[Lyrics], error) {
	c := CallerFrom(r.Context())
	files, err := s.db.LyricFiles(r.Context(), db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}, r.PathValue("id"))
	if err != nil {
		return Page[Lyrics]{}, playbackError(err)
	}
	out := Page[Lyrics]{Items: []Lyrics{}}
	for _, file := range files {
		f, err := os.Open(file.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
				continue // gone since the last scan
			}
			return out, err
		}
		raw, err := io.ReadAll(io.LimitReader(f, maxLyrics))
		f.Close()
		if err != nil {
			return out, err
		}
		text := strings.ToValidUTF8(strings.TrimPrefix(string(raw), "\uFEFF"), "")
		lines, offset, timed := parseLyrics(text)
		lyrics := Lyrics{ID: "lyr" + strconv.FormatInt(file.FileID, 36), Origin: "sidecar", Format: "text", Text: text, Lines: lines, OffsetMs: offset}
		if timed {
			lyrics.Format = "lrc"
		}
		out.Items = append(out.Items, lyrics)
	}
	return out, nil
}
