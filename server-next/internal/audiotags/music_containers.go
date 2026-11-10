package audiotags

import (
	"encoding/binary"
	"strconv"
	"strings"
	"unicode/utf16"
)

// DSDIFF uses 64-bit big-endian chunk lengths. Skip all audio payloads.
func (p *reader) dff() {
	for off := int64(16); off+12 <= p.size; {
		h := p.at(off, 12)
		if len(h) != 12 {
			return
		}
		n := binary.BigEndian.Uint64(h[4:])
		if n > uint64(p.size-off-12) {
			p.warn()
			return
		}
		start := off + 12
		switch string(h[:4]) {
		case "ID3 ":
			p.id3(start)
		case "DIIN":
			for c := start; c+12 <= start+int64(n); {
				v := p.at(c, 12)
				if len(v) != 12 {
					return
				}
				l := binary.BigEndian.Uint64(v[4:])
				if l > uint64(start+int64(n)-c-12) {
					p.warn()
					return
				}
				if string(v[:4]) == "DITI" || string(v[:4]) == "DIAR" {
					b := p.at(c+12, int64(l))
					if len(b) >= 4 {
						count := binary.BigEndian.Uint32(b)
						if count <= uint32(len(b)-4) {
							key := "title"
							if string(v[:4]) == "DIAR" {
								key = "artist"
							}
							p.add(key, string(b[4:4+count]))
						}
					}
				}
				c += 12 + int64(l) + int64(l%2)
			}
		}
		off = start + int64(n) + int64(n%2)
	}
}
func asfText(b []byte) string {
	if len(b)%2 != 0 {
		return ""
	}
	v := make([]uint16, len(b)/2)
	for n := range v {
		v[n] = binary.LittleEndian.Uint16(b[2*n:])
	}
	return strings.TrimRight(string(utf16.Decode(v)), "\x00")
}

var asfContent = []byte{0x33, 0x26, 0xb2, 0x75, 0x8e, 0x66, 0xcf, 0x11, 0xa6, 0xd9, 0, 0xaa, 0, 0x62, 0xce, 0x6c}
var asfExtended = []byte{0x40, 0xa4, 0xd0, 0xd2, 0x07, 0xe3, 0xd2, 0x11, 0x97, 0xf0, 0, 0xa0, 0xc9, 0x5e, 0xa8, 0x50}

func (p *reader) asf() { p.asfObjects(30, p.size, 0) }
func (p *reader) asfObjects(start, end int64, depth int) {
	if depth > 4 {
		return
	}
	for off := start; off+24 <= end && p.nodes < 10000; {
		p.nodes++
		h := p.at(off, 24)
		if len(h) != 24 {
			return
		}
		n := binary.LittleEndian.Uint64(h[16:])
		if n < 24 || n > uint64(end-off) {
			p.warn()
			return
		}
		key := string(h[:16])
		if key == "\x5f\xbf\x03\xb5\xa9\x2e\xcf\x11\x8e\xe3\x00\xc0\x0c\x20\x53\x65" && n >= 46 {
			header := p.at(off+24, 22)
			if len(header) == 22 {
				size := int64(binary.LittleEndian.Uint32(header[18:]))
				if size <= int64(n)-46 {
					p.asfObjects(off+46, off+46+size, depth+1)
				}
			}
		}
		if key == "\xea\xcb\xf8\xc5\xaf\x5b\x77\x48\x84\x67\xaa\x8c\x44\xfa\x4c\xca" || key == "\x94\x1c\x23\x44\x98\x94\xd1\x49\xa1\x41\x1d\x13\x4e\x45\x70\x54" {
			p.asfMetadata(p.at(off+24, int64(n)-24))
		}
		if key == string(asfContent) || key == string(asfExtended) {
			b := p.at(off+24, int64(n)-24)
			if key == string(asfContent) && len(b) >= 10 {
				pos := 10
				for i, k := range []string{"title", "artist", "copyright", "description", "rating"} {
					l := int(binary.LittleEndian.Uint16(b[i*2:]))
					if l > len(b)-pos {
						p.warn()
						return
					}
					p.add(k, asfText(b[pos:pos+l]))
					pos += l
				}
			}
			if key == string(asfExtended) && len(b) >= 2 {
				count := int(binary.LittleEndian.Uint16(b))
				pos := 2
				for i := 0; i < count && i < maxValues; i++ {
					if pos+2 > len(b) {
						p.warn()
						return
					}
					l := int(binary.LittleEndian.Uint16(b[pos:]))
					pos += 2
					if l > len(b)-pos {
						p.warn()
						return
					}
					name := asfText(b[pos : pos+l])
					pos += l
					if pos+4 > len(b) {
						p.warn()
						return
					}
					typ := binary.LittleEndian.Uint16(b[pos:])
					size := int(binary.LittleEndian.Uint16(b[pos+2:]))
					pos += 4
					if size > len(b)-pos {
						p.warn()
						return
					}
					v := b[pos : pos+size]
					pos += size
					name = strings.TrimPrefix(name, "WM/")
					switch name {
					case "AlbumTitle":
						name = "album"
					case "AlbumArtist":
						name = "album_artist"
					case "TrackNumber":
						name = "track"
					case "Year":
						name = "date"
					case "Genre":
						name = "genre"
					case "Composer":
						name = "composer"
					}
					if typ == 1 && name == "Picture" {
						p.asfPicture(v)
					} else if typ == 0 {
						p.add(name, asfText(v))
					} else if typ == 3 && len(v) == 4 {
						p.add(name, strconv.FormatUint(uint64(binary.LittleEndian.Uint32(v)), 10))
					}
				}
			}
		}
		off += int64(n)
	}
}

// Walk only known metadata masters, never Cluster or media packets.
func (p *reader) ebml(start, end int64, depth int) {
	if depth > 12 {
		return
	}
	for off := start; off < end && p.nodes < 10000; {
		p.nodes++
		id, ni, ok := p.vint(off, true)
		if !ok {
			return
		}
		size, ns, ok := p.vint(off+int64(ni), false)
		if !ok {
			return
		}
		body := off + int64(ni+ns)
		if size < 0 {
			size = end - body
		}
		if size > end-body || size < 0 {
			p.warn()
			return
		}
		next := body + size
		switch id {
		case 0x18538067, 0x1254c367, 0x7373, 0x1941a469, 0x1549a966:
			p.ebml(body, next, depth+1)
		case 0x67c8:
			p.simpleTag(body, next, depth+1)
		case 0x7ba9:
			p.add("title", string(p.at(body, size)))
		case 0x61a7:
			p.mkaAttachment(body, next)
		}
		if next <= off {
			return
		}
		off = next
	}
}
func (p *reader) vint(off int64, id bool) (int64, int, bool) {
	b := p.at(off, 1)
	if len(b) != 1 || b[0] == 0 {
		return 0, 0, false
	}
	n, mask := 1, byte(0x80)
	for b[0]&mask == 0 {
		n++
		mask >>= 1
	}
	if n > 8 || id && n > 4 {
		return 0, 0, false
	}
	raw := p.at(off, int64(n))
	if len(raw) != n {
		return 0, 0, false
	}
	v := int64(raw[0])
	if !id {
		v &= int64(mask - 1)
	}
	unknown := !id && raw[0]&(mask-1) == mask-1
	for _, x := range raw[1:] {
		v = v<<8 | int64(x)
		unknown = unknown && x == 255
	}
	if unknown {
		return -1, n, true
	}
	return v, n, true
}
func (p *reader) simpleTag(start, end int64, depth int) {
	name, value := "", ""
	for off := start; off < end && p.nodes < 10000; {
		p.nodes++
		id, ni, ok := p.vint(off, true)
		if !ok {
			return
		}
		n, ns, ok := p.vint(off+int64(ni), false)
		body := off + int64(ni+ns)
		if !ok || n < 0 || n > end-body {
			return
		}
		switch id {
		case 0x45a3:
			name = string(p.at(body, n))
		case 0x4487:
			value = string(p.at(body, n))
		case 0x67c8:
			if depth < 12 {
				p.simpleTag(body, body+n, depth+1)
			}
		}
		off = body + n
	}
	if name != "" && value != "" {
		p.add(name, value)
	}
}
func (p *reader) mkaAttachment(start, end int64) {
	mime := ""
	var data []byte
	for off := start; off < end && p.nodes < 10000; {
		p.nodes++
		id, ni, ok := p.vint(off, true)
		if !ok {
			return
		}
		n, ns, ok := p.vint(off+int64(ni), false)
		body := off + int64(ni+ns)
		if !ok || n < 0 || n > end-body {
			return
		}
		switch id {
		case 0x4660:
			mime = string(p.at(body, n))
		case 0x465c:
			if n <= 4<<20 {
				data = p.at(body, n)
			}
		}
		off = body + n
	}
	if mime == "image/jpeg" || mime == "image/png" {
		p.picture(3, mime, data)
	}
}
func (p *reader) flacCue(b []byte, rate int) {
	if len(b) < 396 || rate <= 0 {
		return
	}
	count := int(b[395])
	pos := 396
	for n := 0; n < count; n++ {
		if pos+36 > len(b) {
			p.warn()
			return
		}
		offset := binary.BigEndian.Uint64(b[pos:])
		number := b[pos+8]
		indexes := int(b[pos+35])
		pos += 36
		if indexes > (len(b)-pos)/12 {
			p.warn()
			return
		}
		start := uint64(0)
		found := false
		for i := 0; i < indexes; i++ {
			v := b[pos : pos+12]
			if v[8] == 1 {
				start = binary.BigEndian.Uint64(v)
				found = true
			}
			pos += 12
		}
		if number == 170 || number == 255 {
			continue
		}
		if number == 0 || !found || offset > 1<<53 || start > 1<<53-offset {
			p.warn()
			continue
		}
		p.out.CueTracks = append(p.out.CueTracks, Chapter{Title: "Track " + strconv.Itoa(int(number)), Start: float64(offset+start) / float64(rate)})
	}
}

// WM/Picture is the packed binary attribute: type and byte length, two UTF-16
// strings, then the unchanged image bytes. No native pointer layout is used.
// https://learn.microsoft.com/windows/win32/api/mfidl/ns-mfidl-asf_flat_picture
func (p *reader) asfPicture(b []byte) {
	if len(b) < 5 {
		p.warn()
		return
	}
	kind, n := int(b[0]), uint64(binary.LittleEndian.Uint32(b[1:5]))
	b = b[5:]
	takeText := func() (string, bool) {
		for i := 0; i+2 <= len(b); i += 2 {
			if b[i] == 0 && b[i+1] == 0 {
				s := asfText(b[:i])
				b = b[i+2:]
				return s, true
			}
		}
		return "", false
	}
	mime, ok := takeText()
	if !ok {
		p.warn()
		return
	}
	if _, ok = takeText(); !ok {
		p.warn()
		return
	}
	if n > uint64(len(b)) || n > 4<<20 {
		p.warn()
		return
	}
	p.picture(kind, mime, b[:int(n)])
}
func (p *reader) asfMetadata(b []byte) {
	if len(b) < 2 {
		return
	}
	count := int(binary.LittleEndian.Uint16(b))
	b = b[2:]
	for i := 0; i < count && i < maxValues; i++ {
		if len(b) < 12 {
			p.warn()
			return
		}
		nameLen := int(binary.LittleEndian.Uint16(b[4:6]))
		typ := binary.LittleEndian.Uint16(b[6:8])
		n := uint64(binary.LittleEndian.Uint32(b[8:12]))
		b = b[12:]
		if nameLen > len(b) || n > uint64(len(b)-nameLen) {
			p.warn()
			return
		}
		name := strings.TrimPrefix(asfText(b[:nameLen]), "WM/")
		switch name {
		case "AlbumTitle":
			name = "album"
		case "AlbumArtist":
			name = "album_artist"
		case "TrackNumber":
			name = "track"
		case "Year":
			name = "date"
		case "Genre":
			name = "genre"
		case "Composer":
			name = "composer"
		}
		v := b[nameLen : nameLen+int(n)]
		b = b[nameLen+int(n):]
		if typ == 1 && name == "Picture" {
			p.asfPicture(v)
		} else if typ == 0 {
			p.add(name, asfText(v))
		}
	}
}
