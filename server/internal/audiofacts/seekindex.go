package audiofacts

import (
	"context"
	"encoding/binary"
	"errors"
)

// A seek index lets a client's own decoder start an MP3, ADTS or Ogg file
// part-way without reading what comes before and without the server converting
// anything (spec §18.1). These containers carry no exact sample index (an MP3's
// Xing table is approximate), so the index is made here, once per file, beside
// the file's other facts: byte offsets at packet boundaries with the raw frame
// each one starts at. The client reads from the nearest entry at or before its
// target with a Range request, decodes a few packets to settle the decoder, and
// discards samples up to the exact frame.
//
// Frames are counted the way the client's demuxer counts them, from its first
// audio packet: an MP3's Xing/Info/VBRI frame is not audio and is not counted;
// ADTS frames are 1024 per raw data block; Opus packets by their TOC byte (pre-
// skip included, as the facts' raw frames). Vorbis entries carry the page
// granule before them (the PCM position the first packet on that page
// continues from). MP3, ADTS and Opus indexes are kept only when their packet
// count adds up to the measured raw frames exactly.

// SeekStepSeconds is the spacing of entries; MaxSeekEntries bounds an index (a
// longer file gets entries further apart, so its size stays flat).
const (
	SeekStepSeconds = 3
	MaxSeekEntries  = 4096
)

// SeekIndex is one file's table: Offsets[i] is a packet (MP3, ADTS) or page
// (Ogg) boundary, Frames[i] the raw frame decoding from there starts at.
type SeekIndex struct {
	Offsets []int64
	Frames  []int64
}

// ExtentReader reads length bytes at offset (at most 1 MiB, never past the end).
type ExtentReader func(ctx context.Context, offset, length int64) ([]byte, error)

// Indexed reports whether f's container is one a seek index is made for.
func Indexed(f Facts) bool {
	switch f.Container {
	case "mp3", "adts":
		return true
	case "ogg":
		return f.Codec == "opus" || f.Codec == "vorbis"
	}
	return false
}

// BuildSeekIndex reads the file once, in order, parsing only frame and page
// headers (payloads are skipped). It returns nil with no error when the file
// can't be indexed exactly; the track then simply has no index.
func BuildSeekIndex(ctx context.Context, read ExtentReader, size int64, f Facts) (*SeekIndex, error) {
	if !Indexed(f) || size <= 0 || f.SampleRate <= 0 {
		return nil, nil
	}
	c := &byteCursor{ctx: ctx, read: read, size: size}
	b := &indexBuilder{step: int64(SeekStepSeconds) * int64(f.SampleRate)}
	var total int64
	var err error
	switch f.Container {
	case "mp3":
		total, err = indexMP3(c, b)
	case "adts":
		total, err = indexADTS(c, b)
	default:
		total, err = indexOgg(c, b, f.Codec)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, errUnindexable) {
			return nil, nil
		}
		return nil, err
	}
	if f.Codec != "vorbis" && total != f.RawFrames || len(b.index.Offsets) == 0 {
		return nil, nil
	}
	b.thin()
	return &b.index, nil
}

var errUnindexable = errors.New("audiofacts: the stream can't be indexed exactly")

// junkLimit bounds the bytes skipped resynchronising across one file, and
// metadataLimit the ID3 tags skipped at its start (as the client's demuxer).
const junkLimit, metadataLimit = 1 << 20, 24 << 20

type indexBuilder struct {
	index SeekIndex
	step  int64
	next  int64
}

// at records a boundary when the next step is reached.
func (b *indexBuilder) at(offset, frame int64) {
	if frame < b.next {
		return
	}
	b.index.Offsets = append(b.index.Offsets, offset)
	b.index.Frames = append(b.index.Frames, frame)
	b.next = frame + b.step
}

// thin keeps every k-th entry so the index stays within MaxSeekEntries.
func (b *indexBuilder) thin() {
	n := len(b.index.Offsets)
	if n <= MaxSeekEntries {
		return
	}
	k := (n + MaxSeekEntries - 1) / MaxSeekEntries
	out := SeekIndex{Offsets: make([]int64, 0, n/k+1), Frames: make([]int64, 0, n/k+1)}
	for i := 0; i < n; i += k {
		out.Offsets = append(out.Offsets, b.index.Offsets[i])
		out.Frames = append(out.Frames, b.index.Frames[i])
	}
	b.index = out
}

// byteCursor reads a file forward in 1 MiB extents: need(n) makes n bytes
// available at pos (fewer only at the end of the file).
type byteCursor struct {
	ctx   context.Context
	read  ExtentReader
	size  int64
	buf   []byte
	start int64
	pos   int64
}

func (c *byteCursor) view() []byte { return c.buf[c.pos-c.start:] }

func (c *byteCursor) need(n int) (bool, error) {
	for c.start+int64(len(c.buf))-c.pos < int64(n) {
		end := c.start + int64(len(c.buf))
		if end >= c.size {
			return false, nil
		}
		if err := c.ctx.Err(); err != nil {
			return false, err
		}
		chunk, err := c.read(c.ctx, end, min(1<<20, c.size-end))
		if err != nil {
			return false, err
		}
		if len(chunk) == 0 {
			return false, nil
		}
		keep := c.buf[c.pos-c.start:]
		next := make([]byte, 0, len(keep)+len(chunk))
		c.buf = append(append(next, keep...), chunk...)
		c.start = c.pos
	}
	return true, nil
}

func (c *byteCursor) skip(n int64) { c.pos += n }

// seek moves to an absolute offset ahead, dropping what is buffered before it.
func (c *byteCursor) seek(pos int64) {
	if pos > c.start+int64(len(c.buf)) {
		c.buf, c.start = nil, pos
	}
	c.pos = pos
}

// ── MP3 ──────────────────────────────────────────────────────────────

var mp3Bitrates = map[string][15]int{
	"1-3": {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320},
	"1-2": {0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384},
	"2-3": {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
	"2-2": {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
}
var mp3Rates = map[int][3]int{3: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 0: {11025, 12000, 8000}}

type mp3Frame struct {
	version, layer, rate, channels, length int
	frames                                 int64
	crc                                    bool
}

// mp3Header is the client demuxer's frame header rule (Layer II and III).
func mp3Header(b []byte) (mp3Frame, bool) {
	if len(b) < 4 || b[0] != 0xff || b[1]&0xe0 != 0xe0 {
		return mp3Frame{}, false
	}
	versionBits, layerBits := int(b[1]>>3)&3, int(b[1]>>1)&3
	if versionBits == 1 || layerBits == 0 {
		return mp3Frame{}, false
	}
	layer := 4 - layerBits
	if layer == 1 {
		return mp3Frame{}, false
	}
	bitrateIndex, rateIndex, padding := int(b[2]>>4), int(b[2]>>2)&3, int(b[2]>>1)&1
	if bitrateIndex == 0 || bitrateIndex == 15 || rateIndex == 3 {
		return mp3Frame{}, false
	}
	mpeg1 := versionBits == 3
	key := "2-"
	if mpeg1 {
		key = "1-"
	}
	kbps := mp3Bitrates[key+string(rune('0'+layer))][bitrateIndex]
	rate := mp3Rates[versionBits][rateIndex]
	frames := 1152
	if layer == 3 && !mpeg1 {
		frames = 576
	}
	channels := 2
	if b[3]>>6 == 3 {
		channels = 1
	}
	length := frames/8*kbps*1000/rate + padding
	return mp3Frame{version: versionBits, layer: layer, rate: rate, channels: channels, length: length, frames: int64(frames), crc: b[1]&1 == 0}, true
}

// mp3InfoFrame is a Xing/Info or VBRI header frame (no audio).
func mp3InfoFrame(b []byte, h mp3Frame) bool {
	if h.layer != 3 {
		return false
	}
	side := 17
	switch {
	case h.version == 3 && h.channels != 1:
		side = 32
	case h.version != 3 && h.channels == 1:
		side = 9
	}
	at := 4 + side
	if h.crc {
		at += 2
	}
	tag := func(o int) string {
		if o+4 > len(b) {
			return ""
		}
		return string(b[o : o+4])
	}
	return tag(at) == "Xing" || tag(at) == "Info" || tag(36) == "VBRI"
}

// id3End is the offset after an ID3v2 tag at b[0:], or 0 when there is none.
func id3End(b []byte) int64 {
	if len(b) < 10 || string(b[:3]) != "ID3" {
		return 0
	}
	size := int64(b[6]&0x7f)<<21 | int64(b[7]&0x7f)<<14 | int64(b[8]&0x7f)<<7 | int64(b[9]&0x7f)
	end := 10 + size
	if b[5]&0x10 != 0 {
		end += 10
	}
	return end
}

func indexMP3(c *byteCursor, b *indexBuilder) (int64, error) {
	// The first frame: past any ID3 tags, a header the next one confirms.
	var first mp3Frame
	metadata := int64(0)
	found := false
	for searched := int64(0); searched < 1<<20 && !found; {
		ok, err := c.need(10)
		if err != nil || !ok {
			return 0, orUnindexable(err)
		}
		if end := id3End(c.view()); end > 0 {
			if metadata += end; metadata > metadataLimit || c.pos+end > c.size {
				return 0, errUnindexable
			}
			c.seek(c.pos + end)
			continue
		}
		if ok, err = c.need(4); err != nil || !ok {
			return 0, orUnindexable(err)
		}
		if h, ok := mp3Header(c.view()); ok && h.length > 4 {
			have, err := c.need(h.length + 4)
			if err != nil {
				return 0, err
			}
			if have {
				if next, ok := mp3Header(c.view()[h.length:]); ok && next.rate == h.rate && next.layer == h.layer && next.version == h.version {
					first, found = h, true
					break
				}
			}
		}
		c.skip(1)
		searched++
	}
	if !found {
		return 0, errUnindexable
	}
	var total, junk int64
	for index := 0; ; {
		ok, err := c.need(4)
		if err != nil {
			return 0, err
		}
		if !ok {
			break
		}
		h, ok := mp3Header(c.view())
		if !ok || h.rate != first.rate || h.layer != first.layer {
			// Trailing tags (ID3v1, APE) end the audio; anything else is resynchronised.
			if _, err := c.need(8); err != nil {
				return 0, err
			}
			if v := c.view(); string(v[:3]) == "TAG" || len(v) >= 8 && string(v[:8]) == "APETAGEX" {
				break
			}
			if junk++; junk > junkLimit {
				return 0, errUnindexable
			}
			c.skip(1)
			continue
		}
		if ok, err = c.need(h.length); err != nil {
			return 0, err
		} else if !ok {
			break // a truncated last frame is not audio
		}
		info := index == 0 && mp3InfoFrame(c.view()[:h.length], h)
		index++
		if !info {
			b.at(c.pos, total)
			total += h.frames
		}
		c.skip(int64(h.length))
	}
	return total, nil
}

// ── ADTS ─────────────────────────────────────────────────────────────

type adtsFrame struct{ rateIndex, length, blocks int }

func adtsHeader(b []byte) (adtsFrame, bool) {
	if len(b) < 7 || b[0] != 0xff || b[1]&0xf6 != 0xf0 {
		return adtsFrame{}, false
	}
	headerLength := 9
	if b[1]&1 != 0 {
		headerLength = 7
	}
	rateIndex := int(b[2]>>2) & 0xf
	channels := int(b[2]&1)<<2 | int(b[3]>>6)
	length := int(b[3]&3)<<11 | int(b[4])<<3 | int(b[5]>>5)
	if rateIndex >= 13 || length < headerLength || channels == 0 {
		return adtsFrame{}, false
	}
	return adtsFrame{rateIndex: rateIndex, length: length, blocks: int(b[6]&3) + 1}, true
}

func indexADTS(c *byteCursor, b *indexBuilder) (int64, error) {
	// One ID3 tag at the start, then a header the next one confirms within 8 KiB.
	if ok, err := c.need(10); err != nil || !ok {
		return 0, orUnindexable(err)
	}
	if end := id3End(c.view()); end > 0 {
		if end > metadataLimit || end >= c.size {
			return 0, errUnindexable
		}
		c.seek(end)
	}
	var first adtsFrame
	found := false
	for searched := 0; searched < 8192 && !found; searched++ {
		ok, err := c.need(7)
		if err != nil || !ok {
			return 0, orUnindexable(err)
		}
		if h, ok := adtsHeader(c.view()); ok {
			have, err := c.need(h.length + 7)
			if err != nil {
				return 0, err
			}
			if have {
				if next, ok := adtsHeader(c.view()[h.length:]); ok && next.rateIndex == h.rateIndex {
					first, found = h, true
					break
				}
			}
		}
		c.skip(1)
	}
	if !found {
		return 0, errUnindexable
	}
	var total, junk int64
	for {
		ok, err := c.need(7)
		if err != nil {
			return 0, err
		}
		if !ok {
			break
		}
		h, ok := adtsHeader(c.view())
		if !ok || h.rateIndex != first.rateIndex {
			if junk++; junk > junkLimit {
				return 0, errUnindexable
			}
			c.skip(1)
			continue
		}
		if ok, err = c.need(h.length); err != nil {
			return 0, err
		} else if !ok {
			break
		}
		b.at(c.pos, total)
		total += 1024 * int64(h.blocks)
		c.skip(int64(h.length))
	}
	return total, nil
}

// ── Ogg (Opus, Vorbis) ───────────────────────────────────────────────

// opusPacketFrames is the samples in one Opus packet at 48 kHz (RFC 6716 §3.1).
func opusPacketFrames(p []byte) int64 {
	if len(p) == 0 {
		return 0
	}
	toc := p[0]
	config := int(toc >> 3)
	var size int
	switch {
	case config < 12:
		size = [4]int{480, 960, 1920, 2880}[config&3]
	case config < 16:
		size = [2]int{480, 960}[config&1]
	default:
		size = [4]int{120, 240, 480, 960}[config&3]
	}
	count := 1
	switch toc & 3 {
	case 1, 2:
		count = 2
	case 3:
		count = 0
		if len(p) > 1 {
			count = int(p[1] & 0x3f)
		}
	}
	return int64(size * count)
}

func indexOgg(c *byteCursor, b *indexBuilder, codec string) (int64, error) {
	headers := 2
	if codec == "vorbis" {
		headers = 3
	}
	var serial uint32
	haveStream := false
	completed := 0
	var total, granule, junk int64
	var partial []byte
	for {
		ok, err := c.need(27)
		if err != nil {
			return 0, err
		}
		if !ok {
			break
		}
		v := c.view()
		if string(v[:4]) != "OggS" || v[4] != 0 {
			if junk++; junk > junkLimit {
				return 0, errUnindexable
			}
			c.skip(1)
			continue
		}
		count := int(v[26])
		if ok, err = c.need(27 + count); err != nil {
			return 0, err
		} else if !ok {
			break // a truncated last page carries nothing
		}
		v = c.view()
		segments := v[27 : 27+count]
		size := 0
		for _, s := range segments {
			size += int(s)
		}
		if ok, err = c.need(27 + count + size); err != nil {
			return 0, err
		} else if !ok {
			break
		}
		v = c.view()
		flags := v[5]
		pageGranule := int64(binary.LittleEndian.Uint64(v[6:14]))
		pageSerial := binary.LittleEndian.Uint32(v[14:18])
		offset := c.pos
		body := v[27+count : 27+count+size]
		c.skip(int64(27 + count + size))
		if !haveStream {
			// The first logical stream whose first packet is the codec's header.
			if flags&2 == 0 || !oggCodecHeader(body, codec) {
				continue
			}
			serial, haveStream = pageSerial, true
		}
		if pageSerial != serial {
			continue
		}
		if flags&1 == 0 {
			partial = partial[:0]
			// A page that starts a packet, past the headers: a place to start.
			if completed >= headers {
				frame := total
				if codec == "vorbis" {
					frame = granule
				}
				b.at(offset, frame)
			}
		}
		at := 0
		for _, n := range segments {
			partial = append(partial, body[at:at+int(n)]...)
			at += int(n)
			if n < 255 {
				if completed >= headers && codec == "opus" {
					total += opusPacketFrames(partial)
				}
				completed++
				partial = partial[:0]
			}
		}
		if len(partial) > 1<<20 {
			return 0, errUnindexable
		}
		if pageGranule >= 0 && (completed > 0) {
			if pageGranule < granule {
				return 0, errUnindexable
			}
			granule = pageGranule
		}
	}
	if completed <= headers {
		return 0, errUnindexable
	}
	return total, nil
}

func oggCodecHeader(packet []byte, codec string) bool {
	if codec == "opus" {
		return len(packet) >= 8 && string(packet[:8]) == "OpusHead"
	}
	return len(packet) >= 7 && packet[0] == 1 && string(packet[1:7]) == "vorbis"
}

func orUnindexable(err error) error {
	if err != nil {
		return err
	}
	return errUnindexable
}

// ── Storage ──────────────────────────────────────────────────────────

const seekIndexVersion = 1

// EncodeSeekIndex is the stored form: a version byte, the entry count, then
// each entry's offset and frame as increases over the one before (varints).
func EncodeSeekIndex(x *SeekIndex) []byte {
	if x == nil || len(x.Offsets) == 0 || len(x.Offsets) != len(x.Frames) {
		return nil
	}
	out := []byte{seekIndexVersion}
	out = binary.AppendUvarint(out, uint64(len(x.Offsets)))
	var offset, frame int64
	for i := range x.Offsets {
		out = binary.AppendUvarint(out, uint64(x.Offsets[i]-offset))
		out = binary.AppendUvarint(out, uint64(x.Frames[i]-frame))
		offset, frame = x.Offsets[i], x.Frames[i]
	}
	return out
}

// DecodeSeekIndex reads EncodeSeekIndex's form; anything else is refused.
func DecodeSeekIndex(data []byte) (*SeekIndex, error) {
	bad := errors.New("audiofacts: the stored seek index is unreadable")
	if len(data) < 2 || data[0] != seekIndexVersion {
		return nil, bad
	}
	data = data[1:]
	n, used := binary.Uvarint(data)
	if used <= 0 || n == 0 || n > MaxSeekEntries {
		return nil, bad
	}
	data = data[used:]
	x := &SeekIndex{Offsets: make([]int64, 0, n), Frames: make([]int64, 0, n)}
	var offset, frame int64
	for range n {
		d, used := binary.Uvarint(data)
		if used <= 0 || d > 1<<50 {
			return nil, bad
		}
		data = data[used:]
		f, used := binary.Uvarint(data)
		if used <= 0 || f > 1<<50 {
			return nil, bad
		}
		data = data[used:]
		offset, frame = offset+int64(d), frame+int64(f)
		if len(x.Offsets) > 0 && d == 0 {
			return nil, bad
		}
		x.Offsets, x.Frames = append(x.Offsets, offset), append(x.Frames, frame)
	}
	if len(data) != 0 {
		return nil, bad
	}
	return x, nil
}
