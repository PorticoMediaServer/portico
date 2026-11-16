package reading

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
)

// A book downloaded to read offline is one the app's reader opens from the file: an EPUB, a PDF
// or a CBZ (10-reading rule 46). A MOBI or AZW3 downloads as the EPUB it is read as, and a CBR,
// CB7 or folder of pictures as a CBZ of its pages. Each copy is made once into the copies
// folder and kept while the book's file is unchanged, so its size is known before it is
// fetched and a fetch that breaks off carries on with the same bytes.

// Copy is a book's download copy: where it is, its size and SHA-256, and the form it is in.
type Copy struct {
	Path      string
	Container string
	Size      int64
	SHA256    string
}

// copyKeep is how long a copy no download has fetched is kept.
const copyKeep = 30 * 24 * time.Hour

// DownloadCopy returns the copy a book version downloads as, made now if it is not made yet.
// ok is false for a book whose own file is what the app reads.
func (s *Service) DownloadCopy(ctx context.Context, v db.BookVersion) (c Copy, ok bool, err error) {
	format := VersionFormat(v)
	c.Container = map[string]string{FormatMOBI: FormatEPUB, FormatAZW3: FormatEPUB, FormatCBR: FormatCBZ, FormatCB7: FormatCBZ,
		FormatImages: FormatCBZ}[format]
	if c.Container == "" {
		return c, false, nil
	}
	if s.Copies == "" {
		return c, true, ErrUnavailable
	}
	c.Path = filepath.Join(s.Copies, fmt.Sprintf("%d-%s.%s", v.FileID, Fingerprint(v.Size, v.ModifiedNS), c.Container))
	// One copy is made at a time: they are rare, and each may read a whole large file.
	s.copying.Lock()
	defer s.copying.Unlock()
	if made, err := os.Stat(c.Path); err == nil {
		if sum, err := os.ReadFile(c.Path + ".sha256"); err == nil {
			now := time.Now()
			os.Chtimes(c.Path, now, now)
			c.Size, c.SHA256 = made.Size(), strings.TrimSpace(string(sum))
			return c, true, nil
		}
	}
	if err := os.MkdirAll(s.Copies, 0o700); err != nil {
		return c, true, err
	}
	making, err := os.CreateTemp(s.Copies, "making-*")
	if err != nil {
		return c, true, err
	}
	defer os.Remove(making.Name())
	defer making.Close()
	sum := sha256.New()
	out := io.MultiWriter(making, sum)
	switch format {
	case FormatMOBI, FormatAZW3:
		err = s.withArchive(ctx, v, func(a *archive) error { return writeEPUB(out, a) })
	case FormatImages:
		err = s.picturesCopy(ctx, v, out)
	default:
		var f *bookFile
		var place db.FileOnDisk
		if f, place, err = s.openFile(ctx, v.FileID); err == nil {
			if place.Size != v.Size || place.ModifiedNS != v.ModifiedNS {
				err = ErrChanged
			} else {
				err = f.explain(comicCopy(ctx, f, format, out))
			}
			f.close()
		}
	}
	if err != nil {
		return c, true, err
	}
	if c.Size, err = making.Seek(0, io.SeekCurrent); err != nil {
		return c, true, err
	}
	c.SHA256 = hex.EncodeToString(sum.Sum(nil))
	if err = making.Close(); err != nil {
		return c, true, err
	}
	if err = os.WriteFile(c.Path+".sha256", []byte(c.SHA256), 0o600); err != nil {
		return c, true, err
	}
	if err = os.Rename(making.Name(), c.Path); err != nil {
		return c, true, err
	}
	s.pruneCopies()
	return c, true, nil
}

// writeEPUB writes a rebuilt book as an EPUB: its mimetype first, then everything else, all
// stored, so the same book always gives the same bytes.
func writeEPUB(out io.Writer, a *archive) error {
	z := zip.NewWriter(out)
	w, err := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return err
	}
	io.WriteString(w, "application/epub+zip")
	for _, name := range a.names {
		if name == "mimetype" {
			continue
		}
		entry := a.entries[name]
		body, err := entry.Open()
		if err != nil {
			return err
		}
		err = copyMember(z, name, body, int64(entry.UncompressedSize64))
		body.Close()
		if err != nil {
			return err
		}
	}
	return z.Close()
}

// picturesCopy writes a comic made of a folder of pictures as a CBZ, its pages numbered in the
// scanner's order so a reader sorting them by name keeps it.
func (s *Service) picturesCopy(ctx context.Context, v db.BookVersion, out io.Writer) error {
	folders, err := s.db.LibraryFolders(ctx)
	if err != nil {
		return err
	}
	z := zip.NewWriter(out)
	for n := range v.Pages {
		page, _, _, err := s.db.ImagePage(ctx, v.FileID, n)
		if err != nil {
			return err
		}
		f, err := libraryfile.Open(page.Path, folders)
		if err != nil {
			return ErrUnavailable
		}
		info, err := f.Stat()
		if err == nil && (info.Size() != page.Size || info.ModTime().UnixNano() != page.ModifiedNS) {
			err = ErrChanged
		}
		if err == nil {
			err = copyMember(z, fmt.Sprintf("%05d%s", n+1, strings.ToLower(path.Ext(page.Path))), f, page.Size)
		}
		f.Close()
		if err != nil {
			return err
		}
	}
	return z.Close()
}

// pruneCopies removes copies no download has fetched for copyKeep, and anything left by one
// that was being made when the server stopped.
func (s *Service) pruneCopies() {
	entries, _ := os.ReadDir(s.Copies)
	for _, e := range entries {
		name := filepath.Join(s.Copies, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		age := time.Since(info.ModTime())
		switch {
		case strings.HasPrefix(e.Name(), "making-"):
			if age > time.Hour {
				os.Remove(name)
			}
		case strings.HasSuffix(e.Name(), ".sha256"):
			if _, err := os.Stat(strings.TrimSuffix(name, ".sha256")); os.IsNotExist(err) {
				os.Remove(name)
			}
		case age > copyKeep:
			os.Remove(name)
			os.Remove(name + ".sha256")
		}
	}
}
