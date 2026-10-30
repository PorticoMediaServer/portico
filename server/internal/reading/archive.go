// Package reading serves authorized static publications independently of timed media.
package reading

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"
)

var (
	ErrInvalid     = errors.New("invalid or unsafe publication")
	ErrLimit       = errors.New("publication safety limit exceeded")
	ErrChanged     = errors.New("publication source changed")
	ErrUnavailable = errors.New("reading source unavailable")
)

const (
	MaxSource    = 2 << 30
	MaxMember    = 32 << 20
	MaxEntries   = 4096
	MaxDirectory = 4 << 20
	MaxExpanded  = 2 << 30
)

type archive struct {
	files      map[string]*archiveFile
	order      []string
	sequential *sequentialArchive
}
type archiveFile struct {
	UncompressedSize64 uint64
	zipped             *zip.File
}

// inspectDirectory bounds archive/zip's allocation before it parses the central
// directory. ZIP64, split/encrypted archives and ambiguous trailing data fail closed.
func inspectDirectory(r io.ReaderAt, size int64) error {
	if size < 22 || size > MaxSource {
		return ErrLimit
	}
	n := min(size, 65535+22)
	b := make([]byte, n)
	if _, err := r.ReadAt(b, size-n); err != nil {
		return err
	}
	for i := len(b) - 22; i >= 0; i-- {
		if binary.LittleEndian.Uint32(b[i:]) != 0x06054b50 {
			continue
		}
		if i+22+int(binary.LittleEndian.Uint16(b[i+20:])) != len(b) {
			continue
		}
		entries := int(binary.LittleEndian.Uint16(b[i+10:]))
		length := int64(binary.LittleEndian.Uint32(b[i+12:]))
		offset := int64(binary.LittleEndian.Uint32(b[i+16:]))
		if binary.LittleEndian.Uint16(b[i+4:]) != 0 || binary.LittleEndian.Uint16(b[i+6:]) != 0 || int(binary.LittleEndian.Uint16(b[i+8:])) != entries {
			return ErrInvalid
		}
		if entries == 0 || entries > MaxEntries || length > MaxDirectory || offset+length != size-n+int64(i) {
			return ErrLimit
		}
		return nil
	}
	return ErrInvalid
}

func safePath(name string) bool {
	if name == "" || len(name) > 1024 || strings.ContainsAny(name, "\\\x00%?#:") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, c := range name {
		if c < 32 || c == 127 {
			return false
		}
	}
	for _, p := range strings.Split(name, "/") {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return path.Clean(name) == name
}

func openArchive(r io.ReaderAt, size int64) (*archive, error) {
	if err := inspectDirectory(r, size); err != nil {
		return nil, err
	}
	z, err := zip.NewReader(r, size)
	if err != nil {
		return nil, archiveFailure(err)
	}
	a := &archive{files: map[string]*archiveFile{}}
	var expanded uint64
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !safePath(name) || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) || f.Flags&1 != 0 {
			return nil, ErrInvalid
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if _, exists := a.files[name]; exists {
			return nil, ErrInvalid
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, ErrInvalid
		}
		if f.UncompressedSize64 > MaxMember || f.CompressedSize64 > MaxSource || (f.UncompressedSize64 > 1<<20 && f.UncompressedSize64/max(uint64(1), f.CompressedSize64) > 200) {
			return nil, ErrLimit
		}
		expanded += f.UncompressedSize64
		if expanded > MaxExpanded {
			return nil, ErrLimit
		}
		a.files[name] = &archiveFile{f.UncompressedSize64, f}
		a.order = append(a.order, name)
	}
	return a, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func (a *archive) read(ctx context.Context, name string, limit int64) ([]byte, error) {
	f := a.files[name]
	if f == nil || !safePath(name) {
		return nil, ErrInvalid
	}
	if f.UncompressedSize64 > uint64(limit) {
		return nil, ErrLimit
	}
	if a.sequential != nil {
		return a.sequential.read(ctx, name, limit)
	}
	r, err := f.zipped.Open()
	if err != nil {
		return nil, archiveFailure(err)
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(contextReader{ctx, r}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrLimit
	}
	if uint64(len(b)) != f.UncompressedSize64 {
		return nil, ErrInvalid
	}
	return b, nil
}

// Format errors are unsafe publications; source failures retain their typed
// meaning. In particular, a temporary cap refusal is not a corrupt archive.
func archiveFailure(err error) error {
	if errors.Is(err, zip.ErrFormat) || errors.Is(err, zip.ErrAlgorithm) || errors.Is(err, zip.ErrInsecurePath) {
		return ErrInvalid
	}
	return err
}

// resolveReference accepts only archive-local resources. A fragment is preserved
// by the client, while the archive lookup uses the decoded local filename.
func resolveReference(base, reference string) (string, error) {
	u, err := url.Parse(reference)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || strings.Contains(reference, "\\") {
		return "", ErrInvalid
	}
	p, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		return "", ErrInvalid
	}
	name := path.Join(path.Dir(base), p)
	if p == "" {
		name = base
	}
	if !safePath(name) || strings.HasPrefix(p, "/") {
		return "", ErrInvalid
	}
	return name, nil
}
