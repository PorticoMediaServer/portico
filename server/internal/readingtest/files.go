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
