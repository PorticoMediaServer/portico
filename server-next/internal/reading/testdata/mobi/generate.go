//go:build ignore

// generate writes the tiny MOBI and AZW3 books the reading tests open: run it in this folder
// with `go run generate.go`. Every byte is made here; the text and picture are original.
//
//   - older.mobi: an older MOBI (version 6), Windows-1252 text, PalmDOC-compressed, with
//     trailing record entries, a page break, a link to a place (filepos), a picture
//     (recindex), a contents index and a cover.
//   - locked.mobi: the same, marked as locked (DRM).
//   - kf8.azw3: an AZW3 (KF8): two XHTML files kept as skeletons and fragments, a style sheet
//     flow, a link to a place (kindle:pos), a picture (kindle:embed) and a contents index.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
)

func main() {
	older, locked := olderMobi()
	write("older.mobi", older)
	write("locked.mobi", locked)
	write("kf8.azw3", kf8())
}

func write(name string, b []byte) {
	if err := os.WriteFile(name, b, 0o644); err != nil {
		panic(err)
	}
}

func picture() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 6))
	for i := range 24 {
		img.Set(i%4, i/4, color.NRGBA{uint8(i * 10), 80, 160, 255})
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func olderMobi() (plain, locked []byte) {
	// Positions are byte offsets in this text, so it is put together in two passes.
	text := ""
	for pass := 0; pass < 2; pass++ {
		chapter := strings.Index(text, "<p>Chapter Two")
		text = fmt.Sprintf(`<html><head><guide><reference type="toc" title="Contents" filepos=%010d /></guide></head><body>`+
			"<p>Caf\xe9 by the river. <a filepos=%010d>Go to chapter two</a></p><p>abcabcabcabcabc</p><img recindex=\"00001\">"+
			`<mbp:pagebreak/><p>Chapter Two</p><p>The end.</p></body></html>`, 0, chapter)
	}
	chapter := strings.Index(text, "<p>Chapter Two")
	ncx := index([]entry{{"0", map[byte][]int{1: {0}, 3: {0}, 4: {0}}}, {"1", map[byte][]int{1: {chapter}, 3: {4}, 4: {0}}}},
		[][]string{{"One", "Two"}})
	exth := exthRecords(map[uint32][]byte{100: []byte("Ann Example"), 503: []byte("A Tiny Older Book"), 201: u32(0)})
	// The text record: compressed text, then a multibyte entry and one more trailing entry.
	record := append(palmDOC([]byte(text)), 0x00, 0x81)
	records := [][]byte{nil, record}
	ncxAt := len(records)
	records = append(records, ncx...)
	pictureAt := len(records)
	records = append(records, picture())
	for _, lock := range []uint16{0, 2} {
		records[0] = header(6, len(text), 1, 1252, lock, map[int]int{0x6c: pictureAt, 0xf4: ncxAt}, exth, "A Tiny Older Book")
		if lock == 0 {
			plain = palmDB("Tiny_Older", records)
		} else {
			locked = palmDB("Tiny_Locked", records)
		}
	}
	return plain, locked
}

func kf8() []byte {
	const skeleton = `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head>` +
		`<link rel="stylesheet" type="text/css" href="kindle:flow:0001?mime=text/css"/></head><body aid="0"></body></html>`
	fragments := []string{
		`<p>One. <a href="kindle:pos:fid:0001:off:0000000000">To two</a> <img src="kindle:embed:0001?mime=image/png" alt=""/></p>`,
		`<h1 id="two">Two</h1><p>The end.</p>`,
	}
	insert := strings.Index(skeleton, `<body aid="0">`) + len(`<body aid="0">`)
	var flow0 string
	var skels, frags []entry
	for i, f := range fragments {
		start := len(flow0)
		flow0 += skeleton + f
		skels = append(skels, entry{fmt.Sprintf("SKEL%010d", i), map[byte][]int{1: {1}, 6: {start, len(skeleton)}}})
		frags = append(frags, entry{fmt.Sprintf("%010d", start+insert), map[byte][]int{6: {i, len(f)}}})
	}
	css := "p { margin: 0 }"
	text := flow0 + css
	ncx := index([]entry{{"0", map[byte][]int{3: {0}, 4: {0}, 6: {0, 0}}}, {"1", map[byte][]int{3: {4}, 4: {0}, 6: {1, 0}}}},
		[][]string{{"One", "Two"}})
	records := [][]byte{nil}
	for i := 0; i < len(text); i += 4096 {
		records = append(records, append(palmDOC([]byte(text[i:min(i+4096, len(text))])), 0x00, 0x81))
	}
	textRecords := len(records) - 1
	at := map[int]int{}
	at[0xfc] = len(records)
	records = append(records, index(skels, nil)...)
	at[0xf8] = len(records)
	records = append(records, index(frags, nil)...)
	at[0xf4] = len(records)
	records = append(records, ncx...)
	at[0x6c] = len(records)
	records = append(records, picture())
	at[0xc0] = len(records)
	fdst := append([]byte("FDST"), u32(12)...)
	fdst = append(fdst, u32(2)...)
	for _, n := range []int{0, len(flow0), len(flow0), len(text)} {
		fdst = append(fdst, u32(n)...)
	}
	records = append(records, fdst)
	exth := exthRecords(map[uint32][]byte{100: []byte("Bo Example"), 503: []byte("A Tiny KF8 Book"), 201: u32(0)})
	records[0] = header(8, len(text), textRecords, 65001, 0, at, exth, "A Tiny KF8 Book")
	binary.BigEndian.PutUint32(records[0][0xc4:], 2)
	return palmDB("Tiny_KF8", records)
}

func u32(n int) []byte { return binary.BigEndian.AppendUint32(nil, uint32(n)) }

// header is a book's header record: PalmDOC header, MOBI header, EXTH and full name. Record
// numbers not given are 0xffffffff.
func header(version, textLength, textRecords, encoding int, lock uint16, records map[int]int, exth []byte, name string) []byte {
	const length = 0x108
	b := make([]byte, 16+length)
	binary.BigEndian.PutUint16(b[0:], 2)
	binary.BigEndian.PutUint32(b[4:], uint32(textLength))
	binary.BigEndian.PutUint16(b[8:], uint16(textRecords))
	binary.BigEndian.PutUint16(b[10:], 4096)
	binary.BigEndian.PutUint16(b[12:], lock)
	copy(b[16:], "MOBI")
	for at, v := range map[int]uint32{20: length, 24: 2, 28: uint32(encoding), 36: uint32(version), 0x80: 0x40} {
		binary.BigEndian.PutUint32(b[at:], v)
	}
	for _, at := range []int{0x6c, 0xc0, 0xf4, 0xf8, 0xfc, 0x104} {
		binary.BigEndian.PutUint32(b[at:], 0xffffffff)
	}
	for at, record := range records {
		binary.BigEndian.PutUint32(b[at:], uint32(record))
	}
	binary.BigEndian.PutUint16(b[0xf2:], 3)
	b = append(b, exth...)
	binary.BigEndian.PutUint32(b[0x54:], uint32(len(b)))
	binary.BigEndian.PutUint32(b[0x58:], uint32(len(name)))
	return append(b, name+"\x00\x00"...)
}

func exthRecords(values map[uint32][]byte) []byte {
	var body []byte
	for _, kind := range []uint32{100, 201, 503} {
		v := values[kind]
		body = binary.BigEndian.AppendUint32(body, kind)
		body = binary.BigEndian.AppendUint32(body, uint32(8+len(v)))
		body = append(body, v...)
	}
	out := append([]byte("EXTH"), u32(12+len(body))...)
	out = append(out, u32(len(values))...)
	out = append(out, body...)
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	return out
}

func palmDB(name string, records [][]byte) []byte {
	head := make([]byte, 78)
	copy(head, name)
	copy(head[60:], "BOOKMOBI")
	binary.BigEndian.PutUint16(head[76:], uint16(len(records)))
	at := 78 + 8*len(records) + 2
	for i, r := range records {
		head = binary.BigEndian.AppendUint32(head, uint32(at))
		head = binary.BigEndian.AppendUint32(head, uint32(i*2))
		at += len(r)
	}
	head = append(head, 0, 0)
	for _, r := range records {
		head = append(head, r...)
	}
	return head
}

// palmDOC compresses as PalmDOC does: a repeat of three or more bytes seen before becomes a
// distance and length, a byte that would be read as a code is escaped, the rest are as they are.
func palmDOC(in []byte) []byte {
	var out []byte
	for i := 0; i < len(in); {
		best, distance := 0, 0
		for d := 1; d <= min(i, 2047); d++ {
			n := 0
			for n < 10 && i+n < len(in) && in[i+n] == in[i+n-d] {
				n++
			}
			if n > best {
				best, distance = n, d
			}
		}
		if best >= 3 {
			pair := 0x8000 | distance<<3 | (best - 3)
			out = append(out, byte(pair>>8), byte(pair))
			i += best
			continue
		}
		if c := in[i]; c >= 1 && c <= 8 || c >= 0x80 {
			out = append(out, 1, c)
		} else {
			out = append(out, c)
		}
		i++
	}
	return out
}

type entry struct {
	name string
	tags map[byte][]int
}

// index writes an index: its header record with the tag table, one record of entries, and the
// records of strings (CNCX), each a list of strings placed one after another.
func index(entries []entry, strs [][]string) [][]byte {
	// Tag, values per entry, mask: one control byte; each tag present has one entry of values.
	table := [][3]byte{{1, 1, 0x01}, {3, 1, 0x02}, {4, 1, 0x04}, {6, 2, 0x08}}
	tagx := append([]byte("TAGX"), u32(12+4*len(table)+4)...)
	tagx = append(tagx, u32(1)...)
	for _, t := range table {
		tagx = append(tagx, t[0], t[1], t[2], 0)
	}
	tagx = append(tagx, 0, 0, 0, 1)
	indx := func(start, count, total, strings int) []byte {
		h := make([]byte, 0xc0)
		copy(h, "INDX")
		for at, v := range map[int]int{4: 0xc0, 20: start, 24: count, 28: 65001, 36: total, 52: strings} {
			binary.BigEndian.PutUint32(h[at:], uint32(v))
		}
		return h
	}
	head := append(indx(0, 1, len(entries), len(strs)), tagx...)
	data := indx(0, len(entries), 0, 0)
	var offsets []int
	for _, e := range entries {
		offsets = append(offsets, len(data))
		data = append(data, byte(len(e.name)))
		data = append(data, e.name...)
		control := byte(0)
		var values []byte
		for _, t := range table {
			if v, ok := e.tags[t[0]]; ok {
				control |= t[2]
				for _, n := range v {
					values = append(values, varint(n)...)
				}
			}
		}
		data = append(data, control)
		data = append(data, values...)
	}
	binary.BigEndian.PutUint32(data[20:], uint32(len(data)))
	data = append(data, "IDXT"...)
	for _, at := range offsets {
		data = binary.BigEndian.AppendUint16(data, uint16(at))
	}
	out := [][]byte{head, data}
	for _, list := range strs {
		var rec []byte
		for _, s := range list {
			rec = append(rec, varint(len(s))...)
			rec = append(rec, s...)
		}
		out = append(out, rec)
	}
	return out
}

// varint writes a number seven bits a byte, the last byte marked by its high bit.
func varint(n int) []byte {
	out := []byte{byte(n&0x7f) | 0x80}
	for n >>= 7; n > 0; n >>= 7 {
		out = append([]byte{byte(n & 0x7f)}, out...)
	}
	return out
}
