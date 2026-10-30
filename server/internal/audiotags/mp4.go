package audiotags

import (
	"encoding/binary"
	"strconv"
	"strings"
)

var atomNames = map[string]string{"\xa9nam": "title", "\xa9ART": "artist", "aART": "album_artist", "\xa9alb": "album", "\xa9day": "date", "\xa9gen": "genre", "\xa9wrt": "composer", "\xa9cmt": "description", "desc": "description", "ldes": "description", "\xa9grp": "series", "tvsh": "series", "soar": "artist_sort", "soaa": "album_artist_sort", "soal": "album_sort", "sonm": "title_sort", "\xa9lyr": "lyrics"}

func (p *reader) atoms(start, end int64, depth int, parent string) {
	if depth > 12 {
		return
	}
	for off := start; off+8 <= end; {
		p.nodes++
		if p.nodes > 16384 {
			p.warn()
			return
		}
		h := p.at(off, 8)
		if len(h) != 8 {
			return
		}
		n := int64(binary.BigEndian.Uint32(h))
		header := int64(8)
		kind := string(h[4:])
		if n == 1 {
			v := p.at(off+8, 8)
			if len(v) != 8 {
				return
			}
			u := binary.BigEndian.Uint64(v)
			if u > uint64(end-off) {
				return
			}
			n = int64(u)
			header = 16
		}
		if n == 0 {
			n = end - off
		}
		if n < header || n > end-off {
			p.warn()
			return
		}
		begin := off + header
		switch {
		case kind == "moov" || kind == "udta" || kind == "ilst":
			p.atoms(begin, off+n, depth+1, kind)
			if kind == "moov" {
				p.quickTimeChapters(begin, off+n)
			}
		case kind == "meta":
			p.atoms(begin+4, off+n, depth+1, kind)
		case parent == "ilst":
			p.item(kind, p.at(begin, n-header))
		case kind == "chpl":
			p.chpl(p.at(begin, n-header))
		}
		off += n
	}
}
func (p *reader) item(kind string, b []byte) {
	key := atomNames[kind]
	var payloads [][]byte
	var types []uint32
	for len(b) >= 8 {
		n := int(binary.BigEndian.Uint32(b))
		if n < 8 || n > len(b) {
			p.warn()
			break
		}
		id := string(b[4:8])
		v := b[8:n]
		switch id {
		case "name":
			if len(v) >= 4 {
				key = string(v[4:])
			}
		case "data":
			if len(v) >= 8 {
				types = append(types, binary.BigEndian.Uint32(v)&0xffffff)
				payloads = append(payloads, v[8:])
			}
		}
		b = b[n:]
	}
	for i, v := range payloads {
		switch kind {
		case "trkn", "disk":
			if len(v) >= 6 {
				k := "track"
				total := "total_tracks"
				if kind == "disk" {
					k = "disc"
					total = "total_discs"
				}
				p.add(k, strconv.Itoa(int(binary.BigEndian.Uint16(v[2:]))))
				p.add(total, strconv.Itoa(int(binary.BigEndian.Uint16(v[4:]))))
			}
		case "cpil":
			if len(v) > 0 {
				p.add("compilation", strconv.Itoa(int(v[len(v)-1])))
			}
		case "gnre":
			if len(v) >= 2 {
				p.add("genre", strconv.Itoa(int(binary.BigEndian.Uint16(v))-1))
			}
		case "covr":
			mime := "image/jpeg"
			if types[i] == 14 {
				mime = "image/png"
			}
			p.picture(3, mime, v)
		default:
			if key != "" {
				if types[i] == 2 {
					p.add(key, text(v, 2))
				} else if types[i] == 21 && len(v) > 0 && len(v) <= 8 {
					var num uint64
					for _, b := range v {
						num = num<<8 | uint64(b)
					}
					p.add(key, strconv.FormatUint(num, 10))
				} else {
					for _, s := range strings.Split(string(v), "\x00") {
						p.add(key, s)
					}
				}
			}
		}
	}
}
func (p *reader) chpl(b []byte) {
	if len(b) < 9 {
		return
	}
	count := int(b[8])
	b = b[9:]
	for i := 0; i < count; i++ {
		if len(b) < 9 {
			p.warn()
			return
		}
		start := float64(binary.BigEndian.Uint64(b)) / 1e7
		n := int(b[8])
		b = b[9:]
		if n > len(b) {
			p.warn()
			return
		}
		title := string(b[:n])
		b = b[n:]
		if len(p.out.Chapters) > 0 {
			p.out.Chapters[len(p.out.Chapters)-1].End = start
		}
		p.out.Chapters = append(p.out.Chapters, Chapter{title, start, 0})
	}
}
