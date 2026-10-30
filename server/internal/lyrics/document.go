// Package lyrics owns bounded lyric documents, not media clocks or subtitle tracks.
package lyrics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const MaxTextBytes = 256 << 10
const MaxLines = 4096
const MaxOffsetMS = 600000
const MaxTimestampMS = 24 * 60 * 60 * 1000

var (
	ErrInput       = errors.New("Invalid lyrics: use bounded UTF-8 or BOM-marked UTF-16 text, valid LRC timestamps and a language tag.")
	ErrConflict    = errors.New("Lyrics changed. Refresh before applying this change.")
	ErrSource      = errors.New("The song source changed or is not verified. Refresh playback or the source selection.")
	ErrUnavailable = errors.New("Lyric acquisition is unavailable. Check source access or provider configuration and retry.")
	ErrCapacity    = errors.New("The lyric resource or revision limit has been reached.")
)

type Line struct {
	AtMS *int64 `json:"atMs"`
	Text string `json:"text"`
}
type Document struct {
	Format           string `json:"format"`
	Text             string `json:"text"`
	Lines            []Line `json:"lines"`
	EmbeddedOffsetMS int64  `json:"embeddedOffsetMs"`
	SkippedLines     int    `json:"-"`
}
type Provenance struct {
	Origin     string `json:"origin"`
	ProviderID string `json:"providerId"`
	Label      string `json:"label"`
	Rights     string `json:"rights"`
}

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)
var timestampPattern = regexp.MustCompile(`^\[([0-9]{1,4}):([0-9]{2})(?:[.:]([0-9]{1,3}))?\]`)
var wordTimestampPattern = regexp.MustCompile(`<\d{1,4}:\d{2}(?:\.\d{1,3})?>`)
var metaPattern = regexp.MustCompile(`^\[(ar|al|ti|au|lr|by|re|tool|ve|length|la|lang|#):[^\[\]]*\]$`)

func Language(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "und"
	}
	if len(s) > 63 || !languagePattern.MatchString(s) {
		return "", ErrInput
	}
	return strings.ToLower(s), nil
}
func boundedText(s string, limit int) bool {
	return utf8.ValidString(s) && len(s) <= limit && strings.IndexFunc(s, func(r rune) bool { return (unicode.IsControl(r) && r != '\n' && r != '\t') || r == utf8.RuneError }) < 0
}

// Decode does not guess a legacy charset. Ill-formed UTF-16 and NUL/control bytes
// fail closed instead of silently changing a lyric's meaning.
func Decode(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > MaxTextBytes {
		return "", ErrInput
	}
	if len(raw) >= 2 && (raw[0] == 0xff && raw[1] == 0xfe || raw[0] == 0xfe && raw[1] == 0xff) {
		little := raw[0] == 0xff
		raw = raw[2:]
		if len(raw)%2 != 0 {
			return "", ErrInput
		}
		units := make([]uint16, 0, len(raw)/2)
		for i := 0; i < len(raw); i += 2 {
			u := uint16(raw[i])<<8 | uint16(raw[i+1])
			if little {
				u = uint16(raw[i+1])<<8 | uint16(raw[i])
			}
			units = append(units, u)
		}
		for i := 0; i < len(units); i++ {
			u := units[i]
			if u >= 0xd800 && u <= 0xdbff {
				if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return "", ErrInput
				}
				i++
			} else if u >= 0xdc00 && u <= 0xdfff {
				return "", ErrInput
			}
		}
		raw = []byte(string(utf16.Decode(units)))
	}
	text := strings.TrimPrefix(string(raw), "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if !boundedText(text, MaxTextBytes) || strings.TrimSpace(text) == "" {
		return "", ErrInput
	}
	return text, nil
}
func Parse(raw []byte, format string) (Document, error) {
	text, err := Decode(raw)
	if err != nil {
		return Document{}, err
	}
	if format != "lrc" && format != "text" {
		return Document{}, ErrInput
	}
	d := Document{Format: format, Text: text, Lines: []Line{}}
	offsetSeen := false
	hasWords := false
	if len(strings.Split(text, "\n")) > MaxLines+64 {
		return Document{}, ErrInput
	}
linesLoop:
	for _, line := range strings.Split(text, "\n") {
		if len(line) > 8192 {
			return Document{}, ErrInput
		}
		if format == "text" {
			d.Lines = append(d.Lines, Line{Text: line})
			hasWords = hasWords || strings.TrimSpace(line) != ""
		} else {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "[offset:") {
				if offsetSeen || !strings.HasSuffix(line, "]") {
					d.SkippedLines++
					continue
				}
				n, e := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(line, "[offset:"), "]"), 10, 64)
				if e != nil || n < -MaxOffsetMS || n > MaxOffsetMS {
					d.SkippedLines++
					continue
				}
				// LRC positive offsets advance lyrics. Normalize to Portico
				// timing intent, where a positive value delays presentation.
				d.EmbeddedOffsetMS = -n
				offsetSeen = true
				continue
			}
			if metaPattern.MatchString(line) {
				continue
			}
			var times []int64
			for {
				m := timestampPattern.FindStringSubmatch(line)
				if m == nil {
					break
				}
				minutes, _ := strconv.ParseInt(m[1], 10, 64)
				seconds, _ := strconv.ParseInt(m[2], 10, 64)
				if seconds >= 60 {
					d.SkippedLines++
					continue linesLoop
				}
				fraction := int64(0)
				if m[3] != "" {
					fraction, _ = strconv.ParseInt(m[3]+strings.Repeat("0", 3-len(m[3])), 10, 64)
				}
				n := (minutes*60+seconds)*1000 + fraction
				if n > MaxTimestampMS {
					d.SkippedLines++
					continue linesLoop
				}
				times = append(times, n)
				line = line[len(m[0]):]
				if len(times) > 64 {
					return Document{}, ErrInput
				}
			}
			// An LRC document cannot mix un-timed prose with timed lines. Markup is
			// retained as literal text; no client ever interprets it as HTML.
			if len(times) == 0 {
				d.SkippedLines++
				continue
			}
			line = wordTimestampPattern.ReplaceAllString(line, "")
			hasWords = hasWords || strings.TrimSpace(line) != ""
			for _, n := range times {
				at := n
				d.Lines = append(d.Lines, Line{AtMS: &at, Text: line})
			}
		}
		if len(d.Lines) > MaxLines {
			return Document{}, ErrInput
		}
	}
	if !hasWords || len(d.Lines) == 0 {
		return Document{}, ErrInput
	}
	if format == "lrc" {
		sort.SliceStable(d.Lines, func(i, j int) bool { return *d.Lines[i].AtMS < *d.Lines[j].AtMS })
		merged := make([]Line, 0, len(d.Lines))
		for _, line := range d.Lines {
			if len(merged) > 0 && *merged[len(merged)-1].AtMS == *line.AtMS {
				last := &merged[len(merged)-1]
				last.Text += "\n" + line.Text
				if len(last.Text) > 8192 {
					return Document{}, ErrInput
				}
			} else {
				merged = append(merged, line)
			}
		}
		d.Lines = merged
	}
	// Repeated timestamps expand input. Bound the normalized payload as well.
	bytes := 0
	for _, line := range d.Lines {
		bytes += len(line.Text)
		if bytes > MaxTextBytes {
			return Document{}, ErrInput
		}
	}
	return d, nil
}
func digest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func validateTiming(d Document, offset int64, sourceDuration float64) error {
	if offset < -MaxOffsetMS || offset > MaxOffsetMS {
		return ErrInput
	}
	if d.Format == "text" && offset != 0 {
		return ErrInput
	}
	for _, line := range d.Lines {
		if line.AtMS != nil && float64(*line.AtMS) > sourceDuration*1000+1000 {
			return ErrInput
		}
	}
	return nil
}
