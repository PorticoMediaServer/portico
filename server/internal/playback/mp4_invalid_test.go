package playback

import (
	"encoding/binary"
	"testing"
)

func TestMP4TimescalesRejectEmptyHeaders(t *testing.T) {
	box := func(kind string, body []byte) []byte {
		b := make([]byte, len(body)+8)
		binary.BigEndian.PutUint32(b, uint32(len(b)))
		copy(b[4:], kind)
		copy(b[8:], body)
		return b
	}
	for _, kind := range []string{"tkhd", "mdhd"} {
		t.Run(kind, func(t *testing.T) {
			tk, md := make([]byte, 16), make([]byte, 16)
			binary.BigEndian.PutUint32(tk[12:], 1)
			binary.BigEndian.PutUint32(md[12:], 48000)
			if kind == "tkhd" {
				tk = nil
			} else {
				md = nil
			}
			init := box("moov", box("trak", append(box("tkhd", tk), box("mdia", box("mdhd", md))...)))
			if _, err := mp4Timescales(init); err == nil {
				t.Fatal("accepted an empty full-box header")
			}
		})
	}
}
