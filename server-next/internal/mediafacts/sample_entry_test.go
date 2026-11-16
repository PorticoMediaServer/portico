package mediafacts

import (
	"fmt"
	"testing"
)

// Transport stream tags describe stream types, not MP4 sample entries. Keeping
// them as entries makes a compatible recording look unplayable on its device.
func TestCodecSampleEntryBelongsToMP4(t *testing.T) {
	for _, row := range []struct{ container, tag, want string }{
		{"mpegts", "HEVC", ""},
		{"mov,mp4,m4a,3gp,3g2,mj2", "hvc1", "hvc1"},
	} {
		raw := fmt.Sprintf(`{"format":{"format_name":%q,"duration":"1"},"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","codec_tag_string":%q}]}`, row.container, row.tag)
		facts, err := decodeMediaProbe([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if got := facts.Streams[0].Detail.CodecTag; got != row.want {
			t.Fatalf("%s tag = %q, want %q", row.container, got, row.want)
		}
	}
}
