package reading

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/bits"
)

// MOBI and AZW3 are read as the EPUB they are rebuilt into (10-reading rule 1): the book's own
// documents are made from the file's text records once and kept, so the EPUB reader, its
// contents, search and highlights work on them unchanged; the pictures stay in the file and are
// read from it when asked for. An AZW3 (and the newer half of a MOBI that has one) is KF8:
// XHTML files cut into skeletons and fragments, which are put back together. An older MOBI is
// one HTML text, split into parts at its page breaks and written out as XHTML. A book locked
// with DRM is not opened (rule 4).
const (
	FormatMOBI = "mobi"
	FormatAZW3 = "azw3"
)

// ErrLocked says the book is locked to another reading app (DRM).
var ErrLocked = errors.New("This book is locked to another reading app, so this server can't open it.")

const (
	maxMobiText    = 64 << 20 // a book's whole text, unpacked
	maxMobiRecord  = 1 << 20  // a header, text or index record
	maxMobiEntries = 100_000  // entries in one index
)

func mobiFormat(format string) bool { return format == FormatMOBI || format == FormatAZW3 }

// isMobi reports whether a file is a MOBI or AZW3, by its PalmDB type.
func isMobi(r io.ReaderAt, size int64) bool {
	var kind [8]byte
	_, err := r.ReadAt(kind[:], 60)
	return size >= 78 && err == nil && string(kind[:]) == "BOOKMOBI"
}

// ebookArchive opens an EPUB, or a MOBI or AZW3 as the EPUB it is rebuilt into.
func ebookArchive(r io.ReaderAt, size int64) (*archive, error) {
	if !isMobi(r, size) {
		return openArchive(r, size)
	}
	book, err := convertMobi(r, size)
	if err != nil {
		return nil, err
	}
	return book.archive(r, size)
}

// palm is a PalmDB file's list of records.
type palm struct {
	r       io.ReaderAt
	offsets []int64 // where each record starts, then the file's end
}

func openPalm(r io.ReaderAt, size int64) (*palm, error) {
	if !isMobi(r, size) {
		return nil, errDamaged
	}
	var count [2]byte
	if _, err := r.ReadAt(count[:], 76); err != nil {
		return nil, errDamaged
	}
	n := int(binary.BigEndian.Uint16(count[:]))
	list := make([]byte, 8*n)
	if got, _ := r.ReadAt(list, 78); n == 0 || got != len(list) {
		return nil, errDamaged
	}
	p := &palm{r: r}
	for i := range n {
		at := int64(binary.BigEndian.Uint32(list[8*i:]))
		if at < 78 || at > size || i > 0 && at < p.offsets[i-1] {
			return nil, errDamaged
		}
		p.offsets = append(p.offsets, at)
	}
	p.offsets = append(p.offsets, size)
	return p, nil
}

func (p *palm) count() int { return len(p.offsets) - 1 }

// record reads record i whole, or its first limit bytes when head is set; a record larger
// than limit is otherwise refused.
func (p *palm) record(i int, limit int64, head bool) ([]byte, error) {
	if i < 0 || i >= p.count() {
		return nil, errDamaged
	}
	n := p.offsets[i+1] - p.offsets[i]
	if n > limit {
		if !head {
			return nil, errDamaged
		}
		n = limit
	}
	b := make([]byte, n)
	if got, _ := p.r.ReadAt(b, p.offsets[i]); int64(got) != n {
		return nil, errDamaged
	}
	return b, nil
}

// mobiHeader is what a book's header record says. Record numbers are the file's own, -1 for none.
type mobiHeader struct {
	base, compression, encryption, textRecords int
	textLength                                 int64
	encoding, version                          int
	extraFlags                                 uint16
	firstResource, ncx, fdst, skeleton, frag   int
	guide                                      int
	title                                      string
	exth                                       map[uint32][][]byte
}

func readMobiHeader(p *palm, base int) (*mobiHeader, error) {
	b, err := p.record(base, maxMobiRecord, false)
	if err != nil || len(b) < 0x84 || string(b[16:20]) != "MOBI" {
		return nil, errDamaged
	}
	length := 16 + int(binary.BigEndian.Uint32(b[20:]))
	if length > len(b) || length < 0x84 {
		return nil, errDamaged
	}
	u32 := func(at int) uint32 {
		if at+4 > length {
			return 0xffffffff
		}
		return binary.BigEndian.Uint32(b[at:])
	}
	record := func(at int) int {
		v := u32(at)
		if v == 0xffffffff || int(v) >= p.count()-base {
			return -1
		}
		return base + int(v)
	}
	h := &mobiHeader{base: base, compression: int(binary.BigEndian.Uint16(b[0:])), textLength: int64(binary.BigEndian.Uint32(b[4:])),
		textRecords: int(binary.BigEndian.Uint16(b[8:])), encryption: int(binary.BigEndian.Uint16(b[12:])),
		encoding: int(u32(28)), version: int(u32(36)), firstResource: record(0x6c), ncx: -1, fdst: -1, skeleton: -1, frag: -1, guide: -1,
		exth: map[uint32][][]byte{}}
	if length >= 0xf4 {
		h.extraFlags = binary.BigEndian.Uint16(b[0xf2:])
	}
	h.ncx = record(0xf4)
	if h.version >= 8 {
		if u32(0xc4) > 1 {
			h.fdst = record(0xc0)
		}
		h.frag, h.skeleton, h.guide = record(0xf8), record(0xfc), record(0x104)
	}
	if at, n := int(u32(0x54)), int(u32(0x58)); at > 0 && n > 0 && n < 4096 && at+n <= len(b) {
		h.title = string(b[at : at+n])
	}
	if e := b[length:]; u32(0x80)&0x40 != 0 && len(e) >= 12 && string(e[:4]) == "EXTH" {
		count, at := int(binary.BigEndian.Uint32(e[8:])), 12
		for range min(count, 4096) {
			if at+8 > len(e) {
				break
			}
			kind, n := binary.BigEndian.Uint32(e[at:]), int(binary.BigEndian.Uint32(e[at+4:]))
			if n < 8 || at+n > len(e) {
				break
			}
			h.exth[kind] = append(h.exth[kind], e[at+8:at+n])
			at += n
		}
	}
	return h, nil
}

// number is the first value of a numeric EXTH record.
func (h *mobiHeader) number(kind uint32) (int, bool) {
	if v := h.exth[kind]; len(v) > 0 && len(v[0]) == 4 {
		return int(binary.BigEndian.Uint32(v[0])), true
	}
	return 0, false
}

// text unpacks the book's text records.
func (h *mobiHeader) text(p *palm) ([]byte, error) {
	switch {
	case h.encryption != 0:
		return nil, ErrLocked
	case h.compression != 1 && h.compression != 2:
		// Huffman-compressed books (kindlegen's -c2) are rare and not read.
		return nil, ErrFormatLater
	case h.textLength > maxMobiText:
		return nil, errDamaged
	}
	out := make([]byte, 0, h.textLength)
	for i := 1; i <= h.textRecords; i++ {
		rec, err := p.record(h.base+i, maxMobiRecord, false)
		if err != nil {
			return nil, err
		}
		rec = rec[:len(rec)-trailing(rec, h.extraFlags)]
		if h.compression == 1 {
			out = append(out, rec...)
		} else if out, err = palmDOC(out, rec); err != nil {
			return nil, err
		}
		if len(out) > maxMobiText {
			return nil, errDamaged
		}
	}
	return out[:min(int64(len(out)), h.textLength)], nil
}

// trailing is how many bytes at the end of a text record are entries the flags say follow
// the text, and not text.
func trailing(rec []byte, flags uint16) int {
	size := 0
	for f := flags >> 1; f != 0 && size < len(rec); f >>= 1 {
		if f&1 == 0 {
			continue
		}
		// Each entry ends with its own size, written backwards seven bits at a time.
		n, shift := 0, 0
		for k := len(rec) - size - 1; k >= 0 && shift < 28; k-- {
			n |= int(rec[k]&0x7f) << shift
			shift += 7
			if rec[k]&0x80 != 0 {
				break
			}
		}
		size += n
	}
	if flags&1 != 0 && size < len(rec) {
		size += int(rec[len(rec)-size-1]&3) + 1
	}
	return min(size, len(rec))
}

// palmDOC unpacks one PalmDOC-compressed record onto out.
func palmDOC(out, in []byte) ([]byte, error) {
	for i := 0; i < len(in); {
		c := in[i]
		i++
		switch {
		case c >= 1 && c <= 8:
			if i+int(c) > len(in) {
				return nil, errDamaged
			}
			out = append(out, in[i:i+int(c)]...)
			i += int(c)
		case c < 0x80:
			out = append(out, c)
		case c >= 0xc0:
			out = append(out, ' ', c^0x80)
		default:
			if i >= len(in) {
				return nil, errDamaged
			}
			pair := int(c)<<8 | int(in[i])
			i++
			distance, n := pair>>3&0x7ff, pair&7+3
			if distance == 0 || distance > len(out) {
				return nil, errDamaged
			}
			for range n {
				out = append(out, out[len(out)-distance])
			}
		}
	}
	return out, nil
}

// mobiEntry is one entry of an index: its name and its tagged numbers.
type mobiEntry struct {
	name string
	tags map[byte][]int
}

// readIndex reads an index (INDX records) and the strings (CNCX records) its entries point at,
// by their place: record number<<16 + offset.
func readIndex(p *palm, at int) ([]mobiEntry, map[int]string, error) {
	head, err := p.record(at, maxMobiRecord, false)
	if err != nil || len(head) < 56 || string(head[:4]) != "INDX" {
		return nil, nil, errDamaged
	}
	be := binary.BigEndian.Uint32
	tagx, records, stringRecords := int(be(head[4:])), int(be(head[24:])), int(be(head[52:]))
	if records > 4096 || stringRecords > 4096 || tagx+12 > len(head) || string(head[tagx:tagx+4]) != "TAGX" {
		return nil, nil, errDamaged
	}
	tagxEnd, controlBytes := tagx+int(be(head[tagx+4:])), int(be(head[tagx+8:]))
	if tagxEnd > len(head) || controlBytes < 0 || controlBytes > 8 {
		return nil, nil, errDamaged
	}
	var table [][4]byte
	for i := tagx + 12; i+4 <= tagxEnd; i += 4 {
		table = append(table, [4]byte(head[i:i+4]))
	}
	strs := map[int]string{}
	for j := range stringRecords {
		rec, err := p.record(at+records+1+j, maxMobiRecord, false)
		if err != nil {
			return nil, nil, err
		}
		for pos := 0; pos < len(rec) && rec[pos] != 0; {
			start := pos
			n, used := varint(rec[pos:])
			if used == 0 || pos+used+n > len(rec) {
				break
			}
			pos += used
			strs[j<<16|start] = string(rec[pos : pos+n])
			pos += n
		}
	}
	var entries []mobiEntry
	for r := 1; r <= records; r++ {
		data, err := p.record(at+r, maxMobiRecord, false)
		if err != nil || len(data) < 28 || string(data[:4]) != "INDX" {
			return nil, nil, errDamaged
		}
		idxt, count := int(be(data[20:])), int(be(data[24:]))
		if idxt < 0 || count < 0 || idxt+4+2*count > len(data) || len(entries)+count > maxMobiEntries {
			return nil, nil, errDamaged
		}
		starts := make([]int, count+1)
		for j := range count {
			starts[j] = int(binary.BigEndian.Uint16(data[idxt+4+2*j:]))
		}
		starts[count] = idxt
		for j := range count {
			start, end := starts[j], starts[j+1]
			if start >= end || end > len(data) || start+1+int(data[start]) > end {
				return nil, nil, errDamaged
			}
			n := int(data[start])
			entries = append(entries, mobiEntry{name: string(data[start+1 : start+1+n]), tags: tagValues(table, controlBytes, data[start+1+n:end])})
		}
	}
	return entries, strs, nil
}

// varint reads a number written seven bits a byte, the last byte marked by its high bit.
func varint(b []byte) (value, used int) {
	for used < len(b) && used < 5 {
		c := b[used]
		used++
		value = value<<7 | int(c&0x7f)
		if c&0x80 != 0 {
			return value, used
		}
	}
	return 0, 0
}

// tagValues reads an index entry's numbers, as its index's tag table says they are written:
// control bytes saying which tags are present and how many values each has, then the values.
func tagValues(table [][4]byte, controlBytes int, b []byte) map[byte][]int {
	out := map[byte][]int{}
	if len(b) < controlBytes {
		return out
	}
	type present struct {
		tag                byte
		count, length, per int
	}
	var tags []present
	pos, control := controlBytes, 0
	for _, t := range table {
		tag, per, mask, last := t[0], int(t[1]), t[2], t[3]
		if last == 1 {
			control++
			continue
		}
		if control >= controlBytes || mask == 0 {
			break
		}
		v := b[control] & mask
		switch {
		case v == 0:
		case v == mask && bits.OnesCount8(mask) > 1:
			// The values' length in bytes follows the control bytes.
			n, used := varint(b[min(pos, len(b)):])
			if used == 0 {
				return out
			}
			pos += used
			tags = append(tags, present{tag: tag, count: -1, length: n, per: per})
		default:
			for mask&1 == 0 {
				mask, v = mask>>1, v>>1
			}
			tags = append(tags, present{tag: tag, count: int(v), per: per})
		}
	}
	for _, t := range tags {
		var values []int
		for consumed := 0; t.count >= 0 && len(values) < min(t.count*t.per, 1024) || t.count < 0 && consumed < t.length; {
			n, used := varint(b[min(pos, len(b)):])
			if used == 0 {
				return out
			}
			pos, consumed = pos+used, consumed+used
			values = append(values, n)
		}
		out[t.tag] = values
	}
	return out
}

// mobiBook is a MOBI or AZW3 rebuilt as an EPUB: its documents, kept, and its pictures, by
// the record of the file they are read from.
type mobiBook struct {
	names    []string
	docs     map[string][]byte
	pictures map[string]int
}

// archive is the rebuilt book as the reader reads an EPUB, its pictures read from the file.
func (m *mobiBook) archive(r io.ReaderAt, size int64) (*archive, error) {
	p, err := openPalm(r, size)
	if err != nil {
		return nil, err
	}
	a := &archive{entries: map[string]*archiveEntry{}, names: m.names, close: func() {}}
	for _, name := range m.names {
		if doc, ok := m.docs[name]; ok {
			a.entries[name] = &archiveEntry{UncompressedSize64: uint64(len(doc)), Open: func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(doc)), nil
			}}
			continue
		}
		rec := m.pictures[name]
		if rec >= p.count() {
			return nil, errDamaged
		}
		a.entries[name] = &archiveEntry{UncompressedSize64: uint64(p.offsets[rec+1] - p.offsets[rec]), Open: func() (io.ReadCloser, error) {
			b, err := p.record(rec, maxPartSize, false)
			if err != nil {
				return nil, err
			}
			return io.NopCloser(bytes.NewReader(b)), nil
		}}
	}
	return a, nil
}
