package subtitles

import (
	"encoding/binary"
	"testing"
)

func pgsSegment(kind byte, payload []byte) []byte {
	out := []byte{'P', 'G', 0, 0, 0, 0, 0, 0, 0, 0, kind, 0, 0}
	binary.BigEndian.PutUint16(out[11:], uint16(len(payload)))
	return append(out, payload...)
}

func pgsComposition(flags ...byte) []byte {
	seg := []byte{7, 128, 4, 56, 0x10, 0, 1, 0x80, 0, 0, byte(len(flags))}
	for i, f := range flags {
		seg = append(seg, 0, byte(i), 0, f, 0, 10, 0, 10)
		if f&0x80 != 0 {
			seg = append(seg, 0, 0, 0, 0, 0, 4, 0, 4)
		}
	}
	return pgsSegment(0x16, seg)
}

func TestPGSLinesCountForcedCompositions(t *testing.T) {
	var b []byte
	b = append(b, pgsComposition(0x40)...)            // forced
	b = append(b, pgsSegment(0x80, nil)...)           // end of display set
	b = append(b, pgsComposition()...)                // clears the screen: not a line
	b = append(b, pgsComposition(0x00)...)            // ordinary
	b = append(b, pgsComposition(0x40|0x80, 0x40)...) // forced, cropped
	b = append(b, pgsComposition(0x40, 0x00)...)      // one ordinary object: not forced
	got := imageLines("pgs", b, nil)
	if got != (Lines{Forced: 2, Total: 4}) || !got.Mixed() {
		t.Fatalf("lines %+v", got)
	}
	if all := imageLines("pgs", pgsComposition(0x40), nil); all.Mixed() || all.Total != 1 {
		t.Fatalf("an all-forced track is not mixed: %+v", all)
	}
	if none := imageLines("pgs", []byte("not a sup"), nil); none.Total != 0 {
		t.Fatalf("garbage %+v", none)
	}
}

// vobsubPack wraps one subpicture in an MPEG-2 pack and a private stream 1 packet.
func vobsubPack(spu []byte) []byte {
	pack := []byte{0, 0, 1, 0xba, 0x44, 0, 4, 0, 4, 1, 1, 0x89, 0xc3, 0xf8}
	pes := []byte{0, 0, 1, 0xbd, 0, 0, 0x81, 0x80, 0x05, 0x21, 0, 1, 0, 1, 0x20}
	pes = append(pes, spu...)
	binary.BigEndian.PutUint16(pes[4:], uint16(len(pes)-6))
	return append(pack, pes...)
}

func vobsubSPU(forced bool) []byte {
	control := []byte{0, 0, 0, 0} // delay, next (patched)
	if forced {
		control = append(control, 0x00)
	}
	control = append(control, 0x01, 0xff)
	spu := []byte{0, 0, 0, 4}
	at := len(spu)
	binary.BigEndian.PutUint16(control[2:], uint16(at))
	spu = append(spu, control...)
	binary.BigEndian.PutUint16(spu, uint16(len(spu)))
	binary.BigEndian.PutUint16(spu[2:], uint16(at))
	return spu
}

func TestVobSubLinesCountForcedSubpictures(t *testing.T) {
	var b []byte
	b = append(b, vobsubPack(vobsubSPU(true))...)
	b = append(b, vobsubPack(vobsubSPU(false))...)
	b = append(b, vobsubPack(vobsubSPU(false))...)
	got := imageLines("vobsub", []byte("idx"), b)
	if got != (Lines{Forced: 1, Total: 3}) {
		t.Fatalf("lines %+v", got)
	}
}
