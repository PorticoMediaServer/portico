//go:build portico_rar7z

package reading

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/archive7z"
	"github.com/nwaples/rardecode/v2"
)

// openClassicArchive is the single place the pinned RAR and 7z readers are
// used. Both are in-process, bounded readers: no external program, no path is
// passed to a shell, and every member is filtered by the same safeMember rules
// as the ZIP path before anything is opened.
func openClassicArchive(ctx context.Context, format string, ra io.ReaderAt, size int64) (memberArchive, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if format == "cbr" {
		return openRAR(ctx, ra, size)
	}
	// CB7 stays closed: sevenzip v1.6.5 allocates structural counts declared in a
	// small plain header before any per-file bytes are validated, and this server
	// has no way to bound those allocations from outside the parser.
	return nil, archiveFormatError(format)
}

// readerAtFileSystem hands the leased reader to rardecode without exposing a
// filesystem path; a multi-volume archive is refused because no volume set is
// offered.
type readerAtFileSystem struct {
	name string
	ra   io.ReaderAt
	size int64
}

func (r readerAtFileSystem) Open(name string) (fs.File, error) {
	if name != r.name {
		return nil, fs.ErrNotExist
	}
	return &readerAtFile{name: name, ra: r.ra, size: r.size}, nil
}

type readerAtFile struct {
	name string
	ra   io.ReaderAt
	size int64
	at   int64
}

func (f *readerAtFile) Stat() (fs.FileInfo, error) { return readerAtInfo{f}, nil }
func (f *readerAtFile) Read(p []byte) (int, error) {
	if f.at >= f.size {
		return 0, io.EOF
	}
	read, err := f.ra.ReadAt(p, f.at)
	f.at += int64(read)
	return read, err
}
func (f *readerAtFile) Close() error { return nil }

type readerAtInfo struct{ f *readerAtFile }

func (i readerAtInfo) Name() string       { return i.f.name }
func (i readerAtInfo) Size() int64        { return i.f.size }
func (i readerAtInfo) Mode() fs.FileMode  { return 0 }
func (i readerAtInfo) ModTime() time.Time { return time.Time{} }
func (i readerAtInfo) IsDir() bool        { return false }
func (i readerAtInfo) Sys() any           { return nil }

// preflightRAR classifies an archive from its signature and, for RAR4, from its
// block headers. A RAR5 archive is admitted as an in-process walk; a RAR4 one is
// classified by preflightRAR4.
func preflightRAR(ra io.ReaderAt, size int64) (rarRoute, error) {
	var head [8]byte
	if _, err := ra.ReadAt(head[:], 0); err != nil {
		return 0, archiveFailure(err)
	}
	if head[0] != 'R' || head[1] != 'a' || head[2] != 'r' || head[3] != '!' || head[4] != 0x1a || head[5] != 0x07 {
		return 0, ErrInvalid
	}
	if head[6] == 0x01 && head[7] == 0x00 {
		return rarInProcess, nil
	}
	if head[6] == 0x00 {
		return preflightRAR4(ra, size)
	}
	return 0, archiveFormatError("cbr")
}

// rarRoute says who decodes an archive: this process for the shapes it reads
// without a RAR4 decoder or solid state, the bounded worker for the rest.
type rarRoute int

const (
	rarInProcess rarRoute = iota
	rarWorker
)

// RAR4 block types and flags (RARLAB technote, RAR 2.9/3.x/4.x).
const (
	rar4MainHead, rar4FileHead, rar4EndHead      = 0x73, 0x74, 0x7b
	rar4MainVolume, rar4MainSolid, rar4MainCrypt = 0x0001, 0x0008, 0x0080
	rar4FileSplit, rar4FileCrypt, rar4FileSolid  = 0x0003, 0x0004, 0x0010
	rar4FileLarge, rar4LongBlock                 = 0x0100, 0x8000
	rar4MethodStore                              = 0x30
	rar4MethodFastest, rar4MethodBest            = 0x31, 0x35
)

// preflightRAR4 walks a legacy archive's block headers without decoding
// anything and classifies it. A plain volume whose files are all stored (method
// 0x30) and not solid stays in process: comic pages are already-compressed
// images, so reading one is a bounded copy. A compressed method (0x31–0x35) or
// any solid flag needs the worker's RAR 2.9 LZ/PPM decoder and solid window.
// Encryption, split volumes, an unknown size and a file header without
// LONG_BLOCK are refused here, before either decoder runs.
func preflightRAR4(ra io.ReaderAt, size int64) (rarRoute, error) {
	at, files := int64(7), 0
	route := rarInProcess
	for blocks := 0; at < size; blocks++ {
		if blocks > MaxEntries+16 {
			return 0, ErrLimit
		}
		var h [32]byte
		n, err := ra.ReadAt(h[:], at)
		if n < 7 {
			if err == nil || errors.Is(err, io.EOF) {
				return 0, ErrInvalid
			}
			return 0, archiveFailure(err)
		}
		kind, flags, headSize := h[2], binary.LittleEndian.Uint16(h[3:5]), int64(binary.LittleEndian.Uint16(h[5:7]))
		if headSize < 7 {
			return 0, ErrInvalid
		}
		next := at + headSize
		switch kind {
		case rar4MainHead:
			if flags&(rar4MainVolume|rar4MainCrypt) != 0 {
				return 0, archiveFormatError("cbr")
			}
			if flags&rar4MainSolid != 0 {
				route = rarWorker
			}
		case rar4FileHead:
			if headSize < 32 || n < 32 {
				return 0, ErrInvalid
			}
			// A file block carries its data (LONG_BLOCK), as every RAR writer sets it; the pinned
			// decoder reads the data size only then, so a header without it is refused cleanly here.
			if flags&rar4LongBlock == 0 || flags&(rar4FileSplit|rar4FileCrypt) != 0 {
				return 0, archiveFormatError("cbr")
			}
			if flags&rar4FileSolid != 0 {
				route = rarWorker
			}
			if flags&rar4FileLarge == 0 && binary.LittleEndian.Uint32(h[11:15]) == 0xffffffff {
				return 0, archiveFormatError("cbr")
			}
			packed := int64(binary.LittleEndian.Uint32(h[7:11]))
			if flags&rar4FileLarge != 0 {
				var high [4]byte
				if _, err := ra.ReadAt(high[:], at+32); err != nil {
					return 0, archiveFailure(err)
				}
				packed |= int64(binary.LittleEndian.Uint32(high[:])) << 32
			}
			if method := h[25]; method != rar4MethodStore {
				if method < rar4MethodFastest || method > rar4MethodBest {
					return 0, archiveFormatError("cbr")
				}
				route = rarWorker
			}
			files++
			next += packed
		case rar4EndHead:
			if files == 0 {
				return 0, ErrInvalid
			}
			return route, nil
		default:
			if flags&rar4LongBlock != 0 {
				if n < 11 {
					return 0, ErrInvalid
				}
				next += int64(binary.LittleEndian.Uint32(h[7:11]))
			}
		}
		if next <= at || next > size {
			return 0, ErrInvalid
		}
		at = next
	}
	if files == 0 {
		return 0, ErrInvalid
	}
	return route, nil
}

// openRAR reads headers one at a time through rardecode's streaming reader. No
// header set is materialised before admission: each Next is bounded by the same
// dictionary/read limits, counted against MaxEntries and MaxExpanded, and
// encrypted or multi-volume archives are refused whole.
// This process decodes only what needs no RAR4 decoder and no solid state: a
// RAR5 archive up to its first solid file, or a RAR4 archive whose files are all
// stored. A compressed RAR4 (LZ or PPM) and a solid archive of either version go
// to the bounded worker, so the RAR 2.9 PPM allocator and the solid window never
// grow in this process. Anything the worker refuses or cannot decode is the same
// unsupported-format answer.
func openRAR(ctx context.Context, ra io.ReaderAt, size int64) (memberArchive, error) {
	if size < 22 || size > MaxSource {
		return nil, ErrLimit
	}
	route, err := preflightRAR(ra, size)
	if err != nil {
		return nil, err
	}
	if route == rarWorker {
		return openRARWorker(ctx, ra, size)
	}
	reader, err := rardecode.NewReader(&contextReader{ctx: ctx, r: io.NewSectionReader(ra, 0, size)},
		rardecode.MaxDictionarySize(maxRARDictionary))
	if err != nil {
		return nil, archiveFailure(err)
	}
	out := make([]archiveMember, 0, 32)
	seen := map[string]bool{}
	var expanded int64
	headers := 0
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, rardecode.ErrSolidOpen) {
			return openRARWorker(ctx, ra, size)
		}
		if errors.Is(err, rardecode.ErrMultiVolume) || errors.Is(err, rardecode.ErrArchiveEncrypted) || errors.Is(err, rardecode.ErrArchivedFileEncrypted) {
			return nil, archiveFormatError("cbr")
		}
		if err != nil {
			return nil, archiveFailure(err)
		}
		// Counted after a real header, so exactly MaxEntries is allowed and the
		// next one is refused.
		headers++
		if headers > MaxEntries {
			return nil, ErrLimit
		}
		if header.Encrypted || header.HeaderEncrypted || header.LinkType != rardecode.LinkTypeNone || header.UnKnownSize {
			return nil, archiveFormatError("cbr")
		}
		if header.Solid {
			// A RAR5 solid file: every later member depends on the window this
			// one builds, which belongs in the worker.
			return openRARWorker(ctx, ra, size)
		}
		if header.IsDir {
			continue
		}
		if !memberName(header.Name, seen) {
			continue
		}
		if header.UnPackedSize < 0 || header.UnPackedSize > MaxMember || uint64(MaxExpanded)-uint64(max(expanded, 0)) < uint64(header.UnPackedSize) {
			return nil, ErrLimit
		}
		expanded += header.UnPackedSize
		if !rarComicMember(header.Name, uint64(header.UnPackedSize)) {
			continue
		}
		name, memberSize := header.Name, header.UnPackedSize
		out = append(out, archiveMember{Name: name, Size: memberSize, Open: func() (io.ReadCloser, error) {
			return openRARMember(ctx, ra, size, name, memberSize)
		}})
	}
	if len(out) == 0 {
		return nil, ErrInvalid
	}
	sortMembers(out)
	return &listMembers{files: out, close: func() error { return nil }}, nil
}

// openRARWorker is the one path for a compressed or solid CBR: the worker gets
// the staged bytes, and a refusal, crash or kill is the unsupported-format
// error the caller already answers as a definite 422.
func openRARWorker(ctx context.Context, ra io.ReaderAt, size int64) (memberArchive, error) {
	members, err := rarWorkerMembers(ctx, ra, size)
	if err != nil {
		return nil, err
	}
	return &zipMembers{files: members}, nil
}

// rarComicMember admits a CBR's pages and, as the CB7 worker does, its one exact root
// ComicInfo.xml under the metadata cap; OpenClassicComic reads that as metadata only.
func rarComicMember(name string, size uint64) bool {
	if name == "ComicInfo.xml" {
		return size > 0 && size <= archive7z.MaxComicInfo
	}
	return safeMember(name, false, size)
}

// openRARMember seeks the one member from a fresh streaming reader. Only the
// in-process shapes reach it — a RAR5 archive up to its first solid file, or a
// RAR4 archive whose files are all stored — so skipping earlier headers needs no
// decompressed page data.
func openRARMember(ctx context.Context, ra io.ReaderAt, size int64, name string, memberSize int64) (io.ReadCloser, error) {
	reader, err := rardecode.NewReader(&contextReader{ctx: ctx, r: io.NewSectionReader(ra, 0, size)},
		rardecode.MaxDictionarySize(maxRARDictionary))
	if err != nil {
		return nil, archiveFailure(err)
	}
	for headers := 0; ; headers++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if headers >= MaxEntries {
			return nil, ErrLimit
		}
		header, err := reader.Next()
		if err != nil {
			return nil, archiveFailure(err)
		}
		if header.Name != name {
			continue
		}
		return &boundedArchiveReader{ctx: ctx, reader: reader, remaining: memberSize}, nil
	}
}

// boundedArchiveReader stops at the declared member size and checks the request
// between reads; the underlying section reader is owned by this call.
type boundedArchiveReader struct {
	ctx       context.Context
	reader    io.Reader
	remaining int64
	closed    bool
}

func (b *boundedArchiveReader) Read(p []byte) (int, error) {
	if b.closed {
		return 0, io.EOF
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	read, err := b.reader.Read(p)
	b.remaining -= int64(read)
	return read, err
}

func (b *boundedArchiveReader) Close() error { b.closed = true; return nil }

// fsMembers walks a filesystem-backed archive once, bounded, keeping only plain
// image members.
func fsMembers(ctx context.Context, rfs fs.FS) ([]archiveMember, error) {
	members := []archiveMember{}
	seen := map[string]bool{}
	var expanded int64
	encountered := 0
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if depth > maxArchiveDepth {
			return ErrLimit
		}
		entries, err := fs.ReadDir(rfs, dir)
		if err != nil {
			return archiveFailure(err)
		}
		for _, entry := range entries {
			encountered++
			if encountered > MaxEntries {
				return ErrLimit
			}
			name := entry.Name()
			if dir != "." {
				name = dir + "/" + entry.Name()
			}
			if !memberName(name, seen) {
				continue
			}
			if entry.IsDir() {
				if err := walk(name, depth+1); err != nil {
					return err
				}
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return archiveFailure(err)
			}
			if info.Size() < 0 || info.Size() > MaxMember || uint64(MaxExpanded)-uint64(max(expanded, 0)) < uint64(info.Size()) {
				return ErrLimit
			}
			expanded += info.Size()
			if !safeMember(name, false, uint64(info.Size())) {
				continue
			}
			memberName := name
			members = append(members, archiveMember{Name: memberName, Size: info.Size(), Open: func() (io.ReadCloser, error) {
				file, err := rfs.Open(memberName)
				if err != nil {
					return nil, archiveFailure(err)
				}
				return file, nil
			}})
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return nil, err
	}
	return members, nil
}

type listMembers struct {
	files []archiveMember
	close func() error
}

func (l *listMembers) Members() []archiveMember { return l.files }
func (l *listMembers) Close() error             { return l.close() }
