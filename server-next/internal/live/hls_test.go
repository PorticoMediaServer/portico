package live

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Most IPTV channels are HLS. A channel whose address answers a master playlist plays its best
// variant: the pieces arrive whole and in order, from a few behind the newest, and keep coming
// as the playlist moves on.
func TestAnHLSChannelPlaysAsOneStream(t *testing.T) {
	var newest atomic.Int64
	newest.Store(9)
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/channel.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000,CODECS=\"avc1.4d401f,mp4a.40.2\"\nlow/index.m3u8\n"+
				"#EXT-X-STREAM-INF:BANDWIDTH=5000000\nhigh/index.m3u8\n")
		case r.URL.Path == "/high/index.m3u8":
			n := newest.Add(1) // the channel moves on one piece each time it is asked
			var list strings.Builder
			fmt.Fprintf(&list, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:%d\n", n-5)
			for s := n - 5; s <= n; s++ {
				fmt.Fprintf(&list, "#EXTINF:1.0,\npiece-%d.ts\n", s)
			}
			fmt.Fprint(w, list.String())
		case strings.HasPrefix(r.URL.Path, "/high/piece-"):
			var s int
			fmt.Sscanf(r.URL.Path, "/high/piece-%d.ts", &s)
			piece := make([]byte, 3*packet)
			for p := 0; p < 3; p++ {
				piece[p*packet], piece[p*packet+1] = 0x47, byte(s)
			}
			w.Write(piece)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(src.Close)
	tuners := NewTuners(t.TempDir()+"/live", src.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now,
		func() Tuning { return DefaultTuning })
	t.Cleanup(tuners.Close)
	in, err := tuners.Tune(1, 9, 1, src.URL+"/channel.m3u8", &Use{Name: "Living room"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out := &firstBytes{want: 6 * 3 * packet, done: cancel}
	in.Read(ctx, time.Unix(1, 0), out)
	got := out.Bytes()
	if len(got) < 6*3*packet {
		t.Fatalf("read %d bytes of the channel: %v", len(got), in.Stopped())
	}
	// The first playlist names pieces 5 to 10: the channel starts at 8 and carries on in order.
	for p := 0; p < 6*3; p++ {
		if got[p*packet] != 0x47 || got[p*packet+1] != byte(8+p/3) {
			t.Fatalf("packet %d is %x %d, want piece %d", p, got[p*packet], got[p*packet+1], 8+p/3)
		}
	}
}

// A playlist that keeps moving while none of its pieces can be fetched is a channel that
// stopped: after the retry time it is said to have stopped, rather than leaving the viewer
// waiting for ever.
func TestAnHLSChannelWhosePiecesNeverComeStops(t *testing.T) {
	var newest atomic.Int64
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.m3u8" {
			http.NotFound(w, r)
			return
		}
		n := newest.Add(1)
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:0.2\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXTINF:0.2,\npiece-%d.ts\n", n, n)
	}))
	t.Cleanup(src.Close)
	tuners := NewTuners(t.TempDir()+"/live", src.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now,
		func() Tuning { return Tuning{Stretch: time.Minute, Retry: time.Second} })
	t.Cleanup(tuners.Close)
	in, err := tuners.Tune(1, 9, 1, src.URL+"/index.m3u8", &Use{Name: "Living room"})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(15 * time.Second); in.Stopped() == nil && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
	}
	if in.Stopped() == nil {
		t.Fatal("a channel whose pieces never came was never said to have stopped")
	}
}
