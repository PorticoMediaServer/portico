package reading

import (
	"io"
	"strings"
)

// Details are the documents inside a book's own file that say what the book is, for the scanner
// to read its title, author and the rest from (10-reading rule 8: details come from the files).
// They are read through the same guarded archive reader as the book itself.

// EPUBPackage returns an EPUB's own package document: the one its container names.
func EPUBPackage(r io.ReaderAt, size int64) ([]byte, error) {
	a, err := openArchive(r, size)
	if err != nil {
		return nil, err
	}
	raw, err := a.read("META-INF/container.xml", maxXMLSize)
	if err != nil {
		return nil, errDamaged
	}
	var container struct {
		Rootfiles []struct {
			FullPath  string `xml:"full-path,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := decode(raw, &container); err != nil {
		return nil, errDamaged
	}
	for _, root := range container.Rootfiles {
		if root.MediaType == "" || root.MediaType == "application/oebps-package+xml" {
			return a.read(root.FullPath, maxXMLSize)
		}
	}
	return nil, errDamaged
}

// ComicInfo returns a comic archive's ComicInfo.xml, or nil when it has none.
func ComicInfo(r io.ReaderAt, size int64) ([]byte, error) {
	a, err := openArchive(r, size)
	if err != nil {
		return nil, err
	}
	for _, name := range a.names {
		if strings.EqualFold(name, "ComicInfo.xml") {
			return a.read(name, 1<<20)
		}
	}
	return nil, nil
}
