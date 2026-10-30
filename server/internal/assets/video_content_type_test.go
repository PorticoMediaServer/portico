package assets

import "testing"

func TestVideoContentTypeUsesMeasuredDemuxIdentity(t *testing.T) {
	for _, tc := range []struct{ format, container, mime string }{
		{"matroska,webm", "matroska", "video/x-matroska"},
		{"mpegts", "mpegts", "video/mp2t"},
	} {
		facts, err := decodeMediaProbe([]byte(`{"format":{"format_name":"` + tc.format + `","duration":"2"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":320,"height":240}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if facts.Container != tc.container || facts.VideoCodec != "h264" || OriginalContentType(facts.Container, facts.VideoCodec) != tc.mime {
			t.Fatal("measured demux identity lost its original media type", facts)
		}
	}
	for _, tc := range []struct{ container, mime string }{
		{"mkv", "video/x-matroska"}, {"ts", "video/mp2t"}, {"m2ts", "video/mp2t"},
		{"webm", "video/webm"}, {"unclassified", "application/octet-stream"},
	} {
		if got := OriginalContentType(tc.container, "h264"); got != tc.mime {
			t.Fatal("original alias/unknown type changed", tc.container, got)
		}
	}
}
