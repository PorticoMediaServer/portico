package reading

import (
	"bytes"
	"io"
)

// A recognized container that disagrees with the declared comic format is an
// archive-type problem. Unknown or malformed bytes still reach the bounded
// format parser; sniffing neither admits a format nor selects another decoder.
func checkComicArchiveSignature(head []byte, expected string) error {
	actual := ""
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")), bytes.HasPrefix(head, []byte("PK\x07\x08")):
		actual = "cbz"
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07\x00")), bytes.HasPrefix(head, []byte("Rar!\x1a\x07\x01\x00")):
		actual = "cbr"
	case bytes.HasPrefix(head, []byte("7z\xbc\xaf\x27\x1c")):
		actual = "cb7"
	}
	if actual != "" && actual != expected {
		return archiveFormatError(actual)
	}
	return nil
}

func checkComicArchiveReader(ra io.ReaderAt, size int64, expected string) error {
	if size < 22 || size > MaxSource {
		return ErrLimit
	}
	var head [8]byte
	if _, err := ra.ReadAt(head[:], 0); err != nil {
		return err
	}
	return checkComicArchiveSignature(head[:], expected)
}
