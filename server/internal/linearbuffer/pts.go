package linearbuffer

import "io"

const ptsWrap int64 = 1 << 33

type Clock struct{ Min, Max int64 }

func unwrap(value, near int64) int64 {
	for value-near > ptsWrap/2 {
		value -= ptsWrap
	}
	for near-value > ptsWrap/2 {
		value += ptsWrap
	}
	return value
}

// ReadPTS extracts presentation time, not PCR/packet arrival time. FFmpeg emits
// complete 188-byte MPEG-TS packets; video is the primary clock, audio is the
// fallback for audio-only channels. Reordered B-frame PTS are accounted for.
func ReadPTS(r io.Reader) (Clock, error) {
	var packet [188]byte
	type stream struct {
		clock Clock
		seen  bool
		video bool
	}
	streams := map[int]*stream{}
	for {
		n, e := io.ReadFull(r, packet[:])
		if e == io.EOF && n == 0 {
			break
		}
		if e != nil || packet[0] != 0x47 || packet[1]&0x80 != 0 {
			return Clock{}, ErrMedia
		}
		if packet[1]&0x40 == 0 || packet[3]&0x10 == 0 {
			continue
		}
		offset := 4
		if packet[3]&0x20 != 0 {
			offset += 1 + int(packet[4])
		}
		if offset+14 > 188 {
			continue
		}
		payload := packet[offset:]
		if payload[0] != 0 || payload[1] != 0 || payload[2] != 1 || payload[7]&0x80 == 0 || payload[8] < 5 {
			continue
		}
		sid := payload[3]
		video := sid >= 0xe0 && sid <= 0xef
		audio := sid >= 0xc0 && sid <= 0xdf || sid == 0xbd
		if !video && !audio {
			continue
		}
		v := payload[9:14]
		if v[0]&1 != 1 || v[2]&1 != 1 || v[4]&1 != 1 {
			return Clock{}, ErrMedia
		}
		pts := int64(v[0]>>1&7)<<30 | int64(v[1])<<22 | int64(v[2]>>1)<<15 | int64(v[3])<<7 | int64(v[4]>>1)
		pid := int(packet[1]&0x1f)<<8 | int(packet[2])
		s := streams[pid]
		if s == nil {
			if len(streams) >= 32 {
				return Clock{}, ErrMedia
			}
			s = &stream{video: video}
			streams[pid] = s
		}
		if !s.seen {
			s.clock = Clock{pts, pts}
			s.seen = true
		} else {
			pts = unwrap(pts, s.clock.Max)
			s.clock.Min = min(s.clock.Min, pts)
			s.clock.Max = max(s.clock.Max, pts)
		}
	}
	var chosen *stream
	chosenPID := 8192
	for pid, s := range streams {
		if !s.seen {
			continue
		}
		if chosen == nil || s.video && !chosen.video || s.video == chosen.video && pid < chosenPID {
			chosen = s
			chosenPID = pid
		}
	}
	if chosen == nil {
		return Clock{}, ErrMedia
	}
	return chosen.clock, nil
}
