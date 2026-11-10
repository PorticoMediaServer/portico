package audiotags

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func box(kind string, b []byte) []byte {
	h := make([]byte, 8)
	binary.BigEndian.PutUint32(h, uint32(8+len(b)))
	copy(h[4:], kind)
	return append(h, b...)
}
func comment(s string) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b[4:], 1)
	n := make([]byte, 4)
	binary.LittleEndian.PutUint32(n, uint32(len(s)))
	return append(append(b, n...), []byte(s)...)
}
func TestOggOpusCommentAndMP4Freeform(t *testing.T) {
	packet := append([]byte("OpusTags"), comment("NARRATOR=Reader")...)
	h := make([]byte, 27)
	copy(h, "OggS")
	h[26] = 1
	b := append(append(h, byte(len(packet))), packet...)
	got := Read(bytes.NewReader(b), int64(len(b)))
	if len(got.Values["narrator"]) != 1 || got.Values["narrator"][0] != "Reader" {
		t.Fatal(got)
	}
	data := append([]byte{0, 0, 0, 1, 0, 0, 0, 0}, []byte("-4.5 dB")...)
	item := box("----", append(box("name", append(make([]byte, 4), []byte("REPLAYGAIN_ALBUM_GAIN")...)), box("data", data)...))
	b = append(box("ftyp", []byte("M4A \x00\x00\x00\x00")), box("moov", box("udta", box("meta", append(make([]byte, 4), box("ilst", item)...))))...)
	got = Read(bytes.NewReader(b), int64(len(b)))
	if got.Values["replaygain_album_gain"][0] != "-4.5 dB" {
		t.Fatal(got)
	}
}
func TestRIFFAndAIFFEmbeddedID3(t *testing.T) {
	id := tag(frame("TIT2", []byte("\x03Chunk title"), 4), 4)
	for _, big := range []bool{false, true} {
		h := make([]byte, 12)
		copy(h, "RIFF")
		copy(h[8:], "WAVE")
		chunk := make([]byte, 8)
		copy(chunk, "id3 ")
		binary.LittleEndian.PutUint32(chunk[4:], uint32(len(id)))
		if big {
			copy(h, "FORM")
			copy(h[8:], "AIFF")
			copy(chunk, "ID3 ")
			binary.BigEndian.PutUint32(chunk[4:], uint32(len(id)))
		}
		b := append(append(h, chunk...), id...)
		got := Read(bytes.NewReader(b), int64(len(b)))
		if len(got.Values["title"]) != 1 || got.Values["title"][0] != "Chunk title" {
			t.Fatal(got)
		}
	}
}
func TestMP4ChapterAtoms(t *testing.T) {
	ch := make([]byte, 9)
	ch[8] = 2
	for i, title := range []string{"Opening", "Closing"} {
		stamp := make([]byte, 9)
		binary.BigEndian.PutUint64(stamp, uint64(i)*100000000)
		stamp[8] = byte(len(title))
		ch = append(ch, stamp...)
		ch = append(ch, []byte(title)...)
	}
	b := append(box("ftyp", []byte("M4A \x00\x00\x00\x00")), box("moov", box("udta", box("chpl", ch)))...)
	got := Read(bytes.NewReader(b), int64(len(b)))
	if len(got.Chapters) != 2 || got.Chapters[0].End != 10 || got.Chapters[1].Title != "Closing" {
		t.Fatal(got)
	}
}
func TestPicturesKeepCoverTypeAndTimedLyrics(t *testing.T) {
	pic := append([]byte{3}, []byte("image/png\x00")...)
	pic = append(pic, 3, 0, 1, 2, 3)
	lyric := append([]byte{3}, []byte("eng\x00[12:34.5]Words")...)
	b := tag(append(frame("APIC", pic, 4), frame("USLT", lyric, 4)...), 4)
	got := Read(bytes.NewReader(b), int64(len(b)))
	if len(got.Pictures) != 1 || got.Pictures[0].Type != 3 || len(got.Lyrics) != 1 || got.Lyrics[0].Format != "lrc" {
		t.Fatal(got)
	}
}

func words(values ...uint32) []byte {
	b := make([]byte, len(values)*4)
	for i, v := range values {
		binary.BigEndian.PutUint32(b[i*4:], v)
	}
	return b
}
func TestQuickTimeChapterTextTrack(t *testing.T) {
	ftyp := box("ftyp", []byte("M4A \x00\x00\x00\x00"))
	makeMoov := func(offset uint32) []byte {
		audio := box("trak", append(box("tkhd", words(0, 0, 0, 1)), box("tref", box("chap", words(2)))...))
		tables := append(box("stts", words(0, 1, 2, 48000)), box("stsz", words(0, 0, 2, 9, 9))...)
		tables = append(tables, box("stco", words(0, 1, offset))...)
		tables = append(tables, box("stsc", words(0, 1, 1, 2, 1))...)
		mdia := append(box("mdhd", words(0, 0, 0, 48000, 96000)), box("hdlr", append(words(0, 0), []byte("text")...))...)
		mdia = append(mdia, box("minf", box("stbl", tables))...)
		chapter := box("trak", append(box("tkhd", words(0, 0, 0, 2)), box("mdia", mdia)...))
		return box("moov", append(audio, chapter...))
	}
	moov := makeMoov(0)
	moov = makeMoov(uint32(len(ftyp) + len(moov) + 8))
	payload := []byte("\x00\x07Opening\x00\x07Closing")
	b := append(append(ftyp, moov...), box("mdat", payload)...)
	got := Read(bytes.NewReader(b), int64(len(b)))
	if len(got.Chapters) != 2 || got.Chapters[1].Start != 1 || got.Chapters[1].Title != "Closing" {
		t.Fatal(got)
	}
}
