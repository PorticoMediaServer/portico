package subtitles

import "encoding/binary"

// Lines counts an image subtitle's lines (its display sets or subpictures)
// and how many of them are marked forced. A device that draws the track
// itself offers "Forced only" when some lines are forced and others are not
// (foreign dialogue and signs inside a full track); the web reads the same
// marks from the file it decodes.
type Lines struct {
	Forced int `json:"forced"`
	Total  int `json:"total"`
}

// Mixed reports whether "Forced only" changes what is shown.
func (l Lines) Mixed() bool { return l.Forced > 0 && l.Forced < l.Total }

// imageLines reads only the headers of a PGS stream (data) or a VobSub
// program stream (companion); bitmaps are skipped, nothing is decoded. Other
// formats have no forced marks.
func imageLines(format string, data, companion []byte) Lines {
	switch format {
	case "pgs", "sup":
		return pgsLines(data)
	case "vobsub", "idx":
		return vobsubLines(companion)
	}
	return Lines{}
}

// pgsLines counts presentation compositions that show at least one object; a
// composition is forced when every object it places carries the forced flag
// (0x40), as the decoders read it.
func pgsLines(b []byte) Lines {
	var out Lines
	for p := 0; p+13 <= len(b); {
		if b[p] != 'P' || b[p+1] != 'G' {
			return out
		}
		kind := b[p+10]
		size := int(binary.BigEndian.Uint16(b[p+11:]))
		start, end := p+13, p+13+size
		if end > len(b) {
			return out
		}
		if kind == 0x16 && size >= 11 {
			seg := b[start:end]
			objects := int(seg[10])
			if objects > 0 {
				forced := true
				q := 11
				for i := 0; i < objects; i++ {
					if q+8 > len(seg) {
						forced = false
						break
					}
					flags := seg[q+3]
					if flags&0x40 == 0 {
						forced = false
					}
					q += 8
					if flags&0x80 != 0 {
						q += 8
					}
				}
				out.Total++
				if forced {
					out.Forced++
				}
			}
		}
		p = end
	}
	return out
}

// vobsubLines walks the program stream's packs, gathers each subpicture
// (private stream 1, substreams 0x20–0x3f) and reads its control sequences:
// command 0x00 marks a forced subpicture.
func vobsubLines(b []byte) Lines {
	var out Lines
	pending := map[byte][]byte{}
	finish := func(stream byte) {
		spu := pending[stream]
		if len(spu) < 4 {
			return
		}
		size := int(binary.BigEndian.Uint16(spu))
		if size < 4 || len(spu) < size {
			return
		}
		delete(pending, stream)
		out.Total++
		if spuForced(spu[:size]) {
			out.Forced++
		}
	}
	for p := 0; p+4 <= len(b); {
		if b[p] != 0 || b[p+1] != 0 || b[p+2] != 1 {
			p++
			continue
		}
		code := b[p+3]
		switch {
		case code == 0xba: // pack header
			if p+5 > len(b) {
				return out
			}
			if b[p+4]&0xc0 == 0x40 { // MPEG-2
				if p+14 > len(b) {
					return out
				}
				p += 14 + int(b[p+13]&7)
			} else {
				p += 12
			}
		case code == 0xb9: // end of stream
			return out
		case code >= 0xbb:
			if p+6 > len(b) {
				return out
			}
			length := int(binary.BigEndian.Uint16(b[p+4:]))
			body := p + 6
			end := body + length
			if end > len(b) {
				return out
			}
			if code == 0xbd && length > 3 && b[body]&0xc0 == 0x80 {
				payload := body + 3 + int(b[body+2])
				if payload < end {
					stream := b[payload]
					if stream >= 0x20 && stream <= 0x3f {
						spu := append(pending[stream], b[payload+1:end]...)
						// A subpicture is at most 64 KiB; a stream that grows past it is broken.
						if len(spu) > 1<<16 {
							delete(pending, stream)
						} else {
							pending[stream] = spu
							finish(stream)
						}
					}
				}
			}
			p = end
		default:
			p += 4
		}
	}
	return out
}

// spuForced reads a subpicture's control sequences for the forced command.
func spuForced(spu []byte) bool {
	at := int(binary.BigEndian.Uint16(spu[2:]))
	for steps := 0; steps < 64 && at+4 <= len(spu); steps++ {
		next := int(binary.BigEndian.Uint16(spu[at+2:]))
		q := at + 4
		for q < len(spu) {
			cmd := spu[q]
			q++
			switch cmd {
			case 0x00:
				return true
			case 0x01, 0x02:
			case 0x03, 0x04:
				q += 2
			case 0x05:
				q += 6
			case 0x06:
				q += 4
			default: // 0xff ends the sequence; anything else is unknown
				q = len(spu)
			}
		}
		if next == at || next <= 0 {
			break
		}
		at = next
	}
	return false
}
