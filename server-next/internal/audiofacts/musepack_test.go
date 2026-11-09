package audiofacts

import (
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func musepackHeader8(total, silence int64) []byte {
	vlq := func(n int64) []byte {
		b := []byte{byte(n & 127)}
		for n >>= 7; n > 0; n >>= 7 {
			b = append([]byte{byte(n&127) | 128}, b...)
		}
		return b
	}
	payload := append([]byte{8}, vlq(total)...)
	payload = append(payload, vlq(silence)...)
	payload = append(payload, 0x1b, 0x10) // 44.1 kHz, stereo
	crc := make([]byte, 4)
	binary.BigEndian.PutUint32(crc, crc32.ChecksumIEEE(payload))
	payload = append(crc, payload...)
	return append(append([]byte("MPCKSH"), byte(len(payload)+3)), payload...)
}

func TestMusepackHeaderCompletionAndBounds(t *testing.T) {
	f := Facts{Codec: "mpc8", SampleRate: 44100, Channels: 2, RawFrames: 23040, DurationFrames: 23040, TrimSource: "mpc-header-pending"}
	header := musepackHeader8(22050, 32)
	h, err := ParseMusepackHeader("mpc8", header)
	if err != nil || h.SampleRate != 44100 || h.Channels != 2 || h.DurationFrames != 22018 || h.DecoderDelayFrames != 481 {
		t.Fatal(h, err)
	}
	if _, err = ParseMusepackHeader("mpc8", make([]byte, (1<<20)+1)); err != ErrInconsistent {
		t.Fatal("unbounded header accepted", err)
	}
	got, err := CompleteCodecHeaders(f, header)
	if err != nil || got.StartFrames != 513 || got.DurationFrames != 22018 || got.EndFrames != 509 || got.TrimSource != "musepack-header" {
		t.Fatal(got, err)
	}
	for name, bad := range map[string][]byte{
		"truncated":            header[:len(header)-1],
		"samples_past_raw":     musepackHeader8(24000, 0),
		"silence_past_samples": musepackHeader8(32, 33),
		"zero_samples":         musepackHeader8(0, 0),
		"overflow_varint":      append([]byte("MPCKSH"), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CompleteCodecHeaders(f, bad); err != ErrInconsistent {
				t.Fatal(err)
			}
		})
	}
	badCRC := append([]byte(nil), header...)
	badCRC[len(badCRC)-1] ^= 1
	if _, err = CompleteCodecHeaders(f, badCRC); err != ErrInconsistent {
		t.Fatal("bad CRC accepted", err)
	}
	f.SampleRate = 48000
	if _, err = CompleteCodecHeaders(f, header); err != ErrInconsistent {
		t.Fatal("mismatched measured rate accepted", err)
	}
	// SV7 true-gapless header counts; the synthesis delay is still raw output.
	h7 := make([]byte, 28)
	copy(h7, "MP+\x07")
	binary.LittleEndian.PutUint32(h7[4:8], 20)
	binary.LittleEndian.PutUint32(h7[20:24], 1<<31|166<<20)
	f = Facts{Codec: "mpc7", SampleRate: 44100, Channels: 2, RawFrames: 23040, DurationFrames: 23040}
	got, err = CompleteCodecHeaders(f, h7)
	if err != nil || got.DurationFrames != 22054 || got.StartFrames != 481 || got.EndFrames != 505 {
		t.Fatal(got, err)
	}
	binary.LittleEndian.PutUint32(h7[20:24], 1<<31|1153<<20)
	if _, err = CompleteCodecHeaders(f, h7); err != ErrInconsistent {
		t.Fatal("invalid last frame accepted", err)
	}
}
