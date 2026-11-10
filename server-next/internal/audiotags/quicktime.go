package audiotags

import (
	"encoding/binary"
	"strings"
)

type qtTrack struct {
	id, rate         uint32
	refs             []uint32
	sizes, durations []uint32
	offsets          []uint64
	chunks           []qtChunk
	text             bool
}
type qtChunk struct{ first, samples uint32 }

func qtWords(b []byte, width int) []uint64 {
	if len(b) < 8 {
		return nil
	}
	count := int(binary.BigEndian.Uint32(b[4:]))
	if count > 4096 || count > (len(b)-8)/width {
		return nil
	}
	out := make([]uint64, count)
	for i := range out {
		if width == 8 {
			out[i] = binary.BigEndian.Uint64(b[8+i*width:])
		} else {
			out[i] = uint64(binary.BigEndian.Uint32(b[8+i*width:]))
		}
	}
	return out
}
func (t *qtTrack) read(b []byte, depth int) {
	if depth > 8 {
		return
	}
	for nodes := 0; len(b) >= 8 && nodes < 4096; nodes++ {
		n := int(binary.BigEndian.Uint32(b))
		if n < 8 || n > len(b) {
			return
		}
		id := string(b[4:8])
		v := b[8:n]
		b = b[n:]
		switch id {
		case "mdia", "minf", "stbl", "tref":
			t.read(v, depth+1)
		case "tkhd":
			off := 12
			if len(v) > 0 && v[0] == 1 {
				off = 20
			}
			if len(v) >= off+4 {
				t.id = binary.BigEndian.Uint32(v[off:])
			}
		case "mdhd":
			off := 12
			if len(v) > 0 && v[0] == 1 {
				off = 20
			}
			if len(v) >= off+4 {
				t.rate = binary.BigEndian.Uint32(v[off:])
			}
		case "hdlr":
			if len(v) >= 12 {
				h := string(v[8:12])
				t.text = h == "text" || h == "sbtl" || h == "subt"
			}
		case "chap":
			if len(v)/4 <= 128 {
				for len(v) >= 4 {
					t.refs = append(t.refs, binary.BigEndian.Uint32(v))
					v = v[4:]
				}
			}
		case "stco":
			t.offsets = qtWords(v, 4)
		case "co64":
			t.offsets = qtWords(v, 8)
		case "stsz":
			if len(v) < 12 {
				continue
			}
			fixed := binary.BigEndian.Uint32(v[4:])
			count := int(binary.BigEndian.Uint32(v[8:]))
			if count > 4096 || fixed == 0 && count > (len(v)-12)/4 {
				continue
			}
			t.sizes = make([]uint32, count)
			for i := range t.sizes {
				t.sizes[i] = fixed
				if fixed == 0 {
					t.sizes[i] = binary.BigEndian.Uint32(v[12+i*4:])
				}
			}
		case "stts":
			if len(v) < 8 {
				continue
			}
			count := int(binary.BigEndian.Uint32(v[4:]))
			if count > 4096 || count > (len(v)-8)/8 {
				continue
			}
			for i := 0; i < count; i++ {
				repeats := int(binary.BigEndian.Uint32(v[8+i*8:]))
				duration := binary.BigEndian.Uint32(v[12+i*8:])
				if repeats > 4096-len(t.durations) {
					break
				}
				for j := 0; j < repeats; j++ {
					t.durations = append(t.durations, duration)
				}
			}
		case "stsc":
			if len(v) < 8 {
				continue
			}
			count := int(binary.BigEndian.Uint32(v[4:]))
			if count > 4096 || count > (len(v)-8)/12 {
				continue
			}
			for i := 0; i < count; i++ {
				t.chunks = append(t.chunks, qtChunk{binary.BigEndian.Uint32(v[8+i*12:]), binary.BigEndian.Uint32(v[12+i*12:])})
			}
		}
	}
}
func (p *reader) quickTimeChapters(start, end int64) {
	if len(p.out.Chapters) > 0 {
		return
	}
	tracks := []qtTrack{}
	wanted := map[uint32]bool{}
	for off := start; off+8 <= end && len(tracks) < 128; {
		h := p.at(off, 8)
		if len(h) != 8 {
			return
		}
		n := int64(binary.BigEndian.Uint32(h))
		if n < 8 || n > end-off {
			return
		}
		if string(h[4:]) == "trak" {
			var track qtTrack
			track.read(p.at(off+8, n-8), 0)
			tracks = append(tracks, track)
			for _, id := range track.refs {
				wanted[id] = true
			}
		}
		off += n
	}
	for _, t := range tracks {
		if !wanted[t.id] || !t.text || t.rate == 0 || len(t.sizes) == 0 || len(t.durations) != len(t.sizes) || len(t.chunks) == 0 {
			continue
		}
		index := 0
		chunkIndex := 0
		var elapsed uint64
		for i, offset := range t.offsets {
			for chunkIndex+1 < len(t.chunks) && t.chunks[chunkIndex+1].first <= uint32(i+1) {
				chunkIndex++
			}
			chunk := t.chunks[chunkIndex]
			if chunk.first > uint32(i+1) || chunk.samples > 4096 {
				break
			}
			for j := uint32(0); j < chunk.samples && index < len(t.sizes); j++ {
				n := t.sizes[index]
				if n > 65536 || offset > uint64(p.size) || uint64(n) > uint64(p.size)-offset {
					p.warn()
					return
				}
				raw := p.at(int64(offset), int64(n))
				title := ""
				if len(raw) >= 2 {
					length := int(binary.BigEndian.Uint16(raw))
					if length <= len(raw)-2 {
						title = string(raw[2 : 2+length])
						if strings.HasPrefix(title, "\xfe\xff") || strings.HasPrefix(title, "\xff\xfe") {
							title = text(raw[2:2+length], 1)
						}
					}
				}
				start := float64(elapsed) / float64(t.rate)
				elapsed += uint64(t.durations[index])
				p.chapter(title, start, float64(elapsed)/float64(t.rate))
				offset += uint64(n)
				index++
			}
		}
		return
	}
}
