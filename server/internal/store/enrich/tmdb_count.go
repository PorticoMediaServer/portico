package enrich

import (
	"bytes"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// countTMDBMovieCredits borrows ONLY complete, immutable bytes already checked
// by validTMDBJSON. It locates the credits/cast/crew spans and counts values
// without decoding strings, copying RawMessages or allocating candidate rows.
// The existing typed decoder still validates every candidate after preflight.
func countTMDBMovieCredits(body []byte) (tmdbMovieCounts, error) {
	root, ok := tmdbObjectSpans(body)
	if !ok {
		return tmdbMovieCounts{}, ErrTMDBMovie
	}
	var count tmdbMovieCounts
	seenCredits := false
	for key, value, more := root.next(); more; key, value, more = root.next() {
		if !tmdbASCIIKey(key, "credits") {
			continue
		}
		if seenCredits {
			return tmdbMovieCounts{}, ErrTMDBMovie
		}
		seenCredits = true
		credits, ok := tmdbObjectSpans(value)
		if !ok {
			return tmdbMovieCounts{}, ErrTMDBMovie
		}
		seen := uint8(0)
		for key, value, more := credits.next(); more; key, value, more = credits.next() {
			bit := uint8(0)
			switch {
			case tmdbASCIIKey(key, "cast"):
				bit = 1
			case tmdbASCIIKey(key, "crew"):
				bit = 2
			default:
				continue
			}
			if seen&bit != 0 {
				return tmdbMovieCounts{}, ErrTMDBMovie
			}
			seen |= bit
			if len(value) < 2 || value[0] != '[' || value[len(value)-1] != ']' {
				return tmdbMovieCounts{}, ErrTMDBMovie
			}
			n := 0
			for at := 1; at < len(value)-1; {
				at = tmdbSpanSpace(value, at)
				if value[at] == ']' {
					break
				}
				if count.cast+count.crew+n >= writer.MaxScreenCreditCandidates {
					return tmdbMovieCounts{}, ErrTMDBMovie
				}
				at = tmdbValueEnd(value, at)
				n++
				at = tmdbSpanSpace(value, at)
				if value[at] == ',' {
					at++
				}
			}
			if bit == 1 {
				count.cast = n
			} else {
				count.crew = n
			}
		}
		if seen != 3 {
			return tmdbMovieCounts{}, ErrTMDBMovie
		}
	}
	if !seenCredits {
		return tmdbMovieCounts{}, ErrTMDBMovie
	}
	return count, nil
}

type tmdbSpanObject struct {
	body []byte
	at   int
}

func tmdbObjectSpans(body []byte) (tmdbSpanObject, bool) {
	body = bytes.TrimSpace(body)
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return tmdbSpanObject{}, false
	}
	return tmdbSpanObject{body: body, at: 1}, true
}

// next returns borrowed key/value spans. JSON syntax/Unicode validation has
// already proved the quotes, colon, commas and matching container boundaries.
func (o *tmdbSpanObject) next() (key, value []byte, more bool) {
	at := tmdbSpanSpace(o.body, o.at)
	if o.body[at] == '}' {
		return nil, nil, false
	}
	end := tmdbValueEnd(o.body, at)
	key = o.body[at:end]
	at = tmdbSpanSpace(o.body, end) + 1 // colon
	at = tmdbSpanSpace(o.body, at)
	end = tmdbValueEnd(o.body, at)
	value = o.body[at:end]
	o.at = tmdbSpanSpace(o.body, end)
	if o.body[o.at] == ',' {
		o.at++
	}
	return key, value, true
}

func tmdbSpanSpace(body []byte, at int) int {
	for at < len(body) {
		switch body[at] {
		case ' ', '\n', '\r', '\t':
			at++
		default:
			return at
		}
	}
	return at
}

// Skip a validated value with an iterative nesting counter. Quotes and escaped
// quotes are skipped as units, so braces/commas in candidate text are inert.
func tmdbValueEnd(body []byte, at int) int {
	if body[at] == '"' {
		for at++; at < len(body); at++ {
			switch body[at] {
			case '\\':
				at++
			case '"':
				return at + 1
			}
		}
		return len(body)
	}
	if body[at] == '{' || body[at] == '[' {
		depth := 0
		for at < len(body) {
			switch body[at] {
			case '"':
				at = tmdbValueEnd(body, at)
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return at + 1
				}
			}
			at++
		}
		return at
	}
	for at < len(body) {
		switch body[at] {
		case ' ', '\n', '\r', '\t', ',', '}', ']':
			return at
		}
		at++
	}
	return at
}

// All selected field names are ASCII letters. Match their validated raw key
// spelling, including Unicode escapes, without allocating a decoded string.
func tmdbASCIIKey(raw []byte, want string) bool {
	j := 0
	for i := 1; i < len(raw)-1; i++ {
		c := uint16(raw[i])
		if c == '\\' {
			if raw[i+1] != 'u' {
				return false
			}
			c = tmdbHexWord(raw[i+2 : i+6])
			i += 5
		}
		if j >= len(want) || c != uint16(want[j]) {
			return false
		}
		j++
	}
	return j == len(want)
}
