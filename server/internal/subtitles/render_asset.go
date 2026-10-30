package subtitles

// Render assets are immutable, server-owned inputs to a confined producer. They
// are never interpreted as HTML or delivered to a native text overlay. Bitmap
// data is preserved, not OCR'd. Filenames below are generated, never user paths.
import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"regexp"
	"strconv"
	"strings"
)

// These hard limits also bound previously stored assets. New reads use the owner policy.
const MaxBinaryBytes = medialimits.MaxSubtitleFileMiB << 20
const MaxAssetBytes = MaxBinaryBytes * 3 / 2

func BinaryLimitBytes() int { return medialimits.SubtitleFileBytes() }
func AssetLimitBytes() int  { return medialimits.SubtitleAssetBytes() }

type RenderAsset struct {
	Version      int              `json:"version"`
	TimeDomain   string           `json:"timeDomain"`
	Renderer     string           `json:"renderer"`
	Format       string           `json:"format"`
	Data         []byte           `json:"data"`
	Companion    []byte           `json:"companion,omitempty"`
	Encoding     string           `json:"encoding,omitempty"`
	TextOnly     bool             `json:"textOnly,omitempty"`
	Authored     []byte           `json:"authored,omitempty"`
	SkippedFonts int              `json:"skippedFonts,omitempty"`
	Fonts        []FontAttachment `json:"fonts,omitempty"`
}

func DecodeRenderAsset(raw []byte) (RenderAsset, error) {
	var a RenderAsset
	if len(raw) > MaxAssetBytes || json.Unmarshal(raw, &a) != nil || a.Version != 2 || a.TimeDomain != "source-relative" || a.Renderer != "burn_in" || len(a.Data) == 0 {
		return a, ErrInput
	}
	if a.TextOnly {
		return a, ErrUnsupported
	}
	switch a.Format {
	case "ass", "ssa", "pgs", "vobsub", "dvb", "mks":
	default:
		return a, ErrUnsupported
	}
	return a, nil
}
func assetJSON(format string, data, companion []byte) ([]byte, error) {
	if len(data)+len(companion) > MaxBinaryBytes {
		return nil, ErrCapacity
	}
	return json.Marshal(RenderAsset{Version: 2, TimeDomain: "source-relative", Renderer: "burn_in", Format: format, Data: data, Companion: companion})
}
func assetRenderer(raw []byte) string {
	var h struct {
		Version  int  `json:"version"`
		TextOnly bool `json:"textOnly"`
	}
	if json.Unmarshal(raw, &h) == nil && h.Version == 2 && !h.TextOnly {
		return "burn_in"
	}
	return "external_text"
}

// CanonicalAsset supports text and bitmap uploads. The optional companion is
// the .sub packet file belonging to a VobSub .idx; neither file names another
// file. Matroska subtitle-only capsules are accepted only from our extractor.
func CanonicalAssetWithLanguage(raw, companion []byte, format string, durationUS int64, language string) ([]byte, error) {
	original := raw
	encoding := ""
	if textFormat(format) || format == "idx" || format == "vobsub" {
		decoded, enc, e := decodeSubtitleText(raw, language)
		encoding = enc
		if e != nil {
			return nil, e
		}
		raw = decoded
	}
	canonical, e := CanonicalAsset(raw, companion, format, durationUS)
	if e == nil && (format == "ass" || format == "ssa") {
		var a RenderAsset
		if json.Unmarshal(canonical, &a) == nil {
			a.Authored = original
			a.Encoding = encoding
			canonical, e = json.Marshal(a)
		}
	}
	return canonical, e
}

func CanonicalAsset(raw, companion []byte, format string, durationUS int64) ([]byte, error) {
	switch format {
	case "pgs", "sup":
		if len(companion) != 0 {
			return nil, ErrInput
		}
		if e := validatePGS(raw); e != nil {
			return nil, e
		}
		return assetJSON("pgs", raw, nil)
	case "vobsub", "idx":
		idx, e := decodeLoosely(raw)
		if e != nil {
			return nil, e
		}
		if len(companion) < 4 || len(companion) > MaxBinaryBytes || !bytes.Equal(companion[:4], []byte{0, 0, 1, 0xba}) {
			return nil, ErrText
		}
		if !bytes.Contains(idx, []byte("VobSub index file")) || !bytes.Contains(idx, []byte("timestamp:")) || bytes.Contains(idx, []byte{0}) {
			return nil, ErrText
		}
		// VobSub's parser has no external references besides the generated .sub
		// basename. Do not accept undocumented file/include directives.
		for _, l := range strings.Split(string(idx), "\n") {
			x := strings.ToLower(strings.TrimSpace(l))
			if strings.HasPrefix(x, "file:") || strings.HasPrefix(x, "include:") {
				return nil, ErrText
			}
			if strings.HasPrefix(x, "size:") {
				dims := strings.Split(strings.TrimSpace(strings.TrimPrefix(x, "size:")), "x")
				if len(dims) != 2 {
					return nil, ErrText
				}
				w, ew := strconv.Atoi(strings.TrimSpace(dims[0]))
				h, eh := strconv.Atoi(strings.TrimSpace(dims[1]))
				if ew != nil || eh != nil || w < 1 || h < 1 || w > 8192 || h > 4320 {
					return nil, ErrCapacity
				}
			}
		}
		return assetJSON("vobsub", idx, companion)
	case "ass", "ssa":
		if len(companion) != 0 {
			return nil, ErrInput
		}
		text, enc, e := decodeSubtitleText(raw, "")
		if e != nil {
			return nil, e
		}
		if e = validateASS(text, durationUS); e != nil {
			if e != ErrText {
				return nil, e
			}
			if _, err := ParseASSAsText(text, durationUS); err != nil {
				return nil, e
			}
			return json.Marshal(RenderAsset{Version: 2, TimeDomain: "source-relative", Renderer: "burn_in", Format: format, Data: text, Authored: raw, Encoding: enc, TextOnly: true})
		}
		out, e := assetJSON(format, text, nil)
		if e != nil {
			return nil, e
		}
		var a RenderAsset
		_ = json.Unmarshal(out, &a)
		a.Authored = raw
		a.Encoding = enc
		return json.Marshal(a)
	case "srt", "vtt", "smi", "sami", "ttml", "dfxp":
		if len(companion) != 0 {
			return nil, ErrInput
		}
		// Text stays text. These formats used to become a burn-in asset the moment
		// they carried an <i> tag, which turned ordinary subtitles into a full
		// re-encode of the film; see tolerant.go.
		return canonicalPlain(raw, format, durationUS)
	default:
		return nil, ErrUnsupported
	}
}
func validatePGS(raw []byte) error {
	if len(raw) == 0 || len(raw) > BinaryLimitBytes() {
		return ErrCapacity
	}
	cues := 0
	for len(raw) > 0 {
		if len(raw) < 13 || string(raw[:2]) != "PG" {
			return ErrText
		}
		n := int(binary.BigEndian.Uint16(raw[11:13]))
		if n > len(raw)-13 {
			return ErrText
		}
		switch raw[10] {
		case 0x14, 0x15, 0x16, 0x17, 0x80:
		default:
			return ErrText
		}
		if raw[10] == 0x16 {
			if n < 11 {
				return ErrText
			}
			w, h := int(binary.BigEndian.Uint16(raw[13:15])), int(binary.BigEndian.Uint16(raw[15:17]))
			if w < 1 || h < 1 || w > 8192 || h > 4320 {
				return ErrCapacity
			}
			cues++
		}
		if raw[10] == 0x15 && n >= 4 && raw[16]&0x80 != 0 {
			if n < 11 {
				return ErrText
			}
			w, h := int(binary.BigEndian.Uint16(raw[20:22])), int(binary.BigEndian.Uint16(raw[22:24]))
			if w < 1 || h < 1 || w > 8192 || h > 4320 {
				return ErrCapacity
			}
		}
		raw = raw[13+n:]
	}
	if cues == 0 || cues > MaxCues {
		return ErrCapacity
	}
	return nil
}
func validateASS(raw []byte, durationUS int64) error {
	lower := strings.ToLower(string(raw))
	if !strings.Contains(lower, "[script info]") || !strings.Contains(lower, "[events]") || !strings.Contains(lower, "dialogue:") {
		return ErrText
	}
	if strings.Contains(lower, "[fonts]") || strings.Contains(lower, "[graphics]") || strings.ContainsRune(lower, 0) {
		return ErrText
	}
	// Preserve script order and overlapping events. ASS layers need not be sorted.
	section := ""
	fields := []string{}
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if len(line) > MaxCueBytes*4 {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(line)
			continue
		}
		if section != "[events]" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "format":
			fields = strings.Split(strings.ToLower(value), ",")
			for i := range fields {
				fields[i] = strings.TrimSpace(fields[i])
			}
			if len(fields) < 3 || len(fields) > 32 || fields[len(fields)-1] != "text" {
				fields = nil
			}
		case "dialogue":
			if len(fields) == 0 {
				continue
			}
			values := strings.SplitN(value, ",", len(fields))
			if len(values) != len(fields) {
				continue
			}
			start, end := int64(-1), int64(-1)
			for i, f := range fields {
				if f == "start" || f == "end" {
					ms, e := assTimestamp(strings.TrimSpace(values[i]))
					if e != nil {
						continue
					}
					if f == "start" {
						start = ms
					} else {
						end = ms
					}
				}
			}
			if start < 0 || end <= start || end > MaxDurationMS || durationUS > 0 && end*1000 > durationUS+2000000 {
				continue
			}
			count++
			if count > MaxCues {
				return ErrCapacity
			}
		}
	}
	if count == 0 {
		return ErrText
	}
	return nil
}

var assTimestampPattern = regexp.MustCompile(`^([0-9]{1,2}):([0-9]{2}):([0-9]{2})\.([0-9]{1,3})$`)

func assTimestamp(v string) (int64, error) {
	m := assTimestampPattern.FindStringSubmatch(v)
	if m == nil {
		return 0, ErrTiming
	}
	h, _ := strconv.ParseInt(m[1], 10, 64)
	min, _ := strconv.ParseInt(m[2], 10, 64)
	sec, _ := strconv.ParseInt(m[3], 10, 64)
	fraction := m[4]
	for len(fraction) < 3 {
		fraction += "0"
	}
	cs, _ := strconv.ParseInt(fraction, 10, 64)
	if min > 59 || sec > 59 {
		return 0, ErrTiming
	}
	return ((h*60+min)*60+sec)*1000 + cs, nil
}
