package enrich

import (
	"encoding/json"
	"unicode/utf8"
)

// encoding/json replaces malformed UTF-8 and unpaired escaped UTF-16
// surrogates. FIRST must refuse those strings, not silently normalize source
// data to replacement characters. This allocation-free extra pass runs only
// after JSON syntax validation; it does not build an unbounded generic tree.
func validTMDBJSON(body []byte) bool {
	if !utf8.Valid(body) || !json.Valid(body) {
		return false
	}
	quoted := false
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '"':
			quoted = !quoted
		case '\\':
			if !quoted {
				continue
			}
			i++ // Syntax validation proves the escape is present and complete.
			if body[i] != 'u' {
				continue
			}
			value := tmdbHexWord(body[i+1 : i+5])
			i += 4
			if value >= 0xdc00 && value <= 0xdfff {
				return false
			}
			if value < 0xd800 || value > 0xdbff {
				continue
			}
			if len(body)-i < 7 || body[i+1] != '\\' || body[i+2] != 'u' {
				return false
			}
			low := tmdbHexWord(body[i+3 : i+7])
			if low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func tmdbHexWord(raw []byte) uint16 {
	var value uint16
	for _, digit := range raw {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit - 'a' + 10)
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit - 'A' + 10)
		default:
			return 0 // Non-hex is reachable only in a malformed low pair.
		}
	}
	return value
}
