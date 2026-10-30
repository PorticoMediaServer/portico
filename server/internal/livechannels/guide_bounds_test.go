package livechannels

import (
	"io"
	"strings"
	"testing"
)

func TestGuideRejectsOversizedTokensBeforeReadingEntireField(t *testing.T) {
	for _, body := range []string{
		`<tv><programme channel="one"><desc>` + strings.Repeat("x", 4<<20) + `</desc></programme></tv>`,
		`<tv><!--` + strings.Repeat("x<y>", 1<<20) + `--></tv>`,
		`<tv><channel id="` + strings.Repeat("x>", 2<<20) + `"/></tv>`,
		`<tv><![CDATA[` + strings.Repeat("x<y>", 1<<20) + `]]></tv>`,
	} {
		r := &countingGuideReader{Reader: strings.NewReader(body)}
		d := newGuideDecoder(r)
		var e error
		for e == nil {
			_, e = d.Token()
		}
		if e == io.EOF {
			t.Error("hostile token accepted")
		}
		if r.read > 2<<20 {
			t.Errorf("decoder read %d bytes before rejecting oversized token", r.read)
		}
	}
}

type countingGuideReader struct {
	io.Reader
	read int
}

func (r *countingGuideReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.read += n
	return n, e
}

func TestGuideRejectsOversizedRecordWithSmallFields(t *testing.T) {
	in := SourceInput{Name: "Guide", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://example.invalid/one\n", Guide: `<tv><programme channel="one" start="20261013120000 +0000" stop="20261013130000 +0000"><title>Show</title>` + strings.Repeat(`<desc>`+strings.Repeat("x", 1024)+`</desc>`, 2048) + `</programme></tv>`}
	data, e := parse(in)
	defer data.close()
	if e == nil {
		t.Fatal("unbounded XML record accepted")
	}
}
