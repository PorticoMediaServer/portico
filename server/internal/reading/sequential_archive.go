package reading

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"strings"
)

// SequentialSource is a genuine from-start acquisition, not ReaderAt emulation.
// Open is supplied by the actual actor/session/revision-bound transport owner.
// Every stream starts at byte zero; cancellation/error is terminal for that
// stream. No publication bytes are cached or materialized by this package.
type SequentialSource struct {
	Size int64
	Open func(context.Context) (io.ReadCloser, error)
}
type sequentialEntry struct {
	name                           string
	offset, data, compressed, size int64
	method, flags                  uint16
	crc                            uint32
	directory                      bool
}
type sequentialArchive struct {
	source  SequentialSource
	entries map[string]sequentialEntry
}
type countStream struct {
	io.Reader
	n int64
}

func (r *countStream) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += int64(n)
	return n, err
}

type streamCursor struct {
	r       *bufio.Reader
	counted *countStream
}

func (c *streamCursor) position() int64 { return c.counted.n - int64(c.r.Buffered()) }
func (c *streamCursor) bytes(n int) ([]byte, error) {
	if n < 0 || n > 65535 {
		return nil, ErrLimit
	}
	b := make([]byte, n)
	_, err := io.ReadFull(c.r, b)
	return b, err
}
func (c *streamCursor) u32() (uint32, error) {
	b, err := c.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}
func newCursor(ctx context.Context, r io.Reader, size int64) *streamCursor {
	counted := &countStream{Reader: io.LimitReader(contextReader{ctx, r}, size+1)}
	return &streamCursor{bufio.NewReaderSize(counted, 64<<10), counted}
}

func noZIP64(extra []byte) bool {
	for len(extra) > 0 {
		if len(extra) < 4 {
			return false
		}
		id := binary.LittleEndian.Uint16(extra)
		n := int(binary.LittleEndian.Uint16(extra[2:]))
		extra = extra[4:]
		if n > len(extra) || id == 1 {
			return false
		}
		extra = extra[n:]
	}
	return true
}
func safeEntry(e sequentialEntry) error {
	if !safePath(strings.TrimSuffix(e.name, "/")) || e.flags&(1|64|8192) != 0 || (e.method != zip.Store && e.method != zip.Deflate) {
		return ErrInvalid
	}
	if e.size < 0 || e.size > MaxMember || e.compressed < 0 || e.compressed > MaxSource {
		return ErrLimit
	}
	if e.size > 1<<20 && e.size/max(int64(1), e.compressed) > 200 {
		return ErrLimit
	}
	return nil
}

func openSequentialArchive(ctx context.Context, source SequentialSource) (*archive, error) {
	if source.Size < 22 || source.Size > MaxSource || source.Open == nil {
		return nil, ErrLimit
	}
	body, err := source.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	c := newCursor(ctx, body, source.Size)
	stream := &sequentialArchive{source: source, entries: map[string]sequentialEntry{}}
	a := &archive{files: map[string]*archiveFile{}, sequential: stream}
	all := map[string]sequentialEntry{}
	var expanded int64
	for {
		offset := c.position()
		signature, err := c.u32()
		if err != nil {
			return nil, err
		}
		if signature == 0x02014b50 {
			return finishSequentialDirectory(c, source.Size, offset, signature, all, a)
		}
		if signature != 0x04034b50 || len(all) >= MaxEntries {
			return nil, ErrInvalid
		}
		h, err := c.bytes(26)
		if err != nil {
			return nil, err
		}
		e := sequentialEntry{offset: offset, flags: binary.LittleEndian.Uint16(h[2:]), method: binary.LittleEndian.Uint16(h[4:]), crc: binary.LittleEndian.Uint32(h[10:]), compressed: int64(binary.LittleEndian.Uint32(h[14:])), size: int64(binary.LittleEndian.Uint32(h[18:]))}
		nameLength, extraLength := int(binary.LittleEndian.Uint16(h[22:])), int(binary.LittleEndian.Uint16(h[24:]))
		if nameLength > 1024 {
			return nil, ErrLimit
		}
		name, err := c.bytes(nameLength)
		if err != nil {
			return nil, err
		}
		e.name = string(name)
		e.directory = strings.HasSuffix(e.name, "/")
		extra, err := c.bytes(extraLength)
		if err != nil {
			return nil, err
		}
		if !noZIP64(extra) {
			return nil, ErrInvalid
		}
		if _, duplicate := all[e.name]; duplicate {
			return nil, ErrInvalid
		}
		if err = safeEntry(e); err != nil {
			return nil, err
		}
		e.data = c.position()
		if e.flags&8 != 0 {
			// Deflate has a real end marker. A stored member with unknown extent
			// cannot be scanned safely by guessing where its descriptor begins.
			if e.method != zip.Deflate {
				return nil, ErrInvalid
			}
			decoder := flate.NewReader(c.r)
			hash := crc32.NewIEEE()
			n, readErr := io.Copy(hash, io.LimitReader(contextReader{ctx, decoder}, MaxMember+1))
			decoder.Close()
			if readErr != nil {
				return nil, readErr
			}
			if n > MaxMember {
				return nil, ErrLimit
			}
			e.size = n
			e.compressed = c.position() - e.data
			e.crc = hash.Sum32()
			crc, err := c.u32()
			if err != nil {
				return nil, err
			}
			if crc == 0x08074b50 {
				crc, err = c.u32()
				if err != nil {
					return nil, err
				}
			}
			compressed, err := c.u32()
			if err != nil {
				return nil, err
			}
			size, err := c.u32()
			if err != nil {
				return nil, err
			}
			if crc != e.crc || int64(compressed) != e.compressed || int64(size) != e.size {
				return nil, ErrInvalid
			}
		} else {
			if _, err = io.CopyN(io.Discard, contextReader{ctx, c.r}, e.compressed); err != nil {
				return nil, err
			}
		}
		if err = safeEntry(e); err != nil {
			return nil, err
		}
		expanded += e.size
		if expanded > MaxExpanded {
			return nil, ErrLimit
		}
		all[e.name] = e
		if !e.directory {
			a.files[e.name] = &archiveFile{UncompressedSize64: uint64(e.size)}
			a.order = append(a.order, e.name)
			stream.entries[e.name] = e
		}
		if c.position() > source.Size {
			return nil, ErrInvalid
		}
	}
}

func finishSequentialDirectory(c *streamCursor, size, start int64, signature uint32, all map[string]sequentialEntry, a *archive) (*archive, error) {
	seen := map[string]bool{}
	for signature == 0x02014b50 {
		if c.position()-start > MaxDirectory || len(seen) >= MaxEntries {
			return nil, ErrLimit
		}
		h, err := c.bytes(42)
		if err != nil {
			return nil, err
		}
		flags, method := binary.LittleEndian.Uint16(h[4:]), binary.LittleEndian.Uint16(h[6:])
		crc := binary.LittleEndian.Uint32(h[12:])
		compressed, expanded := int64(binary.LittleEndian.Uint32(h[16:])), int64(binary.LittleEndian.Uint32(h[20:]))
		offset := int64(binary.LittleEndian.Uint32(h[38:]))
		nameLength, extraLength, commentLength := int(binary.LittleEndian.Uint16(h[24:])), int(binary.LittleEndian.Uint16(h[26:])), int(binary.LittleEndian.Uint16(h[28:]))
		if nameLength > 1024 || binary.LittleEndian.Uint16(h[30:]) != 0 {
			return nil, ErrInvalid
		}
		name, err := c.bytes(nameLength)
		if err != nil {
			return nil, err
		}
		extra, err := c.bytes(extraLength)
		if err != nil {
			return nil, err
		}
		if !noZIP64(extra) {
			return nil, ErrInvalid
		}
		if _, err = c.bytes(commentLength); err != nil {
			return nil, err
		}
		e, ok := all[string(name)]
		if !ok || seen[e.name] || e.flags != flags || e.method != method || e.crc != crc || e.compressed != compressed || e.size != expanded || e.offset != offset {
			return nil, ErrInvalid
		}
		mode := binary.LittleEndian.Uint32(h[34:]) >> 16 & 0o170000
		if mode != 0 && mode != 0o100000 && mode != 0o040000 {
			return nil, ErrInvalid
		}
		if mode == 0o040000 && !e.directory {
			return nil, ErrInvalid
		}
		seen[e.name] = true
		signature, err = c.u32()
		if err != nil {
			return nil, err
		}
	}
	end := c.position() - 4
	if signature != 0x06054b50 || len(seen) != len(all) || end-start > MaxDirectory {
		return nil, ErrInvalid
	}
	h, err := c.bytes(18)
	if err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint16(h) != 0 || binary.LittleEndian.Uint16(h[2:]) != 0 || int(binary.LittleEndian.Uint16(h[4:])) != len(all) || int(binary.LittleEndian.Uint16(h[6:])) != len(all) || int64(binary.LittleEndian.Uint32(h[8:])) != end-start || int64(binary.LittleEndian.Uint32(h[12:])) != start {
		return nil, ErrInvalid
	}
	if _, err = c.bytes(int(binary.LittleEndian.Uint16(h[16:]))); err != nil {
		return nil, err
	}
	if c.position() != size {
		return nil, ErrChanged
	}
	var trailing [1]byte
	n, err := c.r.Read(trailing[:])
	if n != 0 || err != io.EOF {
		return nil, ErrChanged
	}
	return a, nil
}

func (s *sequentialArchive) read(ctx context.Context, name string, limit int64) ([]byte, error) {
	e, ok := s.entries[name]
	if !ok {
		return nil, ErrInvalid
	}
	if e.size > limit {
		return nil, ErrLimit
	}
	body, err := s.source.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	reader := contextReader{ctx, body}
	if _, err = io.CopyN(io.Discard, reader, e.data); err != nil {
		return nil, err
	}
	compressed := &io.LimitedReader{R: reader, N: e.compressed}
	var decoded io.Reader = compressed
	if e.method == zip.Deflate {
		r := flate.NewReader(compressed)
		defer r.Close()
		decoded = r
	}
	b, err := io.ReadAll(io.LimitReader(decoded, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != e.size || int64(len(b)) > limit || compressed.N != 0 || crc32.ChecksumIEEE(b) != e.crc {
		return nil, ErrChanged
	}
	return b, nil
}
