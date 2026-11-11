package subtitles

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitleoffer"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	ErrConflict              = errors.New("subtitle revision changed; refresh before applying this change")
	ErrInput                 = errors.New("invalid subtitle request")
	ErrUnavailable           = errors.New("subtitle source or provider is unavailable")
	ErrRendererConfiguration = errors.New("subtitle renderer requires configured FFmpeg and FFprobe executables")
	ErrUnsupported           = errors.New("this subtitle format or timing cannot be rendered on this playback path")
	ErrOperation             = errors.New("subtitle operation identity was already used with different content")
)

const MaxOffsetUS int64 = 10 * 60 * 1000000

// Decimal microseconds on the wire; milliseconds are only the SRT/VTT parser's
// input precision. This document has no independent running clock or markup.
type TextCue struct {
	StartUS string `json:"startUs"`
	EndUS   string `json:"endUs"`
	Text    string `json:"text"`
	// Italic and Top are the two presentation facts every player can honour.
	// They are omitted when false, so a plain document is unchanged on the wire.
	Italic bool `json:"italic,omitempty"`
	Top    bool `json:"top,omitempty"`
}
type Document struct {
	SkippedCues int       `json:"skippedCues,omitempty"`
	Version     int       `json:"version"`
	TimeDomain  string    `json:"timeDomain"`
	Cues        []TextCue `json:"cues"`
}

func textField(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func Language(s string) (string, error) {
	v, ok := subtitleoffer.Language(s)
	if !ok {
		return "", ErrInput
	}
	return v, nil
}
func Offset(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || strconv.FormatInt(n, 10) != s || n < -MaxOffsetUS || n > MaxOffsetUS {
		return 0, ErrInput
	}
	return n, nil
}
func digestBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// Only explicit Unicode encodings are admitted. No locale-dependent guessing.
func DecodeText(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > MaxInputBytes {
		return nil, ErrCapacity
	}
	if bytes.HasPrefix(raw, []byte{0xff, 0xfe}) || bytes.HasPrefix(raw, []byte{0xfe, 0xff}) {
		little := raw[0] == 0xff
		raw = raw[2:]
		if len(raw)%2 != 0 {
			return nil, ErrText
		}
		units := make([]uint16, len(raw)/2)
		for i := range units {
			if little {
				units[i] = binary.LittleEndian.Uint16(raw[2*i:])
			} else {
				units[i] = binary.BigEndian.Uint16(raw[2*i:])
			}
		}
		for i := 0; i < len(units); i++ {
			u := units[i]
			if u >= 0xd800 && u <= 0xdbff {
				if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return nil, ErrText
				}
				i++
			} else if u >= 0xdc00 && u <= 0xdfff {
				return nil, ErrText
			}
		}
		raw = []byte(string(utf16.Decode(units)))
	}
	if len(raw) > MaxInputBytes || !utf8.Valid(raw) {
		return nil, ErrText
	}
	return raw, nil
}
func canonicalPlain(raw []byte, format string, durationUS int64) ([]byte, error) {
	cues, e := ParseTolerant(raw, format, durationUS)
	if e != nil {
		return nil, e
	}
	out, e := textDocument(cues)
	if e != nil {
		return nil, e
	}
	var d Document
	_ = json.Unmarshal(out, &d)
	decoded, _ := decodeLoosely(raw)
	total := strings.Count(string(decoded), "-->")
	if format == "smi" || format == "sami" {
		total = len(samiSync.FindAllIndex(decoded, -1))
	}
	if format == "ttml" || format == "dfxp" {
		total = len(ttmlStart.FindAllIndex(decoded, -1))
	}
	d.SkippedCues = max(0, total-len(cues))
	return json.Marshal(d)
}

// textDocument is the canonical stored form of a text subtitle.
func textDocument(cues []StyledCue) ([]byte, error) {
	d := Document{Version: 1, TimeDomain: "source-relative", Cues: make([]TextCue, 0, len(cues))}
	for _, c := range cues {
		d.Cues = append(d.Cues, TextCue{StartUS: strconv.FormatInt(c.StartMS*1000, 10), EndUS: strconv.FormatInt(c.EndMS*1000, 10), Text: c.Text, Italic: c.Italic, Top: c.Top})
	}
	out, e := json.Marshal(d)
	if e != nil {
		return nil, e
	}
	if len(out) > MaxInputBytes*2 {
		return nil, ErrCapacity
	}
	return out, nil
}

// TextDocument reads any text subtitle (SRT, WebVTT, ASS, SSA, SAMI, TTML) into
// the plain cues a player draws itself: styling beyond italics and "at the top"
// is dropped.
func TextDocument(raw []byte, format string, durationUS int64) ([]byte, error) {
	parse := func() ([]StyledCue, error) { return ParseTolerant(raw, format, durationUS) }
	if format == "ass" || format == "ssa" {
		parse = func() ([]StyledCue, error) { return ParseASSAsText(raw, durationUS) }
	}
	cues, err := parse()
	if err != nil {
		return nil, err
	}
	return textDocument(cues)
}

// TextAsASS writes a text subtitle that is not ASS (SRT, WebVTT, SAMI, TTML) as a plain
// ASS script, which is the form the converter draws into a picture. It keeps what
// the plain cues keep: the words, italics and "at the top".
func TextAsASS(raw []byte, format string, durationUS int64) ([]byte, error) {
	cues, err := ParseTolerant(raw, format, durationUS)
	if err != nil {
		return nil, err
	}
	stamp := func(ms int64) string {
		return fmt.Sprintf("%d:%02d:%02d.%02d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000/10)
	}
	var b strings.Builder
	b.WriteString("[Script Info]\nScriptType: v4.00+\nPlayResX: 384\nPlayResY: 288\nScaledBorderAndShadow: yes\n\n[V4+ Styles]\n" +
		"Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, " +
		"ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n" +
		"Style: Default,Arial,16,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1\n\n[Events]\n" +
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	for _, cue := range cues {
		// Braces start an override in ASS; a cue's own are only ever words.
		text := strings.NewReplacer("{", "(", "}", ")", "\n", "\\N").Replace(cue.Text)
		if cue.Italic {
			text = "{\\i1}" + text
		}
		if cue.Top {
			text = "{\\an8}" + text
		}
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", stamp(cue.StartMS), stamp(cue.EndMS), text)
	}
	return []byte(b.String()), nil
}
func Canonical(raw []byte, format string, durationUS int64) ([]byte, error) {
	return CanonicalAsset(raw, nil, format, durationUS)
}
func validFormat(s string) bool { return subtitleoffer.ValidFormat(s) }
func safeTitle(s string) string { return subtitleoffer.Title(s) }
