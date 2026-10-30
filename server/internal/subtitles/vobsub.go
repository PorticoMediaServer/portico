package subtitles

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// FFmpeg extracts embedded DVD packets into a single-track Matroska capsule.
// Rewrap those unchanged SPU packets in DVD private-stream packs and build the
// corresponding index. No OCR or pixel conversion is involved.
func VobSubOriginal(capsule []byte) ([]byte, []byte, error) {
	if len(capsule) == 0 || len(capsule) > BinaryLimitBytes() {
		return nil, nil, ErrCapacity
	}
	type packet struct {
		ms   int64
		data []byte
	}
	var packets []packet
	var private []byte
	scale := int64(1000000)
	var walk func([]byte, int, uint64, int64) error
	walk = func(data []byte, depth int, parent uint64, cluster int64) error {
		if depth > 8 {
			return ErrCapacity
		}
		for len(data) > 0 {
			id, n, ok := ebmlNumber(data, true)
			if !ok {
				return ErrText
			}
			data = data[n:]
			size, n, ok := ebmlNumber(data, false)
			if !ok {
				return ErrText
			}
			data = data[n:]
			if size > uint64(len(data)) {
				if size != uint64(1)<<(7*n)-1 {
					return ErrText
				}
				size = uint64(len(data))
			}
			body := data[:int(size)]
			data = data[int(size):]
			switch id {
			case 0x18538067, 0x1549a966, 0x1654ae6b, 0xae, 0x1f43b675, 0xa0:
				next := cluster
				if id == 0x1f43b675 {
					next = 0
				}
				if e := walk(body, depth+1, id, next); e != nil {
					return e
				}
			case 0x2ad7b1:
				if len(body) > 8 {
					return ErrText
				}
				scale = int64(ebmlUnsigned(body))
				if scale <= 0 || scale > 1000000000 {
					return ErrTiming
				}
			case 0x63a2:
				if parent == 0xae {
					private = append([]byte{}, body...)
				}
			case 0xe7:
				if parent == 0x1f43b675 {
					if len(body) > 8 {
						return ErrText
					}
					cluster = int64(ebmlUnsigned(body))
				}
			case 0xa1, 0xa3:
				_, n, ok := ebmlNumber(body, false)
				if !ok || len(body) < n+3 {
					return ErrText
				}
				if body[n+2]&6 != 0 {
					continue
				} // No lacing is written for DVD subtitle packets.
				ticks := cluster + int64(int16(binary.BigEndian.Uint16(body[n:n+2])))
				if ticks < 0 || ticks > MaxDurationMS*1000000/scale {
					continue
				}
				payload := body[n+3:]
				if len(payload) < 4 || len(payload) > 65535 {
					continue
				}
				if int(binary.BigEndian.Uint16(payload[:2])) != len(payload) {
					continue
				}
				packets = append(packets, packet{ticks * scale / 1000000, payload})
				if len(packets) > MaxCues {
					return ErrCapacity
				}
			}
		}
		return nil
	}
	if e := walk(capsule, 0, 0, 0); e != nil {
		return nil, nil, e
	}
	if len(packets) == 0 {
		return nil, nil, ErrText
	}
	var idx strings.Builder
	idx.WriteString("# VobSub index file, v7 (do not modify this line!)\n")
	// CodecPrivate carries the authored palette and canvas, without include/file
	// references. Only known index metadata is copied into the generated index.
	for _, line := range strings.Split(string(private), "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		for _, prefix := range []string{"size:", "palette:", "custom colors:", "scale:", "alpha:", "origin:", "align:", "fadein/out:"} {
			if strings.HasPrefix(lower, prefix) {
				if len(line) > 1000 || strings.ContainsRune(line, 0) {
					return nil, nil, ErrText
				}
				idx.WriteString(line + "\n")
				break
			}
		}
	}
	idx.WriteString("id: und, index: 0\n")
	var sub bytes.Buffer
	for _, p := range packets {
		fmt.Fprintf(&idx, "timestamp: %02d:%02d:%02d:%03d, filepos: %09x\n", p.ms/3600000, p.ms/60000%60, p.ms/1000%60, p.ms%1000, sub.Len())
		payload := p.data
		for len(payload) > 0 {
			n := min(len(payload), 2000)
			pack := make([]byte, 2048)
			copy(pack, []byte{0, 0, 1, 0xba, 0x44, 0, 4, 0, 4, 1, 1, 0x89, 0xc3, 0xf8})
			copy(pack[14:], []byte{0, 0, 1, 0xbd})
			binary.BigEndian.PutUint16(pack[18:20], uint16(n+9))
			copy(pack[20:], []byte{0x80, 0x80, 5})
			pts := uint64(p.ms) * 90
			copy(pack[23:], []byte{0x21 | byte(pts>>29)&0x0e, byte(pts >> 22), byte(pts>>14)&0xfe | 1, byte(pts >> 7), byte(pts<<1)&0xfe | 1})
			pack[28] = 0x20
			copy(pack[29:], payload[:n])
			payload = payload[n:]
			at := 29 + n
			copy(pack[at:], []byte{0, 0, 1, 0xbe})
			binary.BigEndian.PutUint16(pack[at+4:at+6], uint16(len(pack)-at-6))
			for i := at + 6; i < len(pack); i++ {
				pack[i] = 0xff
			}
			sub.Write(pack)
			if sub.Len() > BinaryLimitBytes() {
				return nil, nil, ErrCapacity
			}
		}
	}
	return []byte(idx.String()), sub.Bytes(), nil
}
func ebmlUnsigned(b []byte) uint64 {
	var n uint64
	for _, v := range b {
		n = n<<8 | uint64(v)
	}
	return n
}
func ebmlNumber(b []byte, id bool) (uint64, int, bool) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0, false
	}
	n := 1
	mask := byte(0x80)
	for b[0]&mask == 0 {
		mask >>= 1
		n++
	}
	if n > 8 || id && n > 4 || len(b) < n {
		return 0, 0, false
	}
	v := uint64(b[0])
	if !id {
		v &= uint64(mask - 1)
	}
	for i := 1; i < n; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v, n, true
}
