package livechannels

import "io"

// The XML decoder buffers a whole lexical token before returning it. The
// document budget alone would let one field/comment allocate hundreds of MiB.
// Bound the input token first, including quoted attributes, comments and CDATA.
const maxGuideTokenBytes = 1 << 20
const maxGuideRecordBytes = 1 << 20

type guideByteReader struct {
	reader    io.Reader
	bytes     int
	state     byte
	quote     byte
	prefix    [9]byte
	prefixLen int
	tail      [3]byte
	failed    bool
}

func (r *guideByteReader) Read(p []byte) (int, error) {
	if r.failed {
		return 0, ErrInvalid
	}
	// Limit read-ahead independently of the caller's buffer size.
	n, e := r.reader.Read(p[:min(len(p), 4096)])
	for i, b := range p[:n] {
		if !r.consume(b) {
			r.failed = true
			return i, ErrInvalid
		}
	}
	return n, e
}

func (r *guideByteReader) consume(b byte) bool {
	if r.state == 0 && b == '<' {
		r.bytes = 0
		r.state = 't'
		r.prefixLen = 0
		r.quote = 0
	}
	r.bytes++
	if r.bytes > maxGuideTokenBytes {
		return false
	}
	if r.state == 0 {
		return true
	}
	if r.prefixLen < len(r.prefix) {
		r.prefix[r.prefixLen] = b
		r.prefixLen++
		switch string(r.prefix[:r.prefixLen]) {
		case "<!--":
			r.state = 'c'
		case "<![CDATA[":
			r.state = 'd'
		case "<?":
			r.state = 'p'
		}
	}
	// Internal declaration subsets are not supported; refuse them before the
	// decoder can accumulate a large directive containing nested declarations.
	if r.state == 't' && r.prefixLen >= 3 && r.prefix[1] == '!' && r.prefix[2] != '-' && r.prefix[2] != '[' && b == '[' {
		return false
	}
	r.tail[0], r.tail[1], r.tail[2] = r.tail[1], r.tail[2], b
	end := false
	switch r.state {
	case 'c':
		end = r.tail == [3]byte{'-', '-', '>'}
	case 'd':
		end = r.tail == [3]byte{']', ']', '>'}
	case 'p':
		end = r.tail[1] == '?' && b == '>'
	default:
		if r.quote != 0 {
			if b == r.quote {
				r.quote = 0
			}
		} else if b == '\'' || b == '"' {
			r.quote = b
		} else {
			end = b == '>'
		}
	}
	if end {
		r.state = 0
		r.bytes = 0
	}
	return true
}
