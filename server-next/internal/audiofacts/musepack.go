package audiofacts

import (
	"encoding/binary"
	"hash/crc32"
)

// MusepackHeader contains only facts in this exact source header. DurationFrames
// excludes beginning silence. DecoderDelayFrames is not part of that duration;
// it is emitted by FFmpeg before audible samples and is added to raw-frame trim.
// SV7 has no header CRC. SV8 SH CRC32 is verified before any count is accepted.
type MusepackHeader struct {
	Codec                                                             string
	SampleRate, Channels                                              int
	TotalFrames, BeginningSilence, DurationFrames, DecoderDelayFrames int64
}

// ParseMusepackHeader reads at most one MiB, optionally past a bounded ID3v2
// prefix. It performs no I/O and never guesses from a filename or extension.
// A scanner may use DurationFrames/SampleRate as the header's timeline; exact
// render trim additionally requires the full raw-frame measurement below.
func ParseMusepackHeader(codec string, prefix []byte) (MusepackHeader, error) {
	var h MusepackHeader
	if len(prefix) > 1<<20 || codec != "mpc7" && codec != "mpc8" {
		return h, ErrInconsistent
	}
	if n := id3End(prefix); n > 0 {
		if n >= int64(len(prefix)) {
			return h, ErrInconsistent
		}
		prefix = prefix[n:]
	}
	var total, silence int64
	var rate, channels int
	if codec == "mpc7" {
		if len(prefix) < 28 || string(prefix[:3]) != "MP+" || prefix[3] != 7 && prefix[3] != 0x17 {
			return h, ErrInconsistent
		}
		frames := int64(binary.LittleEndian.Uint32(prefix[4:8]))
		flags := binary.LittleEndian.Uint32(prefix[20:24])
		last := int64((flags >> 20) & 0x7ff)
		if last == 0 {
			last = 1152
		}
		if frames <= 0 || last > 1152 {
			return h, ErrInconsistent
		}
		total = frames * 1152
		if flags>>31 != 0 {
			total -= 1152 - last
		} else {
			total -= 481
		}
		rate = []int{44100, 48000, 37800, 32000}[(binary.LittleEndian.Uint32(prefix[8:12])>>16)&3]
		channels = 2
	} else {
		if len(prefix) < 8 || string(prefix[:4]) != "MPCK" {
			return h, ErrInconsistent
		}
		p := 4
		found := false
		for packets := 0; packets < 64 && p+3 <= len(prefix); packets++ {
			start := p
			key := string(prefix[p : p+2])
			p += 2
			size, n, ok := mpcVar(prefix[p:])
			if !ok || size < 3 || size > int64(len(prefix)-start) {
				return h, ErrInconsistent
			}
			p += n
			end := start + int(size)
			if end < p {
				return h, ErrInconsistent
			}
			if key == "SH" {
				b := prefix[p:end]
				if len(b) < 8 || b[4] != 8 || binary.BigEndian.Uint32(b[:4]) != crc32.ChecksumIEEE(b[4:]) {
					return h, ErrInconsistent
				}
				total, n, ok = mpcVar(b[5:])
				if !ok {
					return h, ErrInconsistent
				}
				b = b[5+n:]
				silence, n, ok = mpcVar(b)
				if !ok {
					return h, ErrInconsistent
				}
				b = b[n:]
				if len(b) < 2 || int(b[0]>>5) > 3 {
					return h, ErrInconsistent
				}
				rate = []int{44100, 48000, 37800, 32000}[b[0]>>5]
				channels = int(b[1]>>4) + 1
				found = true
				break
			}
			p = end
		}
		if !found {
			return h, ErrInconsistent
		}
	}
	if total <= silence || silence < 0 || total > 1<<53-1 || rate <= 0 || channels < 1 || channels > 16 {
		return h, ErrInconsistent
	}
	return MusepackHeader{Codec: codec, SampleRate: rate, Channels: channels, TotalFrames: total, BeginningSilence: silence, DurationFrames: total - silence, DecoderDelayFrames: 481}, nil
}

// CompleteCodecHeaders supplies the Musepack sample counts FFmpeg's demuxers
// omit. Source counts must fit the fully decoded raw-frame measurement before
// render facts are published. Other codecs are unchanged.
func CompleteCodecHeaders(f Facts, prefix []byte) (Facts, error) {
	if f.Codec != "mpc7" && f.Codec != "mpc8" {
		return f, nil
	}
	h, err := ParseMusepackHeader(f.Codec, prefix)
	if err != nil {
		return f, err
	}
	start, audible := h.DecoderDelayFrames+h.BeginningSilence, h.DurationFrames
	if h.SampleRate != f.SampleRate || h.Channels != f.Channels || start > f.RawFrames || audible > f.RawFrames-start {
		return f, ErrInconsistent
	}
	if f.DurationFrames > 0 {
		f.BitRate = int64(float64(f.BitRate) * float64(f.DurationFrames) / float64(audible))
	}
	f.StartFrames, f.DurationFrames, f.EndFrames, f.TrimSource = start, audible, f.RawFrames-start-audible, "musepack-header"
	return f, nil
}

func mpcVar(b []byte) (int64, int, bool) {
	var value int64
	for i, c := range b {
		if i >= 8 || value > ((1<<53)-1-int64(c&127))>>7 {
			return 0, 0, false
		}
		value = value<<7 | int64(c&127)
		if c&128 == 0 {
			return value, i + 1, true
		}
	}
	return 0, 0, false
}
