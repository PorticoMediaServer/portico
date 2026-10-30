package audiotags

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMusicMKAWMATagsR29(t *testing.T) {
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	for _, tc := range []struct{ ext, codec string }{{"mka", "flac"}, {"wma", "wmav2"}} {
		t.Run(tc.ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tone."+tc.ext)
			if out, e := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "sine=duration=0.1", "-c:a", tc.codec, "-metadata", "title=Tagged tone", "-metadata", "artist=Fixture artist", "-metadata", "album=Fixture album", path).CombinedOutput(); e != nil {
				t.Fatalf("%v %s", e, out)
			}
			file, e := os.Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer file.Close()
			st, _ := file.Stat()
			tags := Read(file, st.Size())
			if len(tags.Values["title"]) != 1 || tags.Values["title"][0] != "Tagged tone" || len(tags.Values["artist"]) != 1 || tags.Values["artist"][0] != "Fixture artist" {
				t.Fatal(tags.Values)
			}
		})
	}
}
func TestDFFDIINTagsAndInvalidLengthR29(t *testing.T) {
	chunk := func(id string, b []byte) []byte {
		h := make([]byte, 12)
		copy(h, id)
		binary.BigEndian.PutUint64(h[4:], uint64(len(b)))
		h = append(h, b...)
		if len(b)%2 != 0 {
			h = append(h, 0)
		}
		return h
	}
	title := append([]byte{0, 0, 0, 4}, []byte("Tone")...)
	body := append([]byte("DSD "), chunk("DIIN", chunk("DITI", title))...)
	raw := chunk("FRM8", body)
	tags := Read(bytes.NewReader(raw), int64(len(raw)))
	if len(tags.Values["title"]) != 1 || tags.Values["title"][0] != "Tone" {
		t.Fatal(tags)
	}
	broken := append([]byte(nil), raw...)
	binary.BigEndian.PutUint64(broken[20:], 1<<62)
	if got := Read(bytes.NewReader(broken), int64(len(broken))); len(got.Values["title"]) != 0 {
		t.Fatal(got)
	}
}
