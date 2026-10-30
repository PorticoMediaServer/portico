package localmetadata

import (
	"bytes"
	"errors"
	"io"
	"strings"
)

// Local XML supports the legacy single-byte encodings commonly used by NFO/OPF.
// Conversion is bounded and does not resolve entities or fetch anything.
func localXMLCharset(label string, input io.Reader) (io.Reader, error) {
	label = strings.ToLower(strings.TrimSpace(label))
	if label != "us-ascii" && label != "ascii" && label != "iso-8859-1" && label != "latin1" && label != "windows-1252" {
		return nil, errors.New("unsupported local XML encoding")
	}
	raw, err := io.ReadAll(io.LimitReader(input, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 {
		return nil, errors.New("XML encoding budget")
	}
	var out strings.Builder
	for _, b := range raw {
		r := rune(b)
		if label == "us-ascii" || label == "ascii" {
			if b >= 128 {
				return nil, errors.New("invalid ASCII XML")
			}
		}
		if label == "windows-1252" && b >= 0x80 && b < 0xa0 {
			r = windows1252[b-0x80]
		}
		out.WriteRune(r)
	}
	return bytes.NewBufferString(out.String()), nil
}

var windows1252 = [...]rune{'€', '\u0081', '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', '\u008d', 'Ž', '\u008f', '\u0090', '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', '\u009d', 'ž', 'Ÿ'}
