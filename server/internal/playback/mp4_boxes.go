package playback

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

var errMP4InitChanged = errors.New("fragmented MP4 initialization changed")
var errMP4Box = errors.New("invalid fragmented MP4 boxes")

type mp4Box struct {
	kind             string
	start, body, end int
}

func mp4Boxes(data []byte, from, to int) ([]mp4Box, error) {
	out := []mp4Box{}
	for at := from; at < to; {
		if to-at < 8 {
			return nil, errMP4Box
		}
		size := uint64(binary.BigEndian.Uint32(data[at:]))
		header := 8
		if size == 1 {
			if to-at < 16 {
				return nil, errMP4Box
			}
			size = binary.BigEndian.Uint64(data[at+8:])
			header = 16
		}
		if size == 0 {
			size = uint64(to - at)
		}
		if size < uint64(header) || size > uint64(to-at) || len(out) > 100000 {
			return nil, errMP4Box
		}
		end := at + int(size)
		out = append(out, mp4Box{string(data[at+4 : at+8]), at, at + header, end})
		at = end
	}
	return out, nil
}
func mp4Child(data []byte, parent mp4Box, kind string) (mp4Box, error) {
	boxes, e := mp4Boxes(data, parent.body, parent.end)
	if e != nil {
		return mp4Box{}, e
	}
	for _, b := range boxes {
		if b.kind == kind {
			return b, nil
		}
	}
	return mp4Box{}, errMP4Box
}
func mp4Timescales(init []byte) (map[uint32]uint32, error) {
	boxes, e := mp4Boxes(init, 0, len(init))
	if e != nil {
		return nil, e
	}
	out := map[uint32]uint32{}
	for _, b := range boxes {
		if b.kind != "moov" {
			continue
		}
		tracks, e := mp4Boxes(init, b.body, b.end)
		if e != nil {
			return nil, e
		}
		for _, t := range tracks {
			if t.kind != "trak" {
				continue
			}
			tk, e := mp4Child(init, t, "tkhd")
			if e != nil {
				return nil, e
			}
			md, e := mp4Child(init, t, "mdia")
			if e != nil {
				return nil, e
			}
			hd, e := mp4Child(init, md, "mdhd")
			if e != nil {
				return nil, e
			}
			ti, mi := tk.body+12, hd.body+12
			if tk.end-tk.body < 4 || hd.end-hd.body < 4 || init[tk.body] > 1 || init[hd.body] > 1 {
				return nil, errMP4Box
			}
			if init[tk.body] == 1 {
				ti = tk.body + 20
			}
			if init[hd.body] == 1 {
				mi = hd.body + 20
			}
			if ti+4 > tk.end || mi+4 > hd.end {
				return nil, errMP4Box
			}
			id, scale := binary.BigEndian.Uint32(init[ti:]), binary.BigEndian.Uint32(init[mi:])
			if scale == 0 {
				return nil, errMP4Box
			}
			out[id] = scale
		}
	}
	if len(out) == 0 {
		return nil, errMP4Box
	}
	return out, nil
}

// rebaseFragments restores the absolute clock the MP4 segment muxer discards.
// Each track is shifted as a unit; composition offsets and inter-fragment decode
// distances are preserved. No packet bytes or sample offsets change.
func rebaseFragments(init, media []byte, position float64) error {
	scales, e := mp4Timescales(init)
	if e != nil {
		return e
	}
	boxes, e := mp4Boxes(media, 0, len(media))
	if e != nil {
		return e
	}
	type stamp struct {
		at      int
		version byte
		value   uint64
		track   uint32
	}
	stamps := []stamp{}
	first := map[uint32]uint64{}
	type composition struct {
		at, versionAt int
		value         int64
		track         uint32
	}
	compositions := []composition{}
	minimum := map[uint32]int64{}
	for _, box := range boxes {
		if box.kind != "moof" {
			continue
		}
		trafs, e := mp4Boxes(media, box.body, box.end)
		if e != nil {
			return e
		}
		for _, traf := range trafs {
			if traf.kind != "traf" {
				continue
			}
			hd, e := mp4Child(media, traf, "tfhd")
			if e != nil || hd.end-hd.body < 8 {
				return errMP4Box
			}
			id := binary.BigEndian.Uint32(media[hd.body+4:])
			dt, e := mp4Child(media, traf, "tfdt")
			if e != nil || dt.end-dt.body < 8 {
				return errMP4Box
			}
			v := media[dt.body]
			at := dt.body + 4
			value := uint64(binary.BigEndian.Uint32(media[at:]))
			if v == 1 {
				if at+8 > dt.end {
					return errMP4Box
				}
				value = binary.BigEndian.Uint64(media[at:])
			} else if v != 0 {
				return errMP4Box
			}
			if _, ok := scales[id]; !ok {
				return errMP4Box
			}
			if _, ok := first[id]; !ok {
				first[id] = value
			}
			stamps = append(stamps, stamp{at, v, value, id})
			children, err := mp4Boxes(media, traf.body, traf.end)
			if err != nil {
				return err
			}
			flags := binary.BigEndian.Uint32(media[hd.body:]) & 0xffffff
			cursor := hd.body + 8
			if flags&1 != 0 {
				cursor += 8
			}
			if flags&2 != 0 {
				cursor += 4
			}
			var defaultDuration uint32
			if flags&8 != 0 {
				if cursor+4 > hd.end {
					return errMP4Box
				}
				defaultDuration = binary.BigEndian.Uint32(media[cursor:])
			}
			decode := int64(value - first[id])
			for _, run := range children {
				if run.kind != "trun" {
					continue
				}
				if run.end-run.body < 8 {
					return errMP4Box
				}
				flags := binary.BigEndian.Uint32(media[run.body:]) & 0xffffff
				n := binary.BigEndian.Uint32(media[run.body+4:])
				if n > 1000000 {
					return errMP4Box
				}
				cursor := run.body + 8
				if flags&1 != 0 {
					cursor += 4
				}
				if flags&4 != 0 {
					cursor += 4
				}
				for j := uint32(0); j < n; j++ {
					duration := defaultDuration
					if flags&0x100 != 0 {
						if cursor+4 > run.end {
							return errMP4Box
						}
						duration = binary.BigEndian.Uint32(media[cursor:])
						cursor += 4
					}
					if flags&0x200 != 0 {
						cursor += 4
					}
					if flags&0x400 != 0 {
						cursor += 4
					}
					offset := int64(0)
					if flags&0x800 != 0 {
						if cursor+4 > run.end {
							return errMP4Box
						}
						offset = int64(binary.BigEndian.Uint32(media[cursor:]))
						if media[run.body] == 1 {
							offset = int64(int32(offset))
						}
						compositions = append(compositions, composition{cursor, run.body, offset, id})
						cursor += 4
					}
					if cursor > run.end {
						return errMP4Box
					}
					presentation := decode + offset
					if old, ok := minimum[id]; !ok || presentation < old {
						minimum[id] = presentation
					}
					decode += int64(duration)
				}
			}

		}
	}
	if len(stamps) == 0 {
		return errMP4Box
	}

	// MP4 shifts negative initial DTS forward and stores the reorder delay in
	// composition offsets. Normalize the presentation origin too; rebasing tfdt
	// alone would add that delay again at every restarted window.
	for _, c := range compositions {
		value := c.value - minimum[c.track] + int64(scales[c.track])
		if value < 0 || value > math.MaxInt32 {
			return errMP4Box
		}
		media[c.versionAt] = 0
		binary.BigEndian.PutUint32(media[c.at:], uint32(int32(value)))
	}
	for _, s := range stamps {
		base := uint64(math.Round(position * float64(scales[s.track])))
		hasComposition := false
		for _, c := range compositions {
			if c.track == s.track {
				hasComposition = true
				break
			}
		}
		if !hasComposition {
			base += uint64(scales[s.track])
		}
		if s.value < first[s.track] {
			return errMP4Box
		}
		value := base + s.value - first[s.track]
		if s.version == 0 {
			if value > math.MaxUint32 {
				return errMP4Box
			}
			binary.BigEndian.PutUint32(media[s.at:], uint32(value))
		} else {
			binary.BigEndian.PutUint64(media[s.at:], value)
		}
	}
	return nil
}

// splitFragmentedMP4 bounds memory even for malformed converter output.
func splitFragmentedMP4(path string) ([]byte, []byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, nil, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, 128<<20+1))
	if e != nil || len(data) > 128<<20 {
		return nil, nil, errMP4Box
	}
	boxes, e := mp4Boxes(data, 0, len(data))
	if e != nil {
		return nil, nil, e
	}
	for _, b := range boxes {
		if b.kind == "moof" {
			if b.start > 4<<20 {
				return nil, nil, errMP4Box
			}
			return data[:b.start], data[b.start:], nil
		}
	}
	return nil, nil, errMP4Box
}
func publishMP4Init(dir string, init []byte) error {
	target := filepath.Join(dir, "init.mp4")
	if old, e := os.ReadFile(target); e == nil {
		if !bytes.Equal(old, init) {
			return errMP4InitChanged
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	if e := os.WriteFile(target+".tmp", init, 0600); e != nil {
		return e
	}
	return os.Rename(target+".tmp", target)
}
func promoteFMP4Segments(staging, dir string, t *copyTimeline, from int, final bool) (int, error) {
	highest := from - 1
	for i := from; i < t.count(); i++ {
		name := fmt.Sprintf("segment-%06d.m4s", i)
		source := filepath.Join(staging, fmt.Sprintf("segment-%06d.mp4", i))
		if _, e := os.Stat(source); e != nil {
			if segmentReady(filepath.Join(dir, name)) {
				highest = i
				continue
			}
			break
		}
		if !final {
			if _, e := os.Stat(filepath.Join(staging, fmt.Sprintf("segment-%06d.mp4", i+1))); e != nil {
				break
			}
		}
		init, media, e := splitFragmentedMP4(source)
		if e != nil {
			return highest, e
		}
		init, e = presentationOriginInit(init)
		if e != nil {
			return highest, e
		}
		if e = publishMP4Init(dir, init); e != nil {
			return highest, e
		}
		if e = rebaseFragments(init, media, t.Boundaries[i]); e != nil {
			return highest, e
		}
		path := filepath.Join(dir, name)
		if e = os.WriteFile(path+".tmp", media, 0600); e != nil {
			return highest, e
		}
		if e = os.Rename(path+".tmp", path); e != nil {
			return highest, e
		}
		_ = os.Remove(source)
		highest = i
	}
	return highest, nil
}

// A single one-second media edit lets every fragment express preroll with
// unsigned composition offsets. Negative trun offsets are interpreted
// inconsistently by demuxers. The edit is identical across every seek window;
// media DTS and composition offsets are shifted together by rebaseFragments.
func presentationOriginInit(init []byte) ([]byte, error) {
	scales, e := mp4Timescales(init)
	if e != nil {
		return nil, e
	}
	box := func(kind string, body []byte) []byte {
		out := make([]byte, 8+len(body))
		binary.BigEndian.PutUint32(out, uint32(len(out)))
		copy(out[4:], kind)
		copy(out[8:], body)
		return out
	}
	var rewrite func([]byte, string) ([]byte, error)
	rewrite = func(data []byte, parent string) ([]byte, error) {
		parts, e := mp4Boxes(data, 0, len(data))
		if e != nil {
			return nil, e
		}
		out := []byte{}
		for _, part := range parts {
			body := data[part.body:part.end]
			if part.kind == "moov" || part.kind == "trak" {
				body, e = rewrite(body, part.kind)
				if e != nil {
					return nil, e
				}
			}
			if parent == "trak" && part.kind == "edts" {
				continue
			}
			out = append(out, box(part.kind, body)...)
		}
		if parent == "trak" {
			var id uint32
			for _, p := range parts {
				if p.kind == "tkhd" {
					at := p.body + 12
					if data[p.body] == 1 {
						at = p.body + 20
					}
					if at+4 > p.end {
						return nil, errMP4Box
					}
					id = binary.BigEndian.Uint32(data[at:])
				}
			}
			scale, ok := scales[id]
			if !ok || scale > math.MaxInt32 {
				return nil, errMP4Box
			}
			edit := make([]byte, 20)
			binary.BigEndian.PutUint32(edit[4:], 1)
			binary.BigEndian.PutUint32(edit[12:], scale)
			binary.BigEndian.PutUint16(edit[16:], 1)
			out = append(out, box("edts", box("elst", edit))...)
		}
		return out, nil
	}
	return rewrite(init, "")
}
