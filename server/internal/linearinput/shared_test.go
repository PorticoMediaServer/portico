package linearinput_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/linearinput"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sharedFixture(t *testing.T) (livechannels.Input, *atomic.Int32, *httptest.Server) {
	t.Helper()
	count := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		packet := make([]byte, 188*256)
		for i := 0; i < len(packet); i += 188 {
			packet[i] = 0x47
			packet[i+3] = 0x10
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, e := w.Write(packet); e != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
			}
		}
	}))
	approval, e := (remotemedia.Policy{}).Approve(context.Background(), server.URL+"/")
	if e != nil {
		server.Close()
		t.Fatal(e)
	}
	return livechannels.Input{Locator: server.URL + "/stream", SourceID: server.URL, ChannelID: "one", Policy: remotemedia.Policy{Approvals: []remotemedia.Approval{approval}}, RetryWindowSeconds: 5}, count, server
}
func TestSharedChannelReadersHaveIndependentLifetime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	in, count, server := sharedFixture(t)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, e := linearinput.Open(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	second, e := linearinput.Open(ctx, in)
	if e != nil {
		first.Close()
		t.Fatal(e)
	}
	if count.Load() != 1 {
		t.Fatalf("upstream connections: %d", count.Load())
	}
	response, e := http.Get(second.URL())
	if e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 188)
	if _, e = io.ReadFull(response.Body, b); e != nil {
		t.Fatal(e)
	}
	if e = first.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = io.ReadFull(response.Body, b); e != nil {
		t.Fatalf("one viewer stopped another: %v", e)
	}
	response.Body.Close()
	if e = second.Close(); e != nil {
		t.Fatal(e)
	}
	third, e := linearinput.Open(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if count.Load() != 2 {
		t.Fatalf("idle channel not retired: %d", count.Load())
	}
	third.Close()
}

func TestSharedTransportReconnectsGeneratedStreamAndRecordsGap(t *testing.T) {
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	data, e := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10", "-t", "2", "-c:v", "mpeg2video", "-f", "mpegts", "pipe:1").Output()
	if e != nil {
		t.Fatal(e)
	}
	if len(data) == 0 || len(data)%188 != 0 || len(data) > 1<<20 {
		t.Fatal("bad generated transport stream")
	}
	var requests atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := requests.Add(1)
		for {
			if _, e := w.Write(data); e != nil {
				return
			}
			w.(http.Flusher).Flush()
			if attempt == 1 {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}))
	defer source.Close()
	approval, e := (remotemedia.Policy{}).Approve(ctx, source.URL+"/")
	if e != nil {
		t.Fatal(e)
	}
	gateway, e := linearinput.Open(ctx, livechannels.Input{Locator: source.URL + "/stream", SourceID: source.URL, ChannelID: "one", Policy: remotemedia.Policy{Approvals: []remotemedia.Approval{approval}}, RetryWindowSeconds: 5})
	if e != nil {
		t.Fatal(e)
	}
	defer gateway.Close()
	response, e := http.Get(gateway.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if _, e = io.ReadFull(response.Body, make([]byte, len(data)+188)); e != nil {
		t.Fatal(e)
	}
	if requests.Load() < 2 || gateway.Snapshot().Gaps == 0 {
		t.Fatalf("requests=%d span=%+v", requests.Load(), gateway.Snapshot())
	}
}

func TestSharedHLSSelectsBestVariantAndFetchesSegmentOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	var high, low, segments atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100000\nlow.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=500000\nhigh.m3u8\n")
		case "/low.m3u8":
			low.Add(1)
			io.WriteString(w, "#EXTM3U\n#EXTINF:2,\nsegment.ts\n")
		case "/high.m3u8":
			high.Add(1)
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-PART:DURATION=0.2,URI=\"https://unreviewed.invalid/partial\"\n#EXT-X-UNKNOWN:URI=\"https://unreviewed.invalid/extra\"\n#EXTINF:2,\nsegment.ts\n")
		case "/segment.ts":
			segments.Add(1)
			w.Write([]byte{0x47, 1, 2, 3})
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	approval, e := (remotemedia.Policy{}).Approve(ctx, source.URL+"/")
	if e != nil {
		t.Fatal(e)
	}
	in := livechannels.Input{SourceID: source.URL, ChannelID: "hls", Locator: source.URL + "/master.m3u8", Policy: remotemedia.Policy{Approvals: []remotemedia.Approval{approval}}}
	first, e := linearinput.Open(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	second, e := linearinput.Open(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	get := func(uri string) string {
		t.Helper()
		response, e := http.Get(uri)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		b, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != 200 {
			t.Fatalf("HTTP %d %v: %s", response.StatusCode, e, b)
		}
		return string(b)
	}
	uri := func(manifest string) string {
		t.Helper()
		for _, line := range strings.Split(manifest, "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				return line
			}
		}
		t.Fatal("no URI")
		return ""
	}
	for _, g := range []*linearinput.Gateway{first, second} {
		master := get(g.URL())
		if strings.Count(master, "#EXT-X-STREAM-INF:") != 1 || !strings.Contains(master, "BANDWIDTH=500000") {
			t.Fatalf("wrong master: %s", master)
		}
		media := get(uri(master))
		if strings.Contains(media, "unreviewed.invalid") || strings.Contains(media, "#EXT-X-PART:") {
			t.Fatalf("optional URL escaped: %s", media)
		}
		if get(uri(media)) != string([]byte{0x47, 1, 2, 3}) {
			t.Fatal("wrong media bytes")
		}
	}
	if low.Load() != 0 || high.Load() != 1 || segments.Load() != 1 {
		t.Fatalf("low=%d high=%d segments=%d", low.Load(), high.Load(), segments.Load())
	}
}

func TestSharedChannelRejoinDuringGraceRetainsUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	in, count, source := sharedFixture(t)
	defer source.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, e := linearinput.Open(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	closing := make(chan error, 1)
	go func() { closing <- first.Close() }()
	// Rejoin only after the last lease actually entered its grace period.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !linearinput.SharedInputInGraceForTest(in.SourceID, in.ChannelID) {
		select {
		case e := <-closing:
			t.Fatalf("last reader closed before grace was observed: %v", e)
		case <-deadline.C:
			t.Fatal("last reader did not enter its grace period")
		case <-tick.C:
		}
	}
	second, e := linearinput.Open(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	select {
	case e = <-closing:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not release its lease on rejoin")
	}
	if count.Load() != 1 {
		t.Fatalf("grace rejoin reopened input: %d", count.Load())
	}
	response, e := http.Get(second.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if _, e = io.ReadFull(response.Body, make([]byte, 188)); e != nil {
		t.Fatal(e)
	}
}
