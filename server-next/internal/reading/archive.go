package reading

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"strings"
	"unicode"
)

// Every name and size inside a book's archive was written by whoever made the file, so each is
// checked before it is used. A name from inside an archive is only ever a key into the
// archive's own list: it never becomes a path on the server's disk, and an archive inside an
// archive is never opened.
const (
	// maxEntries and maxDirectory bound the archive's own list of files, which is read whole.
	maxEntries   = 20_000
	maxDirectory = 8 << 20
	// maxPartSize is the most one EPUB part or comic page may unpack to. The kept readers
	// refuse anything larger, and the archive reader stops at the size an entry declares.
	maxPartSize = 32 << 20
	// maxXMLSize bounds the container, package and contents documents, which are parsed whole.
	maxXMLSize = 4 << 20
)

// errDamaged says the file is not a book this server can read: it is damaged, or is not what
// its name says.
var errDamaged = errors.New("This book's file is damaged.")

// archive is a book's zip file with the entries that are safe to name, by name.
type archive struct {
	entries map[string]*archiveEntry
	names   []string // in the archive's own order
	close   func()
}

type archiveEntry struct {
	UncompressedSize64 uint64
	Open               func() (io.ReadCloser, error)
}

// openArchive reads a zip file's list of entries, after checking from its end record that the
// list is of a size worth reading. Entries whose names could mean anything outside the
// archive, or that repeat a name already seen, are left out.
func openArchive(r io.ReaderAt, size int64) (*archive, error) {
	if err := checkDirectory(r, size); err != nil {
		return nil, err
	}
	// The zip reader does not go by the end record's counts: it reads one entry after another
	// until something is not an entry. So the list is also bounded while it is read: at most
	// maxDirectory bytes past the end of the file, and no more than maxEntries entries.
	budget := &readBudget{r: r, left: maxDirectory + 512<<10}
	zr, err := zip.NewReader(budget, size)
	budget.left = -1 // the entries themselves are read through it later, without a limit
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, errDamaged
	}
	if len(zr.File) > maxEntries {
		return nil, errDamaged
	}
	a := &archive{entries: make(map[string]*archiveEntry, len(zr.File)), close: func() {}}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") || !safeName(f.Name) {
			continue
		}
		if _, seen := a.entries[f.Name]; seen {
			continue
		}
		a.entries[f.Name] = &archiveEntry{UncompressedSize64: f.UncompressedSize64, Open: f.Open}
		a.names = append(a.names, f.Name)
	}
	return a, nil
}

// readBudget refuses to read past left bytes in all; a negative left is no limit.
type readBudget struct {
	r    io.ReaderAt
	left int64
}

func (b *readBudget) ReadAt(p []byte, off int64) (int, error) {
	if b.left >= 0 {
		if int64(len(p)) > b.left {
			return 0, errDamaged
		}
		b.left -= int64(len(p))
	}
	return b.r.ReadAt(p, off)
}

// safeName reports whether a name inside an archive is a plain relative path: no "..", no
// leading slash, no backslash or drive letter, no control characters, and of a sane length.
func safeName(name string) bool {
	if len(name) == 0 || len(name) > 1024 || !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) || c == unicode.ReplacementChar {
			return false
		}
	}
	return true
}

// read returns a small entry whole, such as an XML document. Anything larger than limit is
// refused before a byte of it is unpacked.
func (a *archive) read(name string, limit int64) ([]byte, error) {
	f := a.entries[name]
	if f == nil || f.UncompressedSize64 > uint64(limit) {
		return nil, errDamaged
	}
	rc, err := f.Open()
	if err != nil {
		return nil, errDamaged
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit))
	if err != nil {
		return nil, errDamaged
	}
	return data, nil
}

// checkDirectory finds the archive's end record and refuses an archive whose list of entries
// is longer than maxEntries or larger than maxDirectory, so a small file cannot make the
// server hold a list of millions. Archives over 4 GB or 65,535 entries keep these counts in a
// second, larger record, which is read too.
func checkDirectory(r io.ReaderAt, size int64) error {
	const endLen, locatorLen, end64Len = 22, 20, 56
	if size < endLen {
		return errDamaged
	}
	tailLen := min(size, endLen+65535)
	tail := make([]byte, tailLen)
	if _, err := r.ReadAt(tail, size-tailLen); err != nil && err != io.EOF {
		return errDamaged
	}
	at := bytes.LastIndex(tail, []byte("PK\x05\x06"))
	if at < 0 || len(tail)-at < endLen {
		return errDamaged
	}
	record := tail[at:]
	entries := uint64(binary.LittleEndian.Uint16(record[10:]))
	listSize := uint64(binary.LittleEndian.Uint32(record[12:]))
	if entries == 0xffff || listSize == 0xffffffff {
		locator := size - tailLen + int64(at) - locatorLen
		if locator < 0 {
			return errDamaged
		}
		buf := make([]byte, locatorLen)
		if _, err := r.ReadAt(buf, locator); err != nil || !bytes.Equal(buf[:4], []byte("PK\x06\x07")) {
			return errDamaged
		}
		offset := binary.LittleEndian.Uint64(buf[8:])
		if offset > uint64(size-end64Len) {
			return errDamaged
		}
		buf = make([]byte, end64Len)
		if _, err := r.ReadAt(buf, int64(offset)); err != nil || !bytes.Equal(buf[:4], []byte("PK\x06\x06")) {
			return errDamaged
		}
		entries, listSize = binary.LittleEndian.Uint64(buf[32:]), binary.LittleEndian.Uint64(buf[40:])
	}
	if entries > maxEntries || listSize > maxDirectory {
		return errDamaged
	}
	return nil
}
