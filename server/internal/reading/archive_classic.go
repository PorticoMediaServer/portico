//go:build portico_rar7z

package reading

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"time"

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

// openRAR reads headers one at a time through rardecode's streaming reader. No
// header set is materialised before admission: each Next is bounded by the same
// dictionary/read limits, counted against MaxEntries and MaxExpanded, and solid,
// encrypted or multi-volume archives are refused whole.
// RAR3-era containers may select the separate PPM allocator inside the first
// Read (decode29_ppm), whose heap is not governed by MaxDictionarySize. Only the
// RAR5 signature is admitted; a legacy RAR4 container is refused before the
// parser runs rather than promising a bound the option does not provide.
func preflightRAR(ra io.ReaderAt) error {
	var head [8]byte
	if _, err := ra.ReadAt(head[:], 0); err != nil {
		return archiveFailure(err)
	}
	if head[0] != 'R' || head[1] != 'a' || head[2] != 'r' || head[3] != '!' || head[4] != 0x1a || head[5] != 0x07 {
		return ErrInvalid
	}
	if head[6] != 0x01 || head[7] != 0x00 {
		return archiveFormatError("cbr")
	}
	return nil
}

func openRAR(ctx context.Context, ra io.ReaderAt, size int64) (memberArchive, error) {
	if size < 22 || size > MaxSource {
		return nil, ErrLimit
	}
	if err := preflightRAR(ra); err != nil {
		return nil, err
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
		if errors.Is(err, rardecode.ErrMultiVolume) || errors.Is(err, rardecode.ErrArchiveEncrypted) || errors.Is(err, rardecode.ErrArchivedFileEncrypted) || errors.Is(err, rardecode.ErrSolidOpen) {
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
		if header.Solid || header.Encrypted || header.HeaderEncrypted || header.LinkType != rardecode.LinkTypeNone || header.UnKnownSize {
			return nil, archiveFormatError("cbr")
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
		if !safeMember(header.Name, false, uint64(header.UnPackedSize)) {
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

// openRARMember seeks the one member from a fresh streaming reader. Solid
// archives are refused above, so skipping earlier headers needs no decompressed
// page data.
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
