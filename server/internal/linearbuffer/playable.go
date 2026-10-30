package linearbuffer

import "io"

// hasPlayablePayload excludes transport tables, adaptation/PES headers and
// initialization-only output. The publisher independently validates the media
// clock and duration before accepting this muxer's closed segment.
func hasPlayablePayload(r io.Reader, fragmented bool) bool {
	if fragmented {
		data, err := io.ReadAll(io.LimitReader(r, maxSegmentBytes+1))
		if err != nil || int64(len(data)) > maxSegmentBytes {
			return false
		}
		list, ok := boxes(data, 0, len(data))
		if !ok {
			return false
		}
		for _, box := range list {
			if box.kind == "mdat" && box.end > box.start {
				return true
			}
		}
		return false
	}
	type elementary struct {
		header, remaining int
		unbounded         bool
	}
	streams := map[int]elementary{}
	var packet [188]byte
	for {
		n, err := io.ReadFull(r, packet[:])
		if err == io.EOF && n == 0 {
			return false
		}
		if err != nil || packet[0] != 0x47 || packet[1]&0x80 != 0 {
			return false
		}
		if packet[3]&0x10 == 0 {
			continue
		}
		offset := 4
		if packet[3]&0x20 != 0 {
			offset += 1 + int(packet[4])
		}
		if offset >= len(packet) {
			continue
		}
		payload := packet[offset:]
		pid := int(packet[1]&0x1f)<<8 | int(packet[2])
		stream, known := streams[pid]
		if packet[1]&0x40 != 0 {
			delete(streams, pid)
			if len(payload) < 9 || payload[0] != 0 || payload[1] != 0 || payload[2] != 1 {
				continue
			}
			sid := payload[3]
			if !(sid >= 0xe0 && sid <= 0xef || sid >= 0xc0 && sid <= 0xdf || sid == 0xbd) {
				continue
			}
			if len(streams) >= 32 {
				return false
			}
			size := int(payload[4])<<8 | int(payload[5])
			stream = elementary{header: 9 + int(payload[8]), remaining: 6 + size, unbounded: size == 0}
			known = true
		}
		if !known {
			continue
		}
		available := len(payload)
		if !stream.unbounded && available > stream.remaining {
			available = stream.remaining
		}
		if available < 0 {
			return false
		}
		skip := min(stream.header, available)
		stream.header -= skip
		body := payload[skip:available]
		if len(body) > 0 {
			// MPEG padding bytes alone are not an elementary media sample.
			for _, value := range body {
				if value != 0xff {
					return true
				}
			}
		}
		if !stream.unbounded {
			stream.remaining -= available
		}
		streams[pid] = stream
	}
}
