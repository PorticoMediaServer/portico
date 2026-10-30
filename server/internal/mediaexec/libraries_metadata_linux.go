//go:build linux

package mediaexec

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"io"
	"os"
	"slices"
	"syscall"
)

// Record exactly the ELF bytes consumed by dependency resolution, including
// headers, interpreter, dynamic entries and strings. Stat generations can
// collide on rapid in-place writes, even when ctime is checked. A cache hit
// replays this bounded read plan instead of parsing the ELF tree again.
const maxLibraryMetadataBytes = 16 << 20
const maxLibraryMetadataReads = 4096

type libraryMetadataBudget struct {
	bytes, reads int
	exceeded     bool
}

type libraryMetadataRead struct {
	offset int64
	size   int
}

type libraryMetadata struct {
	reads  []libraryMetadataRead
	sum    [sha256.Size]byte
	budget *libraryMetadataBudget
}

type libraryMetadataReader struct {
	file   *os.File
	budget *libraryMetadataBudget
	reads  []libraryMetadataRead
	sum    hash.Hash
}

func openLibraryMetadataFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func metadataReadPrefix(sum hash.Hash, read libraryMetadataRead) {
	var header [16]byte
	binary.LittleEndian.PutUint64(header[:8], uint64(read.offset))
	binary.LittleEndian.PutUint64(header[8:], uint64(read.size))
	sum.Write(header[:])
}

func (r *libraryMetadataReader) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.file.ReadAt(p, offset)
	if n > 0 && !r.budget.exceeded {
		if n > maxLibraryMetadataBytes-r.budget.bytes || r.budget.reads >= maxLibraryMetadataReads {
			// A complex ELF still uses the original resolver, but that one
			// closure cannot be memoized without a complete bounded witness.
			r.budget.exceeded = true
		} else {
			r.budget.bytes += n
			r.budget.reads++
			read := libraryMetadataRead{offset, n}
			r.reads = append(r.reads, read)
			metadataReadPrefix(r.sum, read)
			r.sum.Write(p[:n])
		}
	}
	return n, err
}

func (r *libraryMetadataReader) snapshot() *libraryMetadata {
	result := &libraryMetadata{reads: slices.Clone(r.reads), budget: r.budget}
	copy(result.sum[:], r.sum.Sum(nil))
	return result
}

func (m *libraryMetadata) unchanged(path string, expected os.FileInfo) bool {
	if m.budget.exceeded || len(m.reads) == 0 {
		return false
	}
	file, err := openLibraryMetadataFile(path)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if !sameLibraryInfo(expected, info, err) {
		return false
	}
	sum := sha256.New()
	var buffer [4096]byte
	for _, read := range m.reads {
		metadataReadPrefix(sum, read)
		n, err := io.CopyBuffer(sum, io.NewSectionReader(file, read.offset, int64(read.size)), buffer[:])
		if err != nil || n != int64(read.size) {
			return false
		}
	}
	var actual [sha256.Size]byte
	copy(actual[:], sum.Sum(nil))
	after, statErr := file.Stat()
	current, pathErr := os.Stat(path)
	return actual == m.sum && sameLibraryInfo(info, after, statErr) && sameLibraryInfo(info, current, pathErr)
}
