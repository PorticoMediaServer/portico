// Package scanpath implements the filename_unrepresentable diagnostic's byte
// representation. It does not classify failures, validate filesystem paths or
// confer source, cursor, receipt, protection or disclosure authority.
package scanpath

import (
	"errors"
	"strings"
)

var (
	ErrCapacity = errors.New("filename diagnostic exceeds byte budget")
	ErrEncoding = errors.New("noncanonical filename diagnostic")
)

const hex = "0123456789abcdef"

// EncodeFilename preserves printable ASCII except backslash literally and
// encodes every other byte as lowercase \xhh. It does not interpret UTF-8,
// normalize names or apply filesystem policy. Empty bytes have an empty
// representation; admitting an empty path is a separate caller error.
//
// budgetBytes bounds the COMPLETE raw input plus escaped output payload, not
// heap/RSS, database/index/receipt bytes or a grant. The caller must own the
// input for this call and reserve those other costs separately. Every size is
// checked before output allocation. On failure no partial output is returned.
func EncodeFilename(raw []byte, budgetBytes int64) (string, error) {
	if budgetBytes < 0 || int64(len(raw)) > budgetBytes {
		return "", ErrCapacity
	}
	remaining := budgetBytes - int64(len(raw))
	size := 0
	maxInt := int(^uint(0) >> 1)
	for _, b := range raw {
		width := 4
		if literal(b) {
			width = 1
		}
		if int64(width) > remaining || size > maxInt-width {
			return "", ErrCapacity
		}
		remaining -= int64(width)
		size += width
	}
	var out strings.Builder
	out.Grow(size)
	for _, b := range raw {
		if literal(b) {
			out.WriteByte(b)
		} else {
			out.WriteString(`\x`)
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String(), nil
}

// DecodeFilename accepts ONLY EncodeFilename's canonical byte language. It
// rejects uppercase hex, escaped printable literals, raw non-ASCII/control
// bytes, bare backslashes and malformed escapes. It preserves all raw bytes,
// including NUL and invalid UTF-8, in a new independent slice.
//
// budgetBytes bounds escaped input plus decoded output payload. The entire
// structure and size are checked before allocating the output. Consumers must
// also check the exact reason and retained run/source/root scope, then compare
// the decoded bytes to their native candidate. A successful decode alone is
// never protection, containment or current permission proof.
func DecodeFilename(display string, budgetBytes int64) ([]byte, error) {
	if budgetBytes < 0 || int64(len(display)) > budgetBytes {
		return nil, ErrCapacity
	}
	remaining := budgetBytes - int64(len(display))
	size := 0
	for i := 0; i < len(display); {
		b := display[i]
		if literal(b) {
			i++
		} else {
			if b != '\\' || len(display)-i < 4 || display[i+1] != 'x' {
				return nil, ErrEncoding
			}
			hi, lo := nibble(display[i+2]), nibble(display[i+3])
			if hi < 0 || lo < 0 || literal(byte(hi<<4|lo)) {
				return nil, ErrEncoding
			}
			i += 4
		}
		if remaining == 0 {
			return nil, ErrCapacity
		}
		remaining--
		size++ // no larger than the native input length
	}
	out := make([]byte, size)
	for i, j := 0, 0; i < len(display); j++ {
		if literal(display[i]) {
			out[j] = display[i]
			i++
		} else {
			out[j] = byte(nibble(display[i+2])<<4 | nibble(display[i+3]))
			i += 4
		}
	}
	return out, nil
}

func literal(b byte) bool { return b >= 0x20 && b <= 0x7e && b != '\\' }

func nibble(b byte) int {
	if b >= '0' && b <= '9' {
		return int(b - '0')
	}
	if b >= 'a' && b <= 'f' {
		return int(b-'a') + 10
	}
	return -1
}
