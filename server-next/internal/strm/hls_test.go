package strm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A master playlist is read as its best variant, and the playlist the decoder is given names
// only pieces of the bridge: no address of the target's, nothing a decoder could follow.
func TestPlaylistNamesOnlyBridgePieces(t *testing.T) {
	var origin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100000\nlow.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=900000\nhigh/index.m3u8?token=secret\n"+
				`#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=50000,URI="iframes.m3u8"`+"\n")
		case "/high/index.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n"+`#EXT-X-KEY:METHOD=AES-128,URI="/keys/1?token=secret",IV=0x01`+"\n"+
				`#EXT-X-MAP:URI="init.mp4"`+"\n#EXTINF:2,\npiece0.m4s\n#EXTINF:2,\n"+origin+"/elsewhere/piece1.m4s\n"+
				`#EXT-X-PART:DURATION=1,URI="part.m4s"`+"\n#EXT-X-KEY:METHOD=AES-128,URI=unquoted\n#EXTINF:2,\npiece0.m4s\n#EXT-X-ENDLIST\n")
		case "/live.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\npiece0.ts\n")
		case "/high/piece0.m4s":
			if r.Header.Get("Range") == "bytes=2-4" {
				w.Header().Set("Content-Range", "bytes 2-4/10")
				w.WriteHeader(http.StatusPartialContent)
				io.WriteString(w, "ece")
				return
			}
			io.WriteString(w, "piece zero")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	origin = server.URL
	if _, err := New().Open(context.Background(), server.URL+"/live.m3u8"); err != ErrRange {
		t.Fatalf("live playlist: %v", err)
	}
	target, err := New().Open(context.Background(), server.URL+"/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	p, ok := target.(*Playlist)
	if !ok || target.Size() != 0 {
		t.Fatalf("not read as a playlist: %T", target)
	}
	serve := func(piece, byteRange string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/input/abc", nil)
		if byteRange != "" {
			r.Header.Set("Range", byteRange)
		}
		w := httptest.NewRecorder()
		p.ServeHLS(w, r, "abc", piece)
		return w
	}
	text := serve("", "").Body.String()
	want := "#EXTM3U\n#EXT-X-TARGETDURATION:2\n" + `#EXT-X-KEY:METHOD=AES-128,URI="abc/0.ts",IV=0x01` + "\n" + `#EXT-X-MAP:URI="abc/1.ts"` +
		"\n#EXTINF:2,\nabc/2.ts\n#EXTINF:2,\nabc/3.ts\n#EXTINF:2,\nabc/2.ts\n#EXT-X-ENDLIST\n"
	if text != want {
		t.Fatalf("playlist given to the decoder:\n%s", text)
	}
	if got := p.pieces; len(got) != 4 || got[0] != origin+"/keys/1?token=secret" || got[3] != origin+"/elsewhere/piece1.m4s" {
		t.Fatalf("pieces: %q", got)
	}
	if w := serve("2.ts", ""); w.Code != 200 || w.Body.String() != "piece zero" || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("piece: %d %q %q", w.Code, w.Body.String(), w.Header().Get("Content-Type"))
	}
	if w := serve("2.ts", "bytes=2-4"); w.Code != 206 || w.Body.String() != "ece" || w.Header().Get("Content-Range") != "bytes 2-4/10" {
		t.Fatalf("byte range: %d %q", w.Code, w.Body.String())
	}
	for _, piece := range []string{"4.ts", "02.ts", "2", "2.ts.ts", "-1.ts"} {
		if w := serve(piece, ""); w.Code != 404 {
			t.Fatalf("%s: %d", piece, w.Code)
		}
	}
	if w := serve("1.ts", ""); w.Code != 502 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("missing piece: %d %q", w.Code, w.Body.String())
	}
}
