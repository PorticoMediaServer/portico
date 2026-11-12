package reading

import (
	"encoding/xml"
	"io"
	"strings"
	"unicode"
)

// EPUBWords counts the words in the reading order, excluding markup, styles and scripts.
// A failed or excessively large extraction supplies no estimate rather than a partial count.
func EPUBWords(r io.ReaderAt, size int64) (int, error) {
	a, err := openArchive(r, size)
	if err != nil {
		return 0, err
	}
	book, err := readEPUB(a)
	if err != nil {
		return 0, err
	}
	words, total := 0, 0
	seen := map[string]bool{}
	for _, name := range book.spine {
		if seen[name] {
			continue
		}
		seen[name] = true
		raw, err := a.read(name, maxPartSize)
		if err != nil {
			return 0, err
		}
		total += len(raw)
		if total > 64<<20 {
			return 0, errDamaged
		}
		d, hidden, inWord := newDecoder(raw), 0, false
		for {
			token, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return 0, err
			}
			switch t := token.(type) {
			case xml.StartElement:
				if hidden > 0 || strings.EqualFold(t.Name.Local, "head") || strings.EqualFold(t.Name.Local, "script") || strings.EqualFold(t.Name.Local, "style") {
					hidden++
				}
				if strings.EqualFold(t.Name.Local, "p") || strings.EqualFold(t.Name.Local, "div") || strings.EqualFold(t.Name.Local, "br") {
					inWord = false
				}
			case xml.EndElement:
				if hidden > 0 {
					hidden--
				}
				if strings.EqualFold(t.Name.Local, "p") || strings.EqualFold(t.Name.Local, "div") {
					inWord = false
				}
			case xml.CharData:
				if hidden > 0 {
					continue
				}
				for _, c := range string(t) {
					word := unicode.IsLetter(c) || unicode.IsNumber(c)
					if word && !inWord {
						words++
					}
					inWord = word
				}
			}
		}
	}
	return words, nil
}
