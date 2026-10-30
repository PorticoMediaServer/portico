package subtitles

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

type charsetCandidate struct {
	name, languages string
	encoding        encoding.Encoding
}

var subtitleCharsets = []charsetCandidate{
	{"windows-1252", "en fr de es pt it nl da sv no fi is", charmap.Windows1252},
	{"windows-1250", "pl cs sk hu ro hr sl bs sq", charmap.Windows1250},
	{"windows-1251", "ru uk be bg mk sr kk", charmap.Windows1251},
	{"windows-1253", "el", charmap.Windows1253}, {"windows-1254", "tr az", charmap.Windows1254},
	{"windows-1255", "he yi", charmap.Windows1255}, {"windows-1256", "ar fa ur", charmap.Windows1256},
	{"windows-1257", "et lv lt", charmap.Windows1257}, {"windows-1258", "vi", charmap.Windows1258},
	{"iso-8859-1", "en fr de es pt it", charmap.ISO8859_1}, {"iso-8859-2", "pl cs sk hu ro hr sl", charmap.ISO8859_2},
	{"iso-8859-3", "mt tr", charmap.ISO8859_3}, {"iso-8859-4", "et lv lt", charmap.ISO8859_4},
	{"iso-8859-5", "ru bg sr mk", charmap.ISO8859_5}, {"iso-8859-6", "ar", charmap.ISO8859_6},
	{"iso-8859-7", "el", charmap.ISO8859_7}, {"iso-8859-8", "he", charmap.ISO8859_8},
	{"iso-8859-9", "tr", charmap.ISO8859_9}, {"iso-8859-10", "is no sv fi", charmap.ISO8859_10},
	{"iso-8859-13", "lv lt et", charmap.ISO8859_13}, {"iso-8859-14", "ga cy", charmap.ISO8859_14},
	{"iso-8859-15", "en fr de es pt it", charmap.ISO8859_15}, {"iso-8859-16", "ro pl hu", charmap.ISO8859_16},
	{"koi8-r", "ru", charmap.KOI8R}, {"koi8-u", "uk", charmap.KOI8U},
	{"shift-jis", "ja", japanese.ShiftJIS}, {"euc-jp", "ja", japanese.EUCJP},
	{"gb18030", "zh", simplifiedchinese.GB18030}, {"gbk", "zh", simplifiedchinese.GBK},
	{"big5", "zh", traditionalchinese.Big5}, {"euc-kr", "ko", korean.EUCKR},
	{"windows-874", "th", charmap.Windows874},
}

// DecodeSubtitleText is deterministic and independent of the server's locale.
// Unicode takes precedence. For legacy files, score a bounded sample and use the
// track language as a prior; decoding tables come from the existing x/text module.
func DecodeSubtitleText(raw []byte, language string) ([]byte, error) {
	decoded, _, e := decodeSubtitleText(raw, language)
	return decoded, e
}
func decodeSubtitleText(raw []byte, language string) ([]byte, string, error) {
	if len(raw) == 0 || len(raw) > MaxInputBytes {
		return nil, "", ErrCapacity
	}
	bom := bytes.HasPrefix(raw, []byte{0xff, 0xfe}) || bytes.HasPrefix(raw, []byte{0xfe, 0xff})
	if bom {
		decoded, e := DecodeText(raw)
		enc := "utf-16le"
		if raw[0] == 0xfe {
			enc = "utf-16be"
		}
		return decoded, enc, e
	}
	// UTF-16 without a BOM commonly exposes ASCII timing in alternating bytes.
	if len(raw)%2 == 0 {
		even, odd := 0, 0
		sample := raw[:min(len(raw), 4096)]
		for i, b := range sample {
			if b == 0 {
				if i%2 == 0 {
					even++
				} else {
					odd++
				}
			}
		}
		if max(even, odd) > len(sample)/8 && min(even, odd) < max(even, odd)/4 {
			prefix := []byte{0xff, 0xfe}
			if even > odd {
				prefix = []byte{0xfe, 0xff}
			}
			decoded, e := DecodeText(append(prefix, raw...))
			enc := "utf-16le"
			if even > odd {
				enc = "utf-16be"
			}
			return decoded, enc, e
		}
	}
	if utf8.Valid(raw) {
		return bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), "utf-8", nil
	}
	hint := subtitleLanguageKey(language)
	sample := raw[:min(len(raw), 65536)]
	bestScore := -1e30
	best := subtitleCharsets[0]
	for _, c := range subtitleCharsets {
		decoded, e := c.encoding.NewDecoder().Bytes(sample)
		if e != nil {
			continue
		}
		score := charsetScore(string(decoded), hint)
		if hint != "" && strings.Contains(" "+c.languages+" ", " "+hint+" ") {
			score += 12
		}
		if hint == "zh" {
			if strings.Contains(strings.ToLower(language), "tw") || strings.Contains(strings.ToLower(language), "hant") {
				if c.name == "big5" {
					score += 15
				}
			} else if c.name == "gb18030" {
				score += 3
			}
		}
		if score > bestScore {
			bestScore = score
			best = c
		}
	}
	out, e := best.encoding.NewDecoder().Bytes(raw)
	if e != nil || !utf8.Valid(out) {
		return nil, "", ErrText
	}
	if len(out) > MaxInputBytes*3 {
		return nil, "", ErrCapacity
	}
	return out, best.name, nil
}

func charsetScore(s, hint string) float64 {
	score := 0.0
	significant := 0
	expected := func(r rune) bool {
		switch hint {
		case "ru", "uk", "be", "bg", "mk", "sr", "kk":
			return unicode.In(r, unicode.Cyrillic)
		case "ja":
			return unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han)
		case "zh":
			return unicode.In(r, unicode.Han)
		case "ko":
			return unicode.In(r, unicode.Hangul, unicode.Han)
		case "el":
			return unicode.In(r, unicode.Greek)
		case "ar", "fa", "ur":
			return unicode.In(r, unicode.Arabic)
		case "he", "yi":
			return unicode.In(r, unicode.Hebrew)
		case "th":
			return unicode.In(r, unicode.Thai)
		default:
			return unicode.In(r, unicode.Latin)
		}
	}
	lower := strings.ToLower(s)
	asciiLetters, cyrillic := 0, 0
	for _, r := range lower {
		if r < 128 && unicode.IsLetter(r) {
			asciiLetters++
		}
		if unicode.In(r, unicode.Cyrillic) {
			cyrillic++
		}
	}
	for _, r := range lower {
		if r < 128 {
			continue
		}
		significant++
		switch {
		case r == utf8.RuneError:
			score -= 40
		case unicode.IsControl(r), unicode.Is(unicode.Co, r):
			score -= 20
		case unicode.IsLetter(r):
			score += 1
			if hint != "" {
				if expected(r) {
					score += 4
				} else {
					score -= 4
				}
			}
			if unicode.In(r, unicode.Hiragana, unicode.Hangul) {
				score += 1
			}
			if unicode.In(r, unicode.Han) && strings.ContainsRune("的一是不了人我在有他这中大来上国个到说们为子和你地出道也时年得就那要下以生会自着去之过家学对可她里后小么心多天而能好都然没日于起还发成事只作当想看文无开手十用主行方又如前所本见经头面公同三已老从动两长知民样现分将外但身些与高意进把法此平美新情声明实才最感点很其请你好世界字幕这是日本語", r) {
				score += 1.5
			}
			if unicode.In(r, unicode.Hangul) && strings.ContainsRune("이다는에의을를하고서안녕한국어자막입니세", r) {
				score += 1
			}
			if strings.ContainsRune("оеаинтсрвлкмдпуяыьгзбчйхжшюцщэфё", r) {
				if hint != "" || cyrillic > asciiLetters/2 {
					score += 0.5
				} else {
					score -= 4
				}
			}
		case unicode.IsPunct(r):
			score += 0.1
		default:
			score -= 1
		}
	}
	// Frequent pairs distinguish Cyrillic encodings that all decode without errors.
	for _, pair := range []string{"ст", "но", "то", "на", "не", "ен", "ов", "ни", "ра", "во", "ко", "пр", "по", "ро", "го", "та", "ть", "это", "что", "です", "ます", "した", "ない"} {
		score += float64(strings.Count(lower, pair)) * 3
	}
	if significant == 0 {
		return 0
	}
	return score / float64(significant) * 10
}
