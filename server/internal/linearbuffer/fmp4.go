package linearbuffer

import (
	"encoding/binary"
	"io"
)

// Fragmented MP4 output carries its clock in each fragment's tfdt (decode time
// of the first sample) and trun/tfhd/trex durations, in the timescale its init
// segment states per track. These readers take only what that clock needs and
// refuse anything malformed: the bytes came from our own muxer, so anything
// unexpected is a failed producer, not something to be tolerated.

const maxInitBytes = 4 << 20

type fmp4Track struct {
	id              uint32
	timescale       uint32
	video, audio    bool
	defaultDuration uint32
}

// fmp4Init is what an init segment says about its tracks' clocks.
type fmp4Init struct {
	tracks []fmp4Track
}

type box struct {
	kind       string
	start, end int // payload range
}

func boxes(data []byte, from, to int) ([]box, bool) {
	var out []box
	for at := from; at < to; {
		if to-at < 8 || len(out) >= 256 {
			return nil, false
		}
		size := int64(binary.BigEndian.Uint32(data[at:]))
		header := int64(8)
		if size == 1 {
			if to-at < 16 {
				return nil, false
			}
			size = int64(binary.BigEndian.Uint64(data[at+8:]))
			header = 16
		}
		if size == 0 {
			size = int64(to - at)
		}
		if size < header || size > int64(to-at) {
			return nil, false
		}
		out = append(out, box{kind: string(data[at+4 : at+8]), start: at + int(header), end: at + int(size)})
		at += int(size)
	}
	return out, true
}

func child(data []byte, parent box, kind string) (box, bool) {
	list, ok := boxes(data, parent.start, parent.end)
	if !ok {
		return box{}, false
	}
	for _, b := range list {
		if b.kind == kind {
			return b, true
		}
	}
	return box{}, false
}

// parseInit reads track ids, timescales, handler kinds and default sample
// durations from an init segment (ftyp + moov).
func parseInit(data []byte) (fmp4Init, bool) {
	var out fmp4Init
	top, ok := boxes(data, 0, len(data))
	if !ok {
		return out, false
	}
	var moov box
	found := false
	for _, b := range top {
		if b.kind == "moov" {
			moov, found = b, true
		}
		if b.kind == "moof" || b.kind == "mdat" {
			return out, false
		}
	}
	if !found {
		return out, false
	}
	list, ok := boxes(data, moov.start, moov.end)
	if !ok {
		return out, false
	}
	defaults := map[uint32]uint32{}
	for _, b := range list {
		switch b.kind {
		case "trak":
			t, ok := parseTrak(data, b)
			if !ok {
				return out, false
			}
			out.tracks = append(out.tracks, t)
		case "mvex":
			entries, ok := boxes(data, b.start, b.end)
			if !ok {
				return out, false
			}
			for _, e := range entries {
				// trex: version/flags, track_ID, default description index, default duration.
				if e.kind == "trex" && e.end-e.start >= 16 {
					defaults[binary.BigEndian.Uint32(data[e.start+4:])] = binary.BigEndian.Uint32(data[e.start+12:])
				}
			}
		}
	}
	for i := range out.tracks {
		out.tracks[i].defaultDuration = defaults[out.tracks[i].id]
	}
	return out, len(out.tracks) > 0 && len(out.tracks) <= 16
}

func parseTrak(data []byte, trak box) (fmp4Track, bool) {
	var t fmp4Track
	tkhd, ok := child(data, trak, "tkhd")
	if !ok || tkhd.end-tkhd.start < 24 {
		return t, false
	}
	if data[tkhd.start] == 1 {
		if tkhd.end-tkhd.start < 32 {
			return t, false
		}
		t.id = binary.BigEndian.Uint32(data[tkhd.start+20:])
	} else {
		t.id = binary.BigEndian.Uint32(data[tkhd.start+12:])
	}
	mdia, ok := child(data, trak, "mdia")
	if !ok {
		return t, false
	}
	mdhd, ok := child(data, mdia, "mdhd")
	if !ok || mdhd.end-mdhd.start < 24 {
		return t, false
	}
	if data[mdhd.start] == 1 {
		if mdhd.end-mdhd.start < 32 {
			return t, false
		}
		t.timescale = binary.BigEndian.Uint32(data[mdhd.start+20:])
	} else {
		t.timescale = binary.BigEndian.Uint32(data[mdhd.start+12:])
	}
	hdlr, ok := child(data, mdia, "hdlr")
	if !ok || hdlr.end-hdlr.start < 12 {
		return t, false
	}
	switch string(data[hdlr.start+8 : hdlr.start+12]) {
	case "vide":
		t.video = true
	case "soun":
		t.audio = true
	}
	return t, t.id != 0 && t.timescale > 0
}

// to90k converts a decode time in a track's timescale to the 90 kHz clock the
// MPEG-TS reader reports, without overflowing.
func to90k(v uint64, timescale uint32) int64 {
	ts := uint64(timescale)
	return int64(v/ts*90000 + v%ts*90000/ts)
}

// ReadFragmentClock is ReadPTS for a fragmented MP4 media segment: the decode
// span of its fragments on the primary track (video, else audio), in 90 kHz
// units. Min is the first decode time and Max the end of the last sample.
func ReadFragmentClock(r io.Reader, init fmp4Init) (Clock, error) {
	data, e := io.ReadAll(io.LimitReader(r, maxSegmentBytes+1))
	if e != nil || len(data) > int(maxSegmentBytes) {
		return Clock{}, ErrMedia
	}
	var primary *fmp4Track
	for i := range init.tracks {
		t := &init.tracks[i]
		if t.video && (primary == nil || !primary.video) || primary == nil && t.audio {
			primary = t
		}
	}
	if primary == nil {
		return Clock{}, ErrMedia
	}
	top, ok := boxes(data, 0, len(data))
	if !ok {
		return Clock{}, ErrMedia
	}
	var clock Clock
	seen := false
	for _, moof := range top {
		if moof.kind != "moof" {
			continue
		}
		trafs, ok := boxes(data, moof.start, moof.end)
		if !ok {
			return Clock{}, ErrMedia
		}
		for _, traf := range trafs {
			if traf.kind != "traf" {
				continue
			}
			start, end, id, ok := trafSpan(data, traf, primary.defaultDuration)
			if !ok {
				return Clock{}, ErrMedia
			}
			if id != primary.id {
				continue
			}
			a, z := to90k(start, primary.timescale), to90k(end, primary.timescale)
			if !seen {
				clock, seen = Clock{a, z}, true
			} else {
				clock.Min, clock.Max = min(clock.Min, a), max(clock.Max, z)
			}
		}
	}
	if !seen || clock.Max <= clock.Min {
		return Clock{}, ErrMedia
	}
	return clock, nil
}

// trafSpan reads one track fragment: its track id, its first decode time and
// the decode time after its last sample.
func trafSpan(data []byte, traf box, trexDuration uint32) (uint64, uint64, uint32, bool) {
	tfhd, ok := child(data, traf, "tfhd")
	if !ok || tfhd.end-tfhd.start < 8 {
		return 0, 0, 0, false
	}
	flags := binary.BigEndian.Uint32(data[tfhd.start:]) & 0xffffff
	id := binary.BigEndian.Uint32(data[tfhd.start+4:])
	at := tfhd.start + 8
	if flags&0x01 != 0 {
		at += 8
	}
	if flags&0x02 != 0 {
		at += 4
	}
	duration := trexDuration
	if flags&0x08 != 0 {
		if tfhd.end-at < 4 {
			return 0, 0, 0, false
		}
		duration = binary.BigEndian.Uint32(data[at:])
	}
	tfdt, ok := child(data, traf, "tfdt")
	if !ok || tfdt.end-tfdt.start < 8 {
		return 0, 0, 0, false
	}
	var base uint64
	if data[tfdt.start] == 1 {
		if tfdt.end-tfdt.start < 12 {
			return 0, 0, 0, false
		}
		base = binary.BigEndian.Uint64(data[tfdt.start+4:])
	} else {
		base = uint64(binary.BigEndian.Uint32(data[tfdt.start+4:]))
	}
	runs, ok := boxes(data, traf.start, traf.end)
	if !ok {
		return 0, 0, 0, false
	}
	var total uint64
	for _, run := range runs {
		if run.kind != "trun" {
			continue
		}
		if run.end-run.start < 8 {
			return 0, 0, 0, false
		}
		f := binary.BigEndian.Uint32(data[run.start:]) & 0xffffff
		count := binary.BigEndian.Uint32(data[run.start+4:])
		at := run.start + 8
		if f&0x01 != 0 {
			at += 4
		}
		if f&0x04 != 0 {
			at += 4
		}
		per := 0
		for _, bit := range []uint32{0x100, 0x200, 0x400, 0x800} {
			if f&bit != 0 {
				per += 4
			}
		}
		if count > 1<<20 || at+per*int(count) > run.end {
			return 0, 0, 0, false
		}
		for n := uint32(0); n < count; n++ {
			d := uint64(duration)
			if f&0x100 != 0 {
				d = uint64(binary.BigEndian.Uint32(data[at:]))
			}
			total += d
			at += per
		}
	}
	return base, base + total, id, total > 0
}
