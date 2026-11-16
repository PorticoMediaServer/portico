package reading

import (
	"context"
	"io"
	"path"
	"strings"
)

// Details are the documents inside a book's own file that say what the book is, for the scanner
// to read its title, author and the rest from (10-reading rule 8: details come from the files).
// They are read through the same guarded archive reader as the book itself.

// EPUBPackage returns an EPUB's own package document: the one its container names. A MOBI or
// AZW3 gives the one it is rebuilt with, from what its own header says.
func EPUBPackage(r io.ReaderAt, size int64) ([]byte, error) {
	a, err := ebookArchive(r, size)
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

// Cover reads only an EPUB's declared cover, through the same path, directory and
// unpacked-size checks as the reader, or a PDF's first page, drawn (pdf.go). A comic's cover
// is read by Comic.
func Cover(r io.ReaderAt, size int64, ext string, limit int64) ([]byte, error) {
	if ext == ".pdf" {
		return pdfCover(r, size, limit)
	}
	if ext != ".epub" {
		return nil, nil
	}
	a, err := openArchive(r, size)
	if err != nil {
		return nil, err
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

// Comic reads a comic archive (CBZ, CBR or CB7) once for the scanner: its ComicInfo.xml, nil
// when it has none, and, when coverLimit is above zero, its first page as its cover, nil when
// that page is larger than coverLimit. CBR and CB7 are read by the same limited worker as the
// reader uses; in a build without that format there is nothing to read.
func Comic(ctx context.Context, r io.ReaderAt, size int64, format string, coverLimit int64) (info, cover []byte, err error) {
	if !comicFormat(format) {
		return nil, nil, nil
	}
	a, err := openBookArchive(ctx, &bookFile{at: r, size: size}, format)
	if err != nil {
		return nil, nil, err
	}
	defer a.close()
	for _, name := range a.names {
		if strings.EqualFold(name, "ComicInfo.xml") {
			if info, err = a.read(name, 1<<20); err != nil {
				return nil, nil, err
			}
			break
		}
	}
	if coverLimit > 0 {
		if comic, err := readComic(a); err == nil {
			cover, _ = a.read(comic.pages[0], min(coverLimit, maxPartSize))
		}
	}
	return info, cover, nil
}
