package reading

import (
	"io"
	"path"
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
	raw, _, err := a.epubPackage()
	return raw, err
}

func (a *archive) epubPackage() ([]byte, string, error) {
	raw, err := a.read("META-INF/container.xml", maxXMLSize)
	if err != nil {
		return nil, "", errDamaged
	}
	var container struct {
		Rootfiles []struct {
			FullPath  string `xml:"full-path,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := decode(raw, &container); err != nil {
		return nil, "", errDamaged
	}
	for _, root := range container.Rootfiles {
		if root.MediaType == "" || root.MediaType == "application/oebps-package+xml" {
			raw, err := a.read(root.FullPath, maxXMLSize)
			return raw, root.FullPath, err
		}
	}
	return nil, "", errDamaged
}

// Cover reads only an EPUB's declared cover or a CBZ's first page, through the same
// path, directory and unpacked-size checks as the reader. PDFs have no extracted cover.
func Cover(r io.ReaderAt, size int64, ext string, limit int64) ([]byte, error) {
	if ext != ".epub" && ext != ".cbz" {
		return nil, nil
	}
	a, err := openArchive(r, size)
	if err != nil {
		return nil, err
	}
	if ext == ".cbz" {
		comic, err := readComic(a)
		if err != nil {
			return nil, err
		}
		return a.read(comic.pages[0], min(limit, maxPartSize))
	}
	raw, name, err := a.epubPackage()
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Meta []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"metadata>meta"`
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"manifest>item"`
	}
	if err := decode(raw, &pkg); err != nil {
		return nil, errDamaged
	}
	coverID := ""
	for _, meta := range pkg.Meta {
		if meta.Name == "cover" {
			coverID = meta.Content
			break
		}
	}
	for _, item := range pkg.Items {
		if hasWord(item.Properties, "cover-image") || coverID != "" && item.ID == coverID {
			if cover, _, ok := resolve(path.Dir(name), item.Href); ok {
				return a.read(cover, min(limit, maxPartSize))
			}
		}
	}
	return nil, nil
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
