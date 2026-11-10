package audiotags

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func frame(id string, v []byte, ver byte) []byte {
	h := append([]byte(id), make([]byte, 6)...)
	if ver == 4 {
		n := len(v)
		for i := 7; i >= 4; i-- {
			h[i] = byte(n & 127)
			n >>= 7
		}
	} else {
		binary.BigEndian.PutUint32(h[4:], uint32(len(v)))
	}
	return append(h, v...)
}
func tag(b []byte, ver byte) []byte {
	h := []byte{'I', 'D', '3', ver, 0, 0, 0, 0, 0, 0}
	n := len(b)
	for i := 9; i >= 6; i-- {
		h[i] = byte(n & 127)
		n >>= 7
	}
	return append(h, b...)
}
func TestID3ListsAndNarrator(t *testing.T) {
	b := tag(append(frame("TPE1", []byte("\x03AC/DC\x00Guest"), 4), frame("TCOM", []byte("\x03Reader"), 4)...), 4)
	got := Read(bytes.NewReader(b), int64(len(b)))
	if !reflect.DeepEqual(got.Values["artist"], []string{"AC/DC", "Guest"}) || got.Values["composer"][0] != "Reader" {
		t.Fatalf("%+v", got)
	}
}
func TestID3Versions(t *testing.T) {
	for _, ver := range []byte{3, 4} {
		b := tag(frame("TIT2", []byte{1, 255, 254, 'A', 0, 'B', 0}, ver), ver)
		got := Read(bytes.NewReader(b), int64(len(b)))
		if got.Values["title"][0] != "AB" {
			t.Fatal(got)
		}
	}
	b := tag(append([]byte{'T', 'T', '2', 0, 0, 3}, []byte{0, 'O', 'K'}...), 2)
	got := Read(bytes.NewReader(b), int64(len(b)))
	if got.Values["title"][0] != "OK" {
		t.Fatal(got)
	}
}
func TestAPEGain(t *testing.T) {
	entry := make([]byte, 8)
	binary.LittleEndian.PutUint32(entry, 4)
	entry = append(entry, []byte("REPLAYGAIN_TRACK_GAIN\x00-7.0")...)
	footer := make([]byte, 32)
	copy(footer, "APETAGEX")
	binary.LittleEndian.PutUint32(footer[8:], 2000)
	binary.LittleEndian.PutUint32(footer[12:], uint32(len(entry)+32))
	binary.LittleEndian.PutUint32(footer[16:], 1)
	b := append(entry, footer...)
	got := Read(bytes.NewReader(b), int64(len(b)))
	if got.Values["replaygain_track_gain"][0] != "-7.0" {
		t.Fatal(got)
	}
}
func TestVorbisRepeatedValues(t *testing.T) {
	comments := []byte{0, 0, 0, 0, 2, 0, 0, 0}
	for _, s := range []string{"ARTIST=One", "ARTIST=Two"} {
		n := make([]byte, 4)
		binary.LittleEndian.PutUint32(n, uint32(len(s)))
		comments = append(comments, n...)
		comments = append(comments, s...)
	}
	n := len(comments)
	b := append([]byte{'f', 'L', 'a', 'C', 132, byte(n >> 16), byte(n >> 8), byte(n)}, comments...)
	got := Read(bytes.NewReader(b), int64(len(b)))
	if !reflect.DeepEqual(got.Values["artist"], []string{"One", "Two"}) {
		t.Fatal(got)
	}
}
func FuzzRead(f *testing.F) {
	f.Add([]byte("ID3\x04\x00\x00\x7f\x7f\x7f\x7f"))
	f.Add([]byte("OggS"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			return
		}
		got := Read(bytes.NewReader(b), int64(len(b)))
		if len(got.Values) > maxValues || len(got.Pictures) > 8 || len(got.Chapters) > 4096 {
			t.Fatal("bounds")
		}
	})
}

func TestRelativeVolumeGainAndPeak(t *testing.T) {
	b := tag(frame("RVA2", append([]byte("track\x00"), []byte{1, 0xfc, 0, 16, 0x40, 0}...), 4), 4)
	got := Read(bytes.NewReader(b), int64(len(b)))
	if got.Values["replaygain_track_gain"][0] != "-2.000000 dB" || got.Values["replaygain_track_peak"][0] != "0.500000000" {
		t.Fatal(got)
	}
}
