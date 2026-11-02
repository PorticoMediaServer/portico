// Package readingtest supplies original, lawful publications for reader tests.
// It is imported by tests only; it is not a production content/data producer.
package readingtest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"unicode/utf16"
)

func EPUB() []byte { return epub(true) }

// EPUBWithoutCreator is the same lawful publication without an embedded
// creator, so a test can exercise folder-derived attribution alone.
func EPUBWithoutCreator() []byte { return epub(false) }

func epub(creator bool) []byte {
	metadata := `<dc:identifier id="bookid">urn:uuid:382df65f-89d0-496e-ab18-6670e4d27ad4</dc:identifier><dc:title>Original publication</dc:title>`
	if creator {
		metadata += `<dc:creator>Portico</dc:creator>`
	}
	metadata += `<dc:language>en</dc:language><meta property="dcterms:modified">2026-10-23T00:00:00Z</meta>`
	return archive([][2]string{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="EPUB/package.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"EPUB/package.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` + metadata + `</metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine page-progression-direction="ltr"><itemref idref="chapter"/></spine></package>`},
		{"EPUB/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`},
		{"EPUB/chapter.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" lang="en"><head><title>Original chapter</title></head><body><h1 id="chapter">Original chapter</h1><p>This original text is a small publication used to verify reading, source authorization and saved locations.</p></body></html>`},
	})
}

type comicMember struct {
	name string
	data []byte
}

func comicMembers() []comicMember {
	var picture bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 128, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 128; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 80, 255})
		}
	}
	if err := png.Encode(&picture, img); err != nil {
		panic(err)
	}
	page := picture.Bytes()
	return []comicMember{
		{"page1.png", page},
		{"page2.png", page},
		{"ComicInfo.xml", []byte(`<ComicInfo><Title>Original comic</Title><Writer>Portico</Writer><Manga>YesAndRightToLeft</Manga><LanguageISO>en</LanguageISO></ComicInfo>`)},
	}
}
func CBZ() []byte {
	members := comicMembers()
	entries := make([][2]string, 0, len(members))
	for _, member := range members {
		entries = append(entries, [2]string{member.name, string(member.data)})
	}
	return archive(entries)
}

// CBR is a genuine stored (method 0) RAR5 container, never a renamed ZIP. The
// writer below is the Go form of the fixture generator's CC0 RAR5 writer and is
// read by the server's pinned rardecode reader. Admission still needs a
// portico_rar7z build; an untagged build does not expose the format.
const (
	rarSignature = "Rar!\x1a\x07\x01\x00"
	rarBlockArc  = 1
	rarBlockFile = 2
	rarBlockEnd  = 5

	rarHasData    = 0x0002
	rarFileMTime  = 0x0002
	rarFileCRC    = 0x0004
	rarHostUnix   = 1
	rarMTimeEpoch = 1704067200 // 2024-01-01T00:00:00Z
)

func CBR() []byte {
	var b bytes.Buffer
	b.WriteString(rarSignature)
	b.Write(rarBlock(rarBlockArc, 0, rarVint(0), nil))
	for _, member := range comicMembers() {
		b.Write(rarFileBlock(member.name, member.data))
	}
	b.Write(rarBlock(rarBlockEnd, 0, rarVint(0), nil))
	return b.Bytes()
}

func rarVint(value uint64) []byte {
	out := make([]byte, 0, 8)
	for {
		part := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			part |= 0x80
		}
		out = append(out, part)
		if value == 0 {
			return out
		}
	}
}

func rarBlock(kind uint64, flags uint64, fields, data []byte) []byte {
	if len(data) > 0 {
		flags |= rarHasData
	}
	body := rarVint(kind)
	body = append(body, rarVint(flags)...)
	if len(data) > 0 {
		body = append(body, rarVint(uint64(len(data)))...)
	}
	body = append(body, fields...)
	size := rarVint(uint64(len(body)))
	headerCRC := crc32.NewIEEE()
	headerCRC.Write(size)
	headerCRC.Write(body)
	out := make([]byte, 0, 4+len(size)+len(body)+len(data))
	out = binary.LittleEndian.AppendUint32(out, headerCRC.Sum32())
	out = append(out, size...)
	out = append(out, body...)
	return append(out, data...)
}

func rarFileBlock(name string, payload []byte) []byte {
	fields := rarVint(rarFileMTime | rarFileCRC)
	fields = append(fields, rarVint(uint64(len(payload)))...)
	fields = append(fields, rarVint(0o100644)...)
	fields = binary.LittleEndian.AppendUint32(fields, rarMTimeEpoch)
	fields = binary.LittleEndian.AppendUint32(fields, crc32.ChecksumIEEE(payload))
	fields = append(fields, rarVint(0)...) // stored, non-solid
	fields = append(fields, rarVint(rarHostUnix)...)
	fields = append(fields, rarVint(uint64(len(name)))...)
	fields = append(fields, name...)
	return rarBlock(rarBlockFile, 0, fields, payload)
}

// CBR4 is a legacy RAR4 (2.9/3.x/4.x block format) comic whose pages are
// stored, as many real CBRs are; OPS-2's walk fixture is this shape. method is
// the RAR4 compression method byte for every member (0x30 stored) and flags are
// added to each file header (0x10 solid, 0x04 encrypted), so a test can build
// the variants the server refuses or routes to its worker. A compressed member
// built this way still carries the raw page bytes: it is only a header shape,
// not a decodable stream.
func CBR4(method byte, fileFlags uint16) []byte {
	var b bytes.Buffer
	b.WriteString("Rar!\x1a\x07\x00")
	b.Write(rar4Block(0x73, 0, make([]byte, 6), nil))
	for _, member := range comicMembers() {
		b.Write(rar4FileBlock(member.name, member.data, member.data, 20, method, fileFlags))
	}
	b.Write(rar4Block(0x7b, 0x4000, nil, nil))
	return b.Bytes()
}

// CBR4Compressed is the compressed RAR4 shape: two pages with the RAR 2.9 LZ
// method (0x33) in one non-solid archive. The bitstream is literal-only, so it
// is a legal RAR 2.9 stream produced without any third-party encoder.
func CBR4Compressed() []byte { return cbr4LZ(false) }

// CBR4Solid is the solid RAR4 shape: the archive's main block and the second
// file carry the solid flags, and the second page's literals continue the first
// page's window and tables as a solid decoder expects.
func CBR4Solid() []byte { return cbr4LZ(true) }

func cbr4LZ(solid bool) []byte {
	members := comicMembers()[:2]
	var b bytes.Buffer
	b.WriteString("Rar!\x1a\x07\x00")
	var mainFlags uint16
	if solid {
		mainFlags = 0x0008 // archive is solid
	}
	b.Write(rar4Block(0x73, mainFlags, make([]byte, 6), nil))
	for index, member := range members {
		var bits rar4BitWriter
		if !solid || index == 0 {
			rar4LZHeader(&bits)
		}
		for _, value := range member.data {
			bits.put(uint64(value), 9)
		}
		bits.put(256, 9) // end of block
		bits.put(0, 2)   // no more blocks in this file
		var fileFlags uint16
		if solid && index == 1 {
			fileFlags = 0x0010 // file is solid
		}
		b.Write(rar4FileBlock(member.name, member.data, bits.flush(), 29, 0x33, fileFlags))
	}
	b.Write(rar4Block(0x7b, 0x4000, nil, nil))
	return b.Bytes()
}

// rar4FileBlock writes one RAR4 file header (method 0x30 stored or a compressed
// method) whose packed bytes are already encoded.
func rar4FileBlock(name string, plain, packed []byte, unpackver, method byte, flags uint16) []byte {
	fields := binary.LittleEndian.AppendUint32(nil, uint32(len(packed)))
	fields = binary.LittleEndian.AppendUint32(fields, uint32(len(plain)))
	fields = append(fields, 3) // host OS: Unix
	fields = binary.LittleEndian.AppendUint32(fields, crc32.ChecksumIEEE(plain))
	fields = binary.LittleEndian.AppendUint32(fields, 0x5a210000) // DOS time
	fields = append(fields, unpackver, method)
	fields = binary.LittleEndian.AppendUint16(fields, uint16(len(name)))
	fields = binary.LittleEndian.AppendUint32(fields, 0o100644<<16)
	fields = append(fields, name...)
	return rar4Block(0x74, 0x8000|flags, fields, packed)
}

// rar4BitWriter writes the RAR 2.9 bit order: high bit of each byte first, as
// rardecode's rarBitReader reads it.
type rar4BitWriter struct {
	data  []byte
	value uint64
	bits  uint8
}

func (w *rar4BitWriter) put(value uint64, bits uint8) {
	if bits == 0 {
		return
	}
	w.value = w.value<<bits | value&(1<<bits-1)
	w.bits += bits
	for w.bits >= 8 {
		w.bits -= 8
		w.data = append(w.data, byte(w.value>>w.bits))
	}
	if w.bits == 0 {
		w.value = 0
	} else {
		w.value &= 1<<w.bits - 1
	}
}

// flush pads the last byte with zero bits; the decoder stops at the end marker.
func (w *rar4BitWriter) flush() []byte {
	if w.bits > 0 {
		w.data = append(w.data, byte(w.value<<(8-w.bits)))
		w.value, w.bits = 0, 0
	}
	return w.data
}

// rar4LZHeader writes the literal-only LZ block header: LZ mode, fresh tables,
// a 20-entry bit-length table giving the symbols 0, 9, 16 and 18 two bits each,
// and main code lengths of 9 for every literal plus the end-of-block symbol
// (codes 0..256, in symbol order).
func rar4LZHeader(w *rar4BitWriter) {
	w.put(0, 1) // LZ, not PPM
	w.put(0, 1) // new tables, not an update of the previous ones
	for _, length := range [20]byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 2, 0, 2, 0} {
		w.put(uint64(length), 4)
	}
	// Canonical two-bit codes: 00 = length 0, 01 = length 9, 10 = repeat the
	// previous length, 11 = repeat zero (3..10 times, as three bits plus three).
	w.put(1, 2)            // the first main symbol has code length 9
	rar4CodeRun(w, 2, 256) // the other 256 main symbols are also 9
	rar4CodeRun(w, 3, 147) // offset, low-offset and length tables stay empty
}

func rar4CodeRun(w *rar4BitWriter, symbol uint64, count int) {
	for count > 0 {
		run := min(count, 10)
		if remaining := count - run; remaining > 0 && remaining < 3 {
			run = count - 3
		}
		w.put(symbol, 2)
		w.put(uint64(run-3), 3)
		count -= run
	}
}

// rar4Block writes one RAR4 block: CRC16 (low half of the header's CRC32), type,
// flags, header size, then fields; a file block's packed size is its first field.
func rar4Block(kind byte, flags uint16, fields, data []byte) []byte {
	header := []byte{kind}
	header = binary.LittleEndian.AppendUint16(header, flags)
	header = binary.LittleEndian.AppendUint16(header, uint16(7+len(fields)))
	header = append(header, fields...)
	out := binary.LittleEndian.AppendUint16(nil, uint16(crc32.ChecksumIEEE(header)))
	out = append(out, header...)
	return append(out, data...)
}

// CB7 is a genuine 7z container whose members use the copy (stored) coder, so
// the server's bounded 7z worker decodes the same pages without compression.
// Admission and reading need the portico_cb7 build tag.
func CB7() []byte {
	members := comicMembers()
	var packed []byte
	for _, member := range members {
		packed = append(packed, member.data...)
	}
	header := sevenZipHeader(members)
	start := binary.LittleEndian.AppendUint64(nil, uint64(len(packed)))
	start = binary.LittleEndian.AppendUint64(start, uint64(len(header)))
	start = binary.LittleEndian.AppendUint32(start, crc32.ChecksumIEEE(header))
	var b bytes.Buffer
	b.WriteString("7z\xbc\xaf\x27\x1c")
	b.Write([]byte{0, 4}) // format version 0.4
	b.Write(binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE(start)))
	b.Write(start)
	b.Write(packed)
	b.Write(header)
	return b.Bytes()
}

func sevenZipHeader(members []comicMember) []byte {
	h := []byte{0x01, 0x04, 0x06} // Header, MainStreamsInfo, PackInfo
	h = append(h, sevenZipNumber(0)...)
	h = append(h, sevenZipNumber(uint64(len(members)))...)
	h = append(h, 0x09) // Size
	for _, member := range members {
		h = append(h, sevenZipNumber(uint64(len(member.data)))...)
	}
	h = append(h, 0x00)       // End PackInfo
	h = append(h, 0x07, 0x0B) // UnPackInfo, Folder
	h = append(h, sevenZipNumber(uint64(len(members)))...)
	h = append(h, 0x00) // external folders
	for range members {
		// One coder: flags advertise a one-byte id, and the copy coder is 0x00.
		h = append(h, 0x01, 0x01, 0x00)
	}
	h = append(h, 0x0C) // CodersUnPackSize
	for _, member := range members {
		h = append(h, sevenZipNumber(uint64(len(member.data)))...)
	}
	h = append(h, 0x00)       // End UnPackInfo
	h = append(h, 0x08, 0x0D) // SubStreamsInfo, NumUnpackStream
	for range members {
		h = append(h, 0x01)
	}
	h = append(h, 0x00) // End SubStreamsInfo
	h = append(h, 0x00) // End StreamsInfo
	h = append(h, 0x05) // FilesInfo
	h = append(h, sevenZipNumber(uint64(len(members)))...)
	var names []byte
	for _, member := range members {
		for _, unit := range utf16.Encode([]rune(member.name)) {
			names = append(names, byte(unit), byte(unit>>8))
		}
		names = append(names, 0, 0)
	}
	h = append(h, 0x11) // Name
	h = append(h, sevenZipNumber(uint64(len(names)+1))...)
	h = append(h, 0x00) // external names
	h = append(h, names...)
	h = append(h, 0x00) // End FilesInfo
	h = append(h, 0x00) // End Header
	return h
}

func sevenZipNumber(value uint64) []byte {
	out := make([]byte, 0, 9)
	var first byte
	mask := byte(0x80)
	extra := 0
	for extra = 0; extra < 8; extra++ {
		if value < uint64(1)<<(7*(extra+1)) {
			first |= byte(value >> (8 * extra))
			break
		}
		first |= mask
		mask >>= 1
	}
	out = append(out, first)
	for i := 0; i < extra; i++ {
		out = append(out, byte(value>>(8*i)))
	}
	return out
}
func PDF() []byte {
	var b bytes.Buffer
	fmt.Fprint(&b, "%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	text := "BT /F1 12 Tf 20 100 Td (Original Portico reading fixture.) Tj ET\n"
	objects := []string{`<< /Type /Catalog /Pages 2 0 R >>`, `<< /Type /Pages /Kids [3 0 R] /Count 1 >>`, `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>`, `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(text), text)}
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return b.Bytes()
}
func archive(entries [][2]string) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e[0], Method: zip.Deflate}
		var w interface{ Write([]byte) (int, error) }
		var err error
		if e[0] == "mimetype" {
			h.Method = zip.Store
			h.CRC32 = crc32.ChecksumIEEE([]byte(e[1]))
			h.CompressedSize64 = uint64(len(e[1]))
			h.UncompressedSize64 = uint64(len(e[1]))
			w, err = z.CreateRaw(h)
		} else {
			w, err = z.CreateHeader(h)
		}
		if err != nil {
			panic(err)
		}
		if _, err = w.Write([]byte(e[1])); err != nil {
			panic(err)
		}
	}
	if err := z.Close(); err != nil {
		panic(err)
	}
	return b.Bytes()
}
