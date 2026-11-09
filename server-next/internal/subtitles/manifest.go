package subtitles

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
)

// ManifestWebVTT uses the title clock. Cues crossing a segment edge are repeated
// with their original timestamps, as required by HLS WebVTT. Empty windows are
// valid and retain the timestamp map. Markup comes only from structured flags.
func ManifestWebVTT(reader io.Reader, offset int64, segment int) ([]byte, error) {
	if segment < -1 || segment > 1440 {
		return nil, ErrInput
	}
	raw, e := io.ReadAll(io.LimitReader(reader, MaxInputBytes*2+1))
	if e != nil || len(raw) > MaxInputBytes*2 {
		return nil, ErrCapacity
	}
	var d Document
	if json.Unmarshal(raw, &d) != nil || len(d.Cues) > MaxCues {
		return nil, ErrInput
	}
	var b strings.Builder
	b.WriteString("WEBVTT\nX-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:0\n\n")
	stamp := func(us int64) string {
		ms := us / 1000
		return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
	}
	for i, c := range d.Cues {
		start, e := strconv.ParseInt(c.StartUS, 10, 64)
		if e != nil {
			return nil, ErrInput
		}
		end, e := strconv.ParseInt(c.EndUS, 10, 64)
		if e != nil {
			return nil, ErrInput
		}
		start += offset
		end += offset
		if end <= 0 || segment >= 0 && (end <= int64(segment)*60000000 || start >= int64(segment+1)*60000000) {
			continue
		}
		start = max(int64(0), start)
		if end <= start {
			return nil, ErrInput
		}
		text := html.EscapeString(c.Text)
		if c.Italic {
			text = "<i>" + text + "</i>"
		}
		placement := ""
		if c.Top {
			placement = " line:10%"
		}
		fmt.Fprintf(&b, "%d\n%s --> %s%s\n%s\n\n", i+1, stamp(start), stamp(end), placement, text)
		if b.Len() > MaxInputBytes*2 {
			return nil, ErrCapacity
		}
	}
	return []byte(b.String()), nil
}

// The ordinal suffix disambiguates duplicate human labels in AVFoundation/CAF.
func ManifestSubtitleName(title, language string, ordinal int) string {
	title = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, title)
	if title == "" {
		title = language + " · Subtitles"
	}
	return fmt.Sprintf("%s · %d", title, ordinal+1)
}
