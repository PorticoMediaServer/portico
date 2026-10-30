package playback

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

type convertedFragmentClock struct {
	ready bool
	shift float64
	next  int
}

// HLS's MP4 muxer rebases each run even when output_ts_offset is set. Shift
// closed fragments before publishing; keep relative decode/composition times
// and the spacing between fragments intact, including audio encoder priming.
func (c *convertedFragmentClock) restore(init, media []byte, position float64) error {
	scales, err := mp4Timescales(init)
	if err != nil {
		return err
	}
	boxes, err := mp4Boxes(media, 0, len(media))
	if err != nil {
		return err
	}
	type field struct {
		at     int
		wide   bool
		scale  uint32
		value  uint64
		decode bool
	}
	fields := []field{}
	for _, box := range boxes {
		if box.kind == "sidx" {
			if box.end-box.body < 20 {
				return errMP4Box
			}
			version := media[box.body]
			at := box.body + 12
			wide := version == 1
			if version > 1 || wide && at+8 > box.end {
				return errMP4Box
			}
			value := uint64(binary.BigEndian.Uint32(media[at:]))
			if wide {
				value = binary.BigEndian.Uint64(media[at:])
			}
			fields = append(fields, field{at, wide, binary.BigEndian.Uint32(media[box.body+8:]), value, false})
		}
		if box.kind != "moof" {
			continue
		}
		trafs, err := mp4Boxes(media, box.body, box.end)
		if err != nil {
			return err
		}
		for _, traf := range trafs {
			if traf.kind != "traf" {
				continue
			}
			hd, err := mp4Child(media, traf, "tfhd")
			if err != nil || hd.end-hd.body < 8 {
				return errMP4Box
			}
			scale, ok := scales[binary.BigEndian.Uint32(media[hd.body+4:])]
			if !ok || scale == 0 {
				return errMP4Box
			}
			dt, err := mp4Child(media, traf, "tfdt")
			if err != nil || dt.end-dt.body < 8 {
				return errMP4Box
			}
			version := media[dt.body]
			at := dt.body + 4
			wide := version == 1
			if version > 1 || wide && at+8 > dt.end {
				return errMP4Box
			}
			value := uint64(binary.BigEndian.Uint32(media[at:]))
			if wide {
				value = binary.BigEndian.Uint64(media[at:])
			}
			fields = append(fields, field{at, wide, scale, value, true})
		}
	}
	if !c.ready {
		first := math.Inf(1)
		for _, f := range fields {
			if f.decode {
				first = math.Min(first, float64(f.value)/float64(f.scale))
			}
		}
		if math.IsInf(first, 0) {
			return errMP4Box
		}
		c.shift = position - first
		c.ready = true
	}
	for _, f := range fields {
		if f.scale == 0 || f.value > math.MaxInt64 || float64(f.value)/float64(f.scale) > HLSMaxSegments*HLSSegmentSeconds+86400 {
			return errMP4Box
		}
		value := int64(f.value) + int64(math.Round(c.shift*float64(f.scale)))
		if value < 0 || !f.wide && value > math.MaxUint32 {
			return errMP4Box
		}
		if f.wide {
			binary.BigEndian.PutUint64(media[f.at:], uint64(value))
		} else {
			binary.BigEndian.PutUint32(media[f.at:], uint32(value))
		}
	}
	return nil
}

func promoteConvertedFragments(staging, dir, initWindow string, from int, rendition *AudioRenditionPlan, clock *convertedFragmentClock) (int, error) {
	highest := max(from, clock.next) - 1
	if !clock.ready {
		first := fmt.Sprintf("segment-%06d.m4s", from)
		if rendition != nil {
			first = renditionSegmentFile(*rendition, from)
		}
		if !segmentReady(filepath.Join(staging, first)) {
			return highest, nil
		}
	}
	init, err := readConversionInit(filepath.Join(staging, initWindow))
	if os.IsNotExist(err) {
		return highest, nil
	}
	if err != nil {
		return highest, err
	}
	nameAt := func(i int) string {
		if rendition != nil {
			return renditionSegmentFile(*rendition, i)
		}
		return fmt.Sprintf("segment-%06d.m4s", i)
	}
	for i := max(from, clock.next); i < HLSMaxSegments; i++ {
		name := nameAt(i)
		source := filepath.Join(staging, name)
		if !segmentReady(source) {
			if segmentReady(filepath.Join(dir, name)) {
				highest = i
				clock.next = i + 1
				continue
			}
			break
		}
		// temp_file makes the final name the muxer's close fence.
		if rendition == nil {
			err = publishMP4Init(dir, init)
		} else {
			err = publishRenditionInit(dir, rendition.Ordinal, init)
		}
		if err != nil {
			return highest, err
		}
		f, err := os.Open(source)
		if err != nil {
			return highest, err
		}
		data, err := io.ReadAll(io.LimitReader(f, (128<<20)+1))
		f.Close()
		if err != nil || len(data) > 128<<20 {
			return highest, errMP4Box
		}
		if err = clock.restore(init, data, float64(i*HLSSegmentSeconds)); err != nil {
			return highest, err
		}
		target := filepath.Join(dir, name)
		if err = os.WriteFile(target+".tmp", data, 0600); err != nil {
			return highest, err
		}
		if err = os.Rename(target+".tmp", target); err != nil {
			return highest, err
		}
		_ = os.Remove(source)
		highest = i
		clock.next = i + 1
	}
	return highest, nil
}
